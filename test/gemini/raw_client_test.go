package gemini_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

type rawRecord struct {
	At      time.Time       `json:"at"`
	Phase   string          `json:"phase"`
	Payload json.RawMessage `json:"payload"`
}

type geminiFunctionCall struct {
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

type geminiFunctionResponse struct {
	Name     string          `json:"name"`
	Response json.RawMessage `json:"response"`
}

type geminiPart struct {
	Text             string                  `json:"text,omitempty"`
	Thought          bool                    `json:"thought,omitempty"`
	ThoughtSignature string                  `json:"thoughtSignature,omitempty"`
	FunctionCall     *geminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role"`
	Parts []geminiPart `json:"parts"`
}

type geminiUsageMetadata struct {
	ThoughtsTokenCount int `json:"thoughtsTokenCount"`
}

type geminiCandidate struct {
	Content      geminiContent `json:"content"`
	FinishReason string        `json:"finishReason"`
}

type geminiResponse struct {
	Candidates    []geminiCandidate   `json:"candidates"`
	UsageMetadata geminiUsageMetadata `json:"usageMetadata"`
	Error         *geminiAPIError     `json:"error,omitempty"`
}

type geminiAPIError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

type StreamMode bool

const (
	StreamOn  StreamMode = true
	StreamOff StreamMode = false
)

func (m StreamMode) String() string {
	if m {
		return "stream"
	}
	return "nostream"
}

func (m StreamMode) suffix() string {
	return m.String()
}

type turnOpts struct {
	withToolChoice bool
	withReasoning  bool
}

type turnResult struct {
	Thinking        strings.Builder
	Content         strings.Builder
	ToolCalls       []ToolRecord
	ModelParts      []geminiPart // verbatim model parts for multi-turn replay (thoughtSignature, etc.)
	FinishReason    string
	ReasoningTokens int
	Records         []rawRecord
}

func runRawCharacterize(t *testing.T, spec ModelSpec, stream StreamMode) (rawPath string, report CharacterReport) {
	t.Helper()
	start := time.Now()
	path := geminiRawPath(t, spec.Model, stream)
	transcriptPath := geminiTranscriptPath(t, spec.Model, stream)
	doc := newTranscriptDoc(spec, stream)
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create raw stream %s: %v", path, err)
	}
	defer f.Close()

	collector := newWireCollector(spec, stream)
	write := func(rec rawRecord) {
		if err := writeRawRecord(f, rec); err != nil {
			t.Fatalf("write raw record: %v", err)
		}
		collector.ingestRaw(rec)
	}
	write(rawRecord{
		At:    time.Now().UTC(),
		Phase: "session_start",
		Payload: mustJSON(map[string]any{
			"model":                      spec.Model,
			"stream":                     bool(stream),
			"mode":                       stream.String(),
			"api":                        "generateContent",
			"reasoning_effort_default":   defaultReasoningEffort,
			"reasoning_effort_requested": effectiveReasoningEffort(spec),
			"thinking_probe":             map[string]any{"thinkingLevel": effectiveReasoningEffort(spec), "includeThoughts": true},
		}),
	})

	finish := func(report CharacterReport) CharacterReport {
		writeProbeSummary(collector, write)
		doc.appendProbeContract(report)
		if report.Error != "" {
			doc.appendError(report.Error)
		}
		writeTranscript(t, transcriptPath, doc.b.String())
		return report
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	client := &http.Client{Timeout: 2 * time.Minute}
	contents := []geminiContent{{Role: "user", Parts: []geminiPart{{Text: weatherPrompt}}}}

	turn1, turn1Opts, err := geminiTurnWithProbeFallbacks(ctx, client, spec, contents, stream, collector, write)
	if err != nil {
		report = finish(collector.report(time.Since(start), err.Error()))
		t.Logf("wrote raw capture -> %s", path)
		return path, report
	}
	collector.ingestTurn(turn1, 1)
	doc.appendTurn1(weatherPrompt, turn1)
	if len(turn1.ToolCalls) == 0 {
		report = finish(collector.report(time.Since(start), "turn 1 produced no tool call"))
		return path, report
	}
	tc := turn1.ToolCalls[0]
	collector.noteToolCalled(tc)

	if len(turn1.ModelParts) == 0 {
		report = finish(collector.report(time.Since(start), "turn 1 produced no replayable model parts"))
		return path, report
	}
	collector.noteModelReplay(turn1.ModelParts)
	contents = append(contents, geminiContent{
		Role:  "model",
		Parts: turn1.ModelParts,
	})
	contents = append(contents, geminiContent{
		Role: "user",
		Parts: []geminiPart{{
			FunctionResponse: &geminiFunctionResponse{
				Name:     tc.Name,
				Response: json.RawMessage(fakeWeatherToolResult),
			},
		}},
	})
	doc.appendToolResult(tc.Name, fakeWeatherToolResult)

	turn2, err := geminiTurn(ctx, client, spec, contents, turn1Opts.withToolChoiceFalse(), stream, write)
	if err != nil {
		report = finish(collector.report(time.Since(start), err.Error()))
		return path, report
	}
	collector.noteToolSucceeded()
	collector.ingestTurn(turn2, 2)
	doc.appendTurn2(turn2)

	report = finish(collector.report(time.Since(start), ""))
	t.Logf("wrote raw capture -> %s (%d records, mode=%s)", path, collector.recordCount, stream)
	return path, report
}

func writeProbeSummary(collector *wireCollector, write func(rawRecord)) {
	write(rawRecord{
		At:    time.Now().UTC(),
		Phase: "probe_summary",
		Payload: mustJSON(map[string]any{
			"reasoning_effort_requested":       collector.reasoningEffortRequested,
			"reasoning_effort_request_support": collector.reasoningEffortSupport(),
			"reasoning_effort_fallback":        collector.reasoningFallback,
			"thinking_request_support":         collector.thinkingSupport(),
			"thinking_fallback":                collector.reasoningFallback,
			"thinking_wire_exposed":            probeWireYesNo(collector.hasThinking),
			"thinking_wire_chars":              collector.thinkingChars,
			"reasoning_tokens_observed":        collector.reasoningTokensObserved,
			"tool_choice_request_support":      collector.toolChoiceSupport(),
			"tool_choice_fallback":             collector.toolChoiceFallback,
			"thought_signature_replay":         collector.thoughtSignatureReplay(),
		}),
	})
}

func (o turnOpts) withToolChoiceFalse() turnOpts {
	o.withToolChoice = false
	return o
}

func geminiTurnWithProbeFallbacks(ctx context.Context, client *http.Client, spec ModelSpec, contents []geminiContent, stream StreamMode, collector *wireCollector, write func(rawRecord)) (turnResult, turnOpts, error) {
	opts := turnOpts{withToolChoice: true, withReasoning: true}
	for attempt := 0; attempt < 4; attempt++ {
		turn, err := geminiTurn(ctx, client, spec, contents, opts, stream, write)
		if err == nil {
			if opts.withReasoning && !collector.reasoningFallback {
				collector.reasoningAccepted = true
			}
			if opts.withToolChoice && !collector.toolChoiceFallback {
				collector.toolChoiceAccepted = true
			}
			return turn, opts, nil
		}
		if opts.withReasoning && isReasoningRejected(err) {
			collector.reasoningFallback = true
			opts.withReasoning = false
			write(rawRecord{
				At:    time.Now().UTC(),
				Phase: "reasoning_fallback",
				Payload: mustJSON(map[string]any{
					"reason": "upstream rejected thinkingConfig; retrying with generationConfig unset",
					"error":  err.Error(),
				}),
			})
			continue
		}
		if opts.withToolChoice && isToolChoiceRejected(err) {
			collector.toolChoiceFallback = true
			opts.withToolChoice = false
			write(rawRecord{
				At:    time.Now().UTC(),
				Phase: "tool_choice_fallback",
				Payload: mustJSON(map[string]any{
					"reason": "upstream rejected toolConfig; retrying with tools only",
					"error":  err.Error(),
				}),
			})
			continue
		}
		return turnResult{}, opts, err
	}
	return turnResult{}, opts, fmt.Errorf("turn 1 probe exceeded retry budget")
}

func geminiTurn(ctx context.Context, client *http.Client, spec ModelSpec, contents []geminiContent, opts turnOpts, stream StreamMode, write func(rawRecord)) (turnResult, error) {
	body, err := buildGeminiBody(spec, contents, opts)
	if err != nil {
		return turnResult{}, err
	}
	phase := "turn_request_tools_only"
	switch {
	case opts.withToolChoice && opts.withReasoning:
		phase = "turn_request_tool_config_auto_thinking"
	case opts.withToolChoice:
		phase = "turn_request_tool_config_auto"
	case opts.withReasoning:
		phase = "turn_request_tools_only_thinking"
	}
	write(rawRecord{At: time.Now().UTC(), Phase: phase, Payload: body})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, geminiGenerateURL(spec.Model, stream), bytes.NewReader(body))
	if err != nil {
		return turnResult{}, err
	}
	req.Header.Set("X-goog-api-key", geminiAPIKey())
	req.Header.Set("Content-Type", "application/json")
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	} else {
		req.Header.Set("Accept", "application/json")
	}

	resp, err := client.Do(req)
	if err != nil {
		return turnResult{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		rawBody, _ := io.ReadAll(resp.Body)
		write(rawRecord{
			At:    time.Now().UTC(),
			Phase: "http_error",
			Payload: mustJSON(map[string]any{
				"status": resp.StatusCode,
				"body":   strings.TrimSpace(string(rawBody)),
			}),
		})
		return turnResult{}, fmt.Errorf("upstream status %d: %s", resp.StatusCode, strings.TrimSpace(string(rawBody)))
	}

	if stream {
		return readGeminiSSE(ctx, resp.Body, write)
	}
	return readGeminiJSON(ctx, resp.Body, write)
}

func buildGeminiBody(spec ModelSpec, contents []geminiContent, opts turnOpts) (json.RawMessage, error) {
	payload := map[string]any{
		"contents": contents,
		"tools": []any{map[string]any{
			"functionDeclarations": []any{weatherFunctionDeclaration()},
		}},
	}
	if opts.withToolChoice {
		payload["toolConfig"] = map[string]any{
			"functionCallingConfig": map[string]any{"mode": "AUTO"},
		}
	}
	if opts.withReasoning {
		payload["generationConfig"] = map[string]any{
			"thinkingConfig": map[string]any{
				"thinkingLevel":   effectiveReasoningEffort(spec),
				"includeThoughts": true,
			},
		}
	}
	return json.Marshal(payload)
}

func weatherFunctionDeclaration() map[string]any {
	return map[string]any{
		"name":        weatherToolName,
		"description": "Get current weather for a city",
		"parameters": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"city": map[string]any{"type": "string"},
			},
			"required": []string{"city"},
		},
	}
}

func readGeminiSSE(ctx context.Context, r io.Reader, write func(rawRecord)) (turnResult, error) {
	var out turnResult
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for sc.Scan() {
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		default:
		}
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		if !bytes.HasPrefix(line, []byte("data: ")) {
			continue
		}
		data := bytes.TrimPrefix(line, []byte("data: "))
		if !json.Valid(data) {
			write(rawRecord{At: time.Now().UTC(), Phase: "sse_raw", Payload: mustJSON(string(data))})
			continue
		}
		write(rawRecord{At: time.Now().UTC(), Phase: "sse_data", Payload: json.RawMessage(data)})

		var chunk geminiResponse
		if err := json.Unmarshal(data, &chunk); err != nil {
			continue
		}
		if err := chunk.Error; err != nil && err.Message != "" {
			return out, fmt.Errorf("stream error: %s", err.Message)
		}
		mergeGeminiResponse(&out, chunk)
	}
	if err := sc.Err(); err != nil {
		return out, err
	}
	write(rawRecord{At: time.Now().UTC(), Phase: "sse_done", Payload: mustJSON("stream_end")})
	return out, nil
}

func readGeminiJSON(ctx context.Context, r io.Reader, write func(rawRecord)) (turnResult, error) {
	rawBody, err := io.ReadAll(r)
	if err != nil {
		return turnResult{}, err
	}
	select {
	case <-ctx.Done():
		return turnResult{}, ctx.Err()
	default:
	}
	write(rawRecord{At: time.Now().UTC(), Phase: "json_response", Payload: json.RawMessage(rawBody)})

	var resp geminiResponse
	if err := json.Unmarshal(rawBody, &resp); err != nil {
		return turnResult{}, fmt.Errorf("decode json response: %w", err)
	}
	if resp.Error != nil && resp.Error.Message != "" {
		return turnResult{}, fmt.Errorf("response error: %s", resp.Error.Message)
	}

	var out turnResult
	mergeGeminiResponse(&out, resp)
	return out, nil
}

func mergeGeminiResponse(out *turnResult, resp geminiResponse) {
	if resp.UsageMetadata.ThoughtsTokenCount > out.ReasoningTokens {
		out.ReasoningTokens = resp.UsageMetadata.ThoughtsTokenCount
	}
	for _, cand := range resp.Candidates {
		if cand.FinishReason != "" {
			out.FinishReason = cand.FinishReason
		}
		for _, part := range cand.Content.Parts {
			if part.Thought && strings.TrimSpace(part.Text) != "" {
				out.Thinking.WriteString(part.Text)
			}
			if !part.Thought && strings.TrimSpace(part.Text) != "" {
				out.Content.WriteString(part.Text)
			}
			if part.FunctionCall != nil && part.FunctionCall.Name != "" {
				args := strings.TrimSpace(string(part.FunctionCall.Args))
				if args == "" {
					args = "{}"
				}
				id := part.FunctionCall.ID
				if id == "" {
					id = part.FunctionCall.Name
				}
				out.ToolCalls = append(out.ToolCalls, ToolRecord{
					ID:        id,
					Name:      part.FunctionCall.Name,
					Arguments: args,
					Index:     len(out.ToolCalls),
				})
			}
			if isReplayableModelPart(part) {
				mergeModelPart(&out.ModelParts, part)
			}
		}
	}
}

func isReplayableModelPart(p geminiPart) bool {
	if p.FunctionCall != nil && p.FunctionCall.Name != "" {
		return true
	}
	if p.Thought && strings.TrimSpace(p.Text) != "" {
		return true
	}
	return !p.Thought && strings.TrimSpace(p.Text) != ""
}

func mergeModelPart(parts *[]geminiPart, p geminiPart) {
	if p.FunctionCall != nil && p.FunctionCall.Name != "" {
		for i, existing := range *parts {
			if existing.FunctionCall != nil && existing.FunctionCall.Name == p.FunctionCall.Name {
				merged := existing
				if p.FunctionCall.ID != "" {
					merged.FunctionCall.ID = p.FunctionCall.ID
				}
				if len(bytes.TrimSpace(p.FunctionCall.Args)) > 0 {
					merged.FunctionCall.Args = p.FunctionCall.Args
				}
				if p.ThoughtSignature != "" {
					merged.ThoughtSignature = p.ThoughtSignature
				}
				(*parts)[i] = merged
				return
			}
		}
		*parts = append(*parts, cloneModelPart(p))
		return
	}
	if p.Thought && strings.TrimSpace(p.Text) != "" {
		for i, existing := range *parts {
			if existing.Thought {
				(*parts)[i].Text = existing.Text + p.Text
				return
			}
		}
		*parts = append(*parts, cloneModelPart(p))
		return
	}
	if strings.TrimSpace(p.Text) != "" {
		*parts = append(*parts, cloneModelPart(p))
	}
}

func cloneModelPart(p geminiPart) geminiPart {
	out := p
	if p.FunctionCall != nil {
		fc := *p.FunctionCall
		out.FunctionCall = &fc
	}
	if p.FunctionResponse != nil {
		fr := *p.FunctionResponse
		out.FunctionResponse = &fr
	}
	return out
}

func isReasoningRejected(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "thinkingconfig") ||
		strings.Contains(msg, "thinking_config") ||
		strings.Contains(msg, "thinkinglevel") ||
		strings.Contains(msg, "includethoughts") ||
		strings.Contains(msg, "generationconfig") ||
		(strings.Contains(msg, "400") && strings.Contains(msg, "thinking"))
}

func isToolChoiceRejected(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "toolconfig") ||
		strings.Contains(msg, "functioncallingconfig") ||
		strings.Contains(msg, "function_calling") ||
		(strings.Contains(msg, "400") && strings.Contains(msg, "tools"))
}

func writeRawRecord(w io.Writer, rec rawRecord) error {
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
