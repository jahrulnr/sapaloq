package nrouter_test

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

// rawRecord is one line in tmp/9router/*.jsonl — unmodified capture from the raw HTTP probe.
type rawRecord struct {
	At    time.Time       `json:"at"`
	Phase string          `json:"phase"`
	Payload json.RawMessage `json:"payload"`
}

type chatMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content,omitempty"`
	ToolCalls  []wireToolCall   `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	Name       string           `json:"name,omitempty"`
}

type wireToolCall struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Function wireToolFunction `json:"function"`
}

type wireToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type wireToolDelta struct {
	Index    int              `json:"index"`
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function wireToolFunction `json:"function"`
}

type wireDelta struct {
	Role             string          `json:"role,omitempty"`
	Content          string          `json:"content,omitempty"`
	ReasoningContent string          `json:"reasoning_content,omitempty"`
	Reasoning        json.RawMessage `json:"reasoning,omitempty"`
	ToolCalls        []wireToolDelta `json:"tool_calls,omitempty"`
}

type wireChoice struct {
	Index        int       `json:"index"`
	Delta        wireDelta `json:"delta"`
	FinishReason string    `json:"finish_reason,omitempty"`
}

type wireChunk struct {
	Choices []wireChoice `json:"choices"`
	Error   *wireAPIError `json:"error,omitempty"`
	Usage   *wireUsage    `json:"usage,omitempty"`
}

type wireUsage struct {
	CompletionTokensDetails *wireTokenDetails `json:"completion_tokens_details,omitempty"`
}

type wireTokenDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

type wireAPIError struct {
	Message string `json:"message"`
}

// StreamMode selects chat/completions wire format for one characterize run.
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

type wireMessage struct {
	Role             string          `json:"role,omitempty"`
	Content          string          `json:"content,omitempty"`
	ReasoningContent string          `json:"reasoning_content,omitempty"`
	Reasoning        json.RawMessage `json:"reasoning,omitempty"`
	ToolCalls        []wireToolCall  `json:"tool_calls,omitempty"`
}

type turnOpts struct {
	withToolChoice bool
	withReasoning  bool
}

type wireCompletionChoice struct {
	Index        int         `json:"index"`
	Message      wireMessage `json:"message"`
	FinishReason string      `json:"finish_reason,omitempty"`
}

type wireCompletion struct {
	Choices []wireCompletionChoice `json:"choices"`
	Error   *wireAPIError          `json:"error,omitempty"`
	Usage   *wireUsage             `json:"usage,omitempty"`
}

type turnResult struct {
	Thinking        strings.Builder
	Content         strings.Builder
	ToolCalls       []ToolRecord
	FinishReason    string
	ReasoningTokens int
	Records         []rawRecord
}

// runRawCharacterize performs a two-turn 9router chat/completions probe using
// only net/http. If the upstream rejects tool_choice, it retries with tools only.
// Writes raw JSONL plus a human-readable transcript (.md) beside it.
func runRawCharacterize(t *testing.T, spec ModelSpec, stream StreamMode) (rawPath string, report CharacterReport) {
	t.Helper()
	start := time.Now()
	path := nrouterRawPath(t, spec.Model, stream)
	transcriptPath := nrouterTranscriptPath(t, spec.Model, stream)
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
			"model":                    spec.Model,
			"stream":                   bool(stream),
			"mode":                     stream.String(),
			"reasoning_effort_default": defaultReasoningEffort,
			"reasoning_effort_requested": effectiveReasoningEffort(spec),
			"thinking_probe":           map[string]any{"type": defaultThinkingProbeType},
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
	messages := []chatMessage{{Role: "user", Content: weatherPrompt}}

	turn1, turn1Opts, err := chatTurnWithProbeFallbacks(ctx, client, spec, messages, stream, collector, write)
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

	messages = append(messages, chatMessage{
		Role:      "assistant",
		Content:   "",
		ToolCalls: []wireToolCall{{ID: tc.ID, Type: "function", Function: wireToolFunction{Name: tc.Name, Arguments: tc.Arguments}}},
	})
	messages = append(messages, chatMessage{
		Role:       "tool",
		ToolCallID: tc.ID,
		Name:       tc.Name,
		Content:    fakeWeatherToolResult,
	})
	collector.noteToolSucceeded()
	doc.appendToolResult(tc.Name, fakeWeatherToolResult)

	turn2, err := chatTurn(ctx, client, spec, messages, turn1Opts.withToolChoiceFalse(), stream, write)
	if err != nil {
		report = finish(collector.report(time.Since(start), err.Error()))
		return path, report
	}
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
			"reasoning_effort_requested":      collector.reasoningEffortRequested,
			"reasoning_effort_request_support": collector.reasoningEffortSupport(),
			"reasoning_effort_fallback":       collector.reasoningFallback,
			"thinking_request_support":        collector.thinkingSupport(),
			"thinking_fallback":               collector.reasoningFallback,
			"thinking_wire_exposed":           probeWireYesNo(collector.hasThinking),
			"thinking_wire_chars":             collector.thinkingChars,
			"reasoning_tokens_observed":       collector.reasoningTokensObserved,
			"tool_choice_request_support":     collector.toolChoiceSupport(),
			"tool_choice_fallback":            collector.toolChoiceFallback,
		}),
	})
}

func (o turnOpts) withToolChoiceFalse() turnOpts {
	o.withToolChoice = false
	return o
}

// chatTurnWithProbeFallbacks runs turn 1 with default reasoning_effort/thinking probe,
// falling back to unset reasoning fields and/or tools-only when upstream rejects them.
func chatTurnWithProbeFallbacks(ctx context.Context, client *http.Client, spec ModelSpec, messages []chatMessage, stream StreamMode, collector *wireCollector, write func(rawRecord)) (turnResult, turnOpts, error) {
	opts := turnOpts{withToolChoice: true, withReasoning: true}
	for attempt := 0; attempt < 4; attempt++ {
		turn, err := chatTurn(ctx, client, spec, messages, opts, stream, write)
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
					"reason": "upstream rejected reasoning_effort/thinking; retrying with both unset",
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
					"reason": "upstream rejected tool_choice; retrying with tools only",
					"error":  err.Error(),
				}),
			})
			continue
		}
		return turnResult{}, opts, err
	}
	return turnResult{}, opts, fmt.Errorf("turn 1 probe exceeded retry budget")
}

func chatTurn(ctx context.Context, client *http.Client, spec ModelSpec, messages []chatMessage, opts turnOpts, stream StreamMode, write func(rawRecord)) (turnResult, error) {
	body, err := buildChatBody(spec, messages, opts, stream)
	if err != nil {
		return turnResult{}, err
	}
	phase := "turn_request_tools_only"
	switch {
	case opts.withToolChoice && opts.withReasoning:
		phase = "turn_request_tool_choice_auto_reasoning"
	case opts.withToolChoice:
		phase = "turn_request_tool_choice_auto"
	case opts.withReasoning:
		phase = "turn_request_tools_only_reasoning"
	}
	write(rawRecord{At: time.Now().UTC(), Phase: phase, Payload: body})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, nrouterEndpoint(), bytes.NewReader(body))
	if err != nil {
		return turnResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+nrouterAPIKey())
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
		return readSSE(ctx, resp.Body, write)
	}
	return readJSONCompletion(ctx, resp.Body, write)
}

func buildChatBody(spec ModelSpec, messages []chatMessage, opts turnOpts, stream StreamMode) (json.RawMessage, error) {
	payload := map[string]any{
		"model":    spec.Model,
		"stream":   bool(stream),
		"messages": messages,
		"tools":    []any{weatherToolSchema()},
	}
	if opts.withToolChoice {
		payload["tool_choice"] = "auto"
	}
	if opts.withReasoning {
		payload["reasoning_effort"] = effectiveReasoningEffort(spec)
		payload["thinking"] = map[string]any{"type": defaultThinkingProbeType}
	}
	return json.Marshal(payload)
}

func weatherToolSchema() map[string]any {
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        weatherToolName,
			"description": "Get current weather for a city",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"city": map[string]any{"type": "string"},
				},
				"required": []string{"city"},
			},
		},
	}
}

func readSSE(ctx context.Context, r io.Reader, write func(rawRecord)) (turnResult, error) {
	var out turnResult
	acc := newToolAccumulator()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for sc.Scan() {
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		default:
		}
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		if !bytes.HasPrefix(line, []byte("data: ")) {
			continue
		}
		data := bytes.TrimPrefix(line, []byte("data: "))
		if bytes.Equal(data, []byte("[DONE]")) {
			write(rawRecord{At: time.Now().UTC(), Phase: "sse_done", Payload: mustJSON("[DONE]")})
			break
		}
		if !json.Valid(data) {
			write(rawRecord{
				At:      time.Now().UTC(),
				Phase:   "sse_raw",
				Payload: mustJSON(string(data)),
			})
			continue
		}
		write(rawRecord{At: time.Now().UTC(), Phase: "sse_data", Payload: json.RawMessage(data)})

		var chunk wireChunk
		if err := json.Unmarshal(data, &chunk); err != nil {
			continue
		}
		if chunk.Error != nil && chunk.Error.Message != "" {
			return out, fmt.Errorf("stream error: %s", chunk.Error.Message)
		}
		out.ReasoningTokens = maxReasoningTokens(out.ReasoningTokens, chunk.Usage)
		for _, choice := range chunk.Choices {
			d := choice.Delta
			if d.ReasoningContent != "" {
				out.Thinking.WriteString(d.ReasoningContent)
			}
			appendReasoningWire(&out.Thinking, d.Reasoning)
			if d.Content != "" {
				out.Content.WriteString(d.Content)
			}
			for _, td := range d.ToolCalls {
				acc.apply(td)
			}
			if choice.FinishReason != "" {
				out.FinishReason = choice.FinishReason
			}
		}
	}
	if err := sc.Err(); err != nil {
		return out, err
	}
	out.ToolCalls = acc.finish()
	return out, nil
}

func readJSONCompletion(ctx context.Context, r io.Reader, write func(rawRecord)) (turnResult, error) {
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

	var completion wireCompletion
	if err := json.Unmarshal(rawBody, &completion); err != nil {
		return turnResult{}, fmt.Errorf("decode json response: %w", err)
	}
	if completion.Error != nil && completion.Error.Message != "" {
		return turnResult{}, fmt.Errorf("response error: %s", completion.Error.Message)
	}

	var out turnResult
	out.ReasoningTokens = maxReasoningTokens(0, completion.Usage)
	for _, choice := range completion.Choices {
		msg := choice.Message
		if msg.ReasoningContent != "" {
			out.Thinking.WriteString(msg.ReasoningContent)
		}
		appendReasoningWire(&out.Thinking, msg.Reasoning)
		if msg.Content != "" {
			out.Content.WriteString(msg.Content)
		}
		for i, tc := range msg.ToolCalls {
			out.ToolCalls = append(out.ToolCalls, ToolRecord{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
				Index:     i,
			})
		}
		if choice.FinishReason != "" {
			out.FinishReason = choice.FinishReason
		}
	}
	return out, nil
}

type toolAccumulator struct {
	calls map[int]*ToolRecord
	order []int
}

func newToolAccumulator() *toolAccumulator {
	return &toolAccumulator{calls: map[int]*ToolRecord{}}
}

func (a *toolAccumulator) apply(td wireToolDelta) {
	rec, ok := a.calls[td.Index]
	if !ok {
		rec = &ToolRecord{Index: td.Index}
		a.calls[td.Index] = rec
		a.order = append(a.order, td.Index)
	}
	if td.ID != "" {
		rec.ID = td.ID
	}
	if td.Function.Name != "" {
		rec.Name = td.Function.Name
	}
	if td.Function.Arguments != "" {
		rec.Arguments += td.Function.Arguments
	}
}

func (a *toolAccumulator) finish() []ToolRecord {
	out := make([]ToolRecord, 0, len(a.order))
	for _, idx := range a.order {
		if rec, ok := a.calls[idx]; ok && rec.Name != "" {
			out = append(out, *rec)
		}
	}
	return out
}

func maxReasoningTokens(current int, usage *wireUsage) int {
	if usage == nil || usage.CompletionTokensDetails == nil {
		return current
	}
	if usage.CompletionTokensDetails.ReasoningTokens > current {
		return usage.CompletionTokensDetails.ReasoningTokens
	}
	return current
}

func isReasoningRejected(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "reasoning_effort") ||
		strings.Contains(msg, "reasoning effort") ||
		strings.Contains(msg, "thinking") ||
		(strings.Contains(msg, "400") && strings.Contains(msg, "reasoning"))
}

func appendReasoningWire(b *strings.Builder, raw json.RawMessage) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if strings.TrimSpace(s) != "" {
			b.WriteString(s)
		}
		return
	}
	b.Write(raw)
}

func isToolChoiceRejected(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "tool_choice") ||
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
