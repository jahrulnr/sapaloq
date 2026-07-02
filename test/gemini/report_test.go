package gemini_test

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ToolRecord captures one completed tool call from the wire stream.
type ToolRecord struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name"`
	Arguments string `json:"arguments,omitempty"`
	Index     int    `json:"index"`
	Source    string `json:"source,omitempty"`
}

// CharacterReport is the derived summary from raw Gemini wire records.
type CharacterReport struct {
	Model                string       `json:"model"`
	Stream               bool         `json:"stream"`
	StreamMode           string       `json:"stream_mode"`
	Endpoint             string       `json:"endpoint"`
	ConfiguredParser     string       `json:"configured_parser"`
	ConfiguredAuthScheme string       `json:"configured_auth_scheme"`
	AutoDetectedParser   string       `json:"auto_detected_parser"`
	ReasoningEffort          string `json:"reasoning_effort,omitempty"`
	ReasoningEffortRequested string `json:"reasoning_effort_requested,omitempty"`
	ThinkingSupport          string `json:"thinking_request_support"`
	ThinkingWireExposed      string `json:"thinking_wire_exposed"`
	ThinkingFallback         bool   `json:"thinking_fallback"`
	ReasoningTokensObserved  int    `json:"reasoning_tokens_observed"`
	ReasoningEffortSupport   string `json:"reasoning_effort_request_support"`
	ReasoningEffortFallback  bool   `json:"reasoning_effort_fallback"`
	DurationMS           int64        `json:"duration_ms"`
	Timeline             []string     `json:"timeline"`
	ThinkingChars        int          `json:"thinking_chars"`
	HasThinking          bool         `json:"has_thinking"`
	ThinkingBeforeTool   bool         `json:"thinking_before_tool"`
	ResponseChars        int          `json:"response_chars"`
	ContentBeforeTool    bool         `json:"content_before_tool"`
	ToolBeforeFinalText  bool         `json:"tool_before_final_text"`
	ToolCalls            []ToolRecord `json:"tool_calls"`
	InferenceRounds      int          `json:"inference_rounds"`
	FinalText            string       `json:"final_text,omitempty"`
	ToolCalled           bool         `json:"tool_called"`
	ToolSucceeded        bool         `json:"tool_succeeded"`
	ToolChoiceFallback   bool         `json:"tool_choice_fallback"`
	// ToolChoiceSupport is yes when turn 1 accepted tool_choice:auto with tools,
	// no when upstream rejected tool_choice (tools-only retry may still work),
	// unknown when the probe failed before determining.
	ToolChoiceSupport       string       `json:"tool_choice_request_support"`
	ThoughtSignatureReplay  string       `json:"thought_signature_replay"`
	AnswerMentionsWeather bool        `json:"answer_mentions_weather"`
	Warnings             []string     `json:"warnings,omitempty"`
	Error                string       `json:"error,omitempty"`
}

type wireCollector struct {
	spec                 ModelSpec
	stream               StreamMode
	timeline             []string
	recordCount          int
	toolChoiceFallback     bool
	toolChoiceAccepted     bool
	reasoningFallback      bool
	reasoningAccepted      bool
	reasoningEffortRequested string
	reasoningTokensObserved  int
	toolCalled           bool
	toolSucceeded        bool
	toolCalls            []ToolRecord
	seenToolNames        map[string]struct{}
	thinkingChars        int
	hasThinking          bool
	thinkingBeforeTool   bool
	contentBeforeTool    bool
	toolBeforeFinal      bool
	responseChars        int
	finalText            string
	inferenceRounds      int
	errText              string
	sawFirstTool             bool
	postToolResponse         bool
	thoughtSignatureReplayed bool
}

func newWireCollector(spec ModelSpec, stream StreamMode) *wireCollector {
	return &wireCollector{
		spec:                     spec,
		stream:                   stream,
		seenToolNames:            map[string]struct{}{},
		reasoningEffortRequested: effectiveReasoningEffort(spec),
	}
}

func (c *wireCollector) ingestRaw(rec rawRecord) {
	c.recordCount++
	c.timeline = append(c.timeline, rec.Phase)
}

func (c *wireCollector) noteToolCalled(tc ToolRecord) {
	c.toolCalled = true
	if !c.sawFirstTool {
		c.sawFirstTool = true
	}
	if _, ok := c.seenToolNames[tc.Name]; !ok {
		c.seenToolNames[tc.Name] = struct{}{}
		c.toolCalls = append(c.toolCalls, tc)
	}
}

func (c *wireCollector) noteToolSucceeded() {
	c.toolSucceeded = true
	c.postToolResponse = true
	if c.inferenceRounds == 0 {
		c.inferenceRounds = 1
	}
}

func (c *wireCollector) noteModelReplay(parts []geminiPart) {
	for _, p := range parts {
		if p.FunctionCall != nil && strings.TrimSpace(p.ThoughtSignature) != "" {
			c.thoughtSignatureReplayed = true
			return
		}
	}
}

func (c *wireCollector) ingestTurn(turn turnResult, round int) {
	if turn.ReasoningTokens > c.reasoningTokensObserved {
		c.reasoningTokensObserved = turn.ReasoningTokens
	}
	if turn.Thinking.Len() > 0 {
		c.hasThinking = true
		c.thinkingChars += turn.Thinking.Len()
		if !c.sawFirstTool {
			c.thinkingBeforeTool = true
		}
	}
	if round == 1 {
		for _, tc := range turn.ToolCalls {
			if strings.TrimSpace(turn.Content.String()) != "" && !c.sawFirstTool {
				c.contentBeforeTool = true
			}
			c.noteToolCalled(tc)
		}
		if turn.Content.Len() > 0 && !c.sawFirstTool {
			c.contentBeforeTool = true
		}
	}
	if round == 2 {
		text := strings.TrimSpace(turn.Content.String())
		if text != "" {
			if c.sawFirstTool && !c.postToolResponse {
				c.toolBeforeFinal = true
			}
			c.finalText = text
			c.responseChars += turn.Content.Len()
		}
		c.inferenceRounds = 2
	}
}

func (c *wireCollector) report(elapsed time.Duration, errText string) CharacterReport {
	if errText != "" {
		c.errText = errText
	}
	final := strings.TrimSpace(c.finalText)
	answerOK := strings.Contains(strings.ToLower(final), "jakarta") || strings.Contains(final, "32")

	r := CharacterReport{
		Model:                 c.spec.Model,
		Stream:                bool(c.stream),
		StreamMode:            c.stream.String(),
		Endpoint:              geminiAPIBase(),
		ConfiguredParser:      c.spec.Parser,
		ConfiguredAuthScheme:  c.spec.AuthScheme,
		AutoDetectedParser:    sniffParser(c.spec.Model),
		ReasoningEffort:          c.spec.ReasoningEffort,
		ReasoningEffortRequested: c.reasoningEffortRequested,
		ReasoningEffortSupport:   c.reasoningEffortSupport(),
		ReasoningEffortFallback:  c.reasoningFallback,
		ThinkingSupport:          c.thinkingSupport(),
		ThinkingWireExposed:      probeWireYesNo(c.hasThinking),
		ThinkingFallback:         c.reasoningFallback,
		ReasoningTokensObserved:  c.reasoningTokensObserved,
		DurationMS:            elapsed.Milliseconds(),
		Timeline:              c.timeline,
		ThinkingChars:         c.thinkingChars,
		HasThinking:           c.hasThinking,
		ThinkingBeforeTool:    c.thinkingBeforeTool,
		ResponseChars:         c.responseChars,
		ContentBeforeTool:     c.contentBeforeTool,
		ToolBeforeFinalText:   c.toolBeforeFinal,
		ToolCalls:             c.toolCalls,
		InferenceRounds:       c.inferenceRounds,
		FinalText:             final,
		ToolCalled:            c.toolCalled,
		ToolSucceeded:         c.toolSucceeded,
		ToolChoiceFallback:    c.toolChoiceFallback,
		ToolChoiceSupport:     c.toolChoiceSupport(),
		ThoughtSignatureReplay: c.thoughtSignatureReplay(),
		AnswerMentionsWeather: answerOK,
		Error:                 c.errText,
	}
	if !r.HasThinking {
		r.Warnings = append(r.Warnings, "no reasoning_content/reasoning observed on the wire for this run")
	}
	if r.ReasoningEffortSupport != probeSupportYes {
		msg := reasoningEffortSupportNote(r.ReasoningEffortSupport, r.ReasoningEffortRequested)
		if r.ReasoningEffortFallback {
			msg += " (retried with reasoning_effort unset)"
		}
		r.Warnings = append(r.Warnings, msg)
	}
	if r.ThinkingWireExposed != probeSupportYes {
		r.Warnings = append(r.Warnings, thinkingWireNote(r.ThinkingWireExposed, r.ThinkingChars, r.ReasoningTokensObserved))
	}
	if r.ThinkingSupport != probeSupportYes {
		msg := thinkingRequestSupportNote(r.ThinkingSupport)
		if r.ThinkingFallback {
			msg += " (retried with thinking unset)"
		}
		r.Warnings = append(r.Warnings, msg)
	}
	if r.ToolChoiceSupport != probeSupportYes {
		msg := toolChoiceSupportNote(r.ToolChoiceSupport)
		if r.ToolChoiceFallback {
			msg += " (turn 1 retried with tools only)"
		}
		r.Warnings = append(r.Warnings, msg)
	}
	if c.spec.Parser != "" && r.AutoDetectedParser != c.spec.Parser {
		r.Warnings = append(r.Warnings,
			"sniffed parser ("+r.AutoDetectedParser+") differs from configured ("+c.spec.Parser+")")
	}
	return r
}

const (
	probeSupportYes     = "yes"
	probeSupportNo      = "no"
	probeSupportUnknown = "unknown"
)

func deriveProbeSupport(accepted, fallback bool) string {
	switch {
	case accepted:
		return probeSupportYes
	case fallback:
		return probeSupportNo
	default:
		return probeSupportUnknown
	}
}

func (c *wireCollector) toolChoiceSupport() string {
	return deriveProbeSupport(c.toolChoiceAccepted, c.toolChoiceFallback)
}

func (c *wireCollector) reasoningEffortSupport() string {
	return deriveProbeSupport(c.reasoningAccepted, c.reasoningFallback)
}

func (c *wireCollector) thinkingSupport() string {
	return c.reasoningEffortSupport()
}

func (c *wireCollector) thoughtSignatureReplay() string {
	if !c.toolCalled {
		return probeSupportUnknown
	}
	if c.thoughtSignatureReplayed {
		return probeSupportYes
	}
	return probeSupportNo
}

func toolChoiceSupportNote(support string) string {
	switch support {
	case probeSupportYes:
		return "upstream accepts tool_choice: auto with tools — safe to send tool_choice in provider-bridge"
	case probeSupportNo:
		return "upstream rejects tool_choice — omit tool_choice field; send tools only"
	default:
		return "tool_choice support not determined (probe failed before turn 1 completed)"
	}
}

func reasoningEffortSupportNote(support, requested string) string {
	switch support {
	case probeSupportYes:
		return "upstream accepts reasoning_effort=" + requested + " — safe to send in provider-bridge config"
	case probeSupportNo:
		return "upstream rejects reasoning_effort — leave reasoningEffort unset in provider entry"
	default:
		return "reasoning_effort support not determined (probe failed before turn 1 completed)"
	}
}

func probeWireYesNo(ok bool) string {
	if ok {
		return probeSupportYes
	}
	return probeSupportNo
}

func thinkingWireNote(exposed string, chars, reasoningTokens int) string {
	if exposed == probeSupportYes {
		return "thinking/reasoning visible on wire (~" + fmtInt(chars) + " chars; reasoning_tokens=" + fmtInt(reasoningTokens) + ")"
	}
	return "thinking/reasoning not exposed on wire (reasoning_content/reasoning empty; reasoning_tokens=" + fmtInt(reasoningTokens) + ")"
}

func fmtInt(n int) string {
	return strconv.Itoa(n)
}

func thinkingRequestSupportNote(support string) string {
	switch support {
	case probeSupportYes:
		return "upstream accepts thinking field in request (check thinking_wire_exposed for wire output)"
	case probeSupportNo:
		return "upstream rejects thinking field — omit thinking payload in provider-bridge"
	default:
		return "thinking support not determined (probe failed before turn 1 completed)"
	}
}

func countJSONLLines(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read raw stream %s: %v", path, err)
	}
	if len(b) == 0 {
		return 0
	}
	return strings.Count(string(b), "\n")
}

func logCharacterReport(t *testing.T, report CharacterReport) {
	t.Helper()
	b, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	t.Logf("characterization summary:\n%s", string(b))
}

func assertCharacterReport(t *testing.T, report CharacterReport) {
	t.Helper()
	if report.Error != "" {
		t.Fatalf("probe error: %s", report.Error)
	}
	if !report.ToolCalled {
		t.Fatalf("model never called %s (required for weather scenario)", weatherToolName)
	}
	if !report.ToolSucceeded {
		t.Fatal("tool round-trip did not complete")
	}
	if !report.AnswerMentionsWeather {
		t.Fatalf("final answer missing Jakarta/32: %q", report.FinalText)
	}
	for _, w := range report.Warnings {
		t.Logf("warning: %s", w)
	}
}

func TestDeriveProbeSupport(t *testing.T) {
	if got := deriveProbeSupport(true, false); got != probeSupportYes {
		t.Fatalf("accepted = %q", got)
	}
	if got := deriveProbeSupport(false, true); got != probeSupportNo {
		t.Fatalf("fallback = %q", got)
	}
	if got := deriveProbeSupport(false, false); got != probeSupportUnknown {
		t.Fatalf("unknown = %q", got)
	}
}

func TestIsReasoningRejected(t *testing.T) {
	err := fmtError(`upstream status 400: {"message":"thinkingConfig.thinkingLevel is not supported for this model"}`)
	if !isReasoningRejected(err) {
		t.Fatal("expected reasoning rejection")
	}
	if isReasoningRejected(fmtError("connection reset")) {
		t.Fatal("unexpected match")
	}
}

func TestIsToolChoiceRejected(t *testing.T) {
	err := fmtError(`upstream status 400: {"message":"toolConfig.functionCallingConfig is invalid"}`)
	if !isToolChoiceRejected(err) {
		t.Fatal("expected tool_choice rejection")
	}
	if isToolChoiceRejected(fmtError("connection reset")) {
		t.Fatal("unexpected match")
	}
}

func fmtError(msg string) error {
	return &probeError{msg: msg}
}

type probeError struct{ msg string }

func (e *probeError) Error() string { return e.msg }

func TestReadGeminiSSE(t *testing.T) {
	body := "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hi\"}],\"role\":\"model\"}}]}\n\n"
	var records []rawRecord
	turn, err := readGeminiSSE(context.Background(), strings.NewReader(body), func(rec rawRecord) {
		records = append(records, rec)
	})
	if err != nil {
		t.Fatalf("readGeminiSSE: %v", err)
	}
	if turn.Content.String() != "hi" {
		t.Fatalf("content = %q", turn.Content.String())
	}
	if len(records) < 1 {
		t.Fatalf("records = %d, want at least 1", len(records))
	}
}

func TestMergeGeminiResponsePreservesThoughtSignature(t *testing.T) {
	const sample = `{"candidates":[{"content":{"parts":[{"text":"reasoning","thought":true},{"functionCall":{"name":"get_weather","args":{"city":"Jakarta"},"id":"w6smjznv"},"thoughtSignature":"sig-abc"}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"thoughtsTokenCount":70}}`
	var resp geminiResponse
	if err := json.Unmarshal([]byte(sample), &resp); err != nil {
		t.Fatal(err)
	}
	var turn turnResult
	mergeGeminiResponse(&turn, resp)
	if len(turn.ModelParts) != 2 {
		t.Fatalf("ModelParts = %d, want 2", len(turn.ModelParts))
	}
	if turn.ModelParts[1].ThoughtSignature != "sig-abc" {
		t.Fatalf("thoughtSignature = %q", turn.ModelParts[1].ThoughtSignature)
	}
	if turn.ModelParts[1].FunctionCall == nil || turn.ModelParts[1].FunctionCall.ID != "w6smjznv" {
		t.Fatal("expected functionCall id on replay part")
	}
	if turn.ToolCalls[0].ID != "w6smjznv" {
		t.Fatalf("ToolRecord ID = %q", turn.ToolCalls[0].ID)
	}
}

func TestMergeGeminiStreamThoughtSignature(t *testing.T) {
	const chunk1 = `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"get_weather","args":{"city":"Jakarta"},"id":"yyu90aam"},"thoughtSignature":"sig-stream"}],"role":"model"}}],"usageMetadata":{"thoughtsTokenCount":69}}`
	const chunk2 = `{"candidates":[{"content":{"parts":[{"text":""}],"role":"model"},"finishReason":"STOP"}]}`
	var turn turnResult
	for _, raw := range []string{chunk1, chunk2} {
		var resp geminiResponse
		if err := json.Unmarshal([]byte(raw), &resp); err != nil {
			t.Fatal(err)
		}
		mergeGeminiResponse(&turn, resp)
	}
	if len(turn.ModelParts) != 1 {
		t.Fatalf("ModelParts = %d, want 1", len(turn.ModelParts))
	}
	if turn.ModelParts[0].ThoughtSignature != "sig-stream" {
		t.Fatalf("thoughtSignature = %q", turn.ModelParts[0].ThoughtSignature)
	}
}

func TestTranscriptDocExposesEmptyWireFields(t *testing.T) {
	doc := newTranscriptDoc(ModelSpec{Model: "test/model"}, StreamOff)
	doc.appendTurn1("hello", turnResult{})
	got := doc.b.String()
	if !strings.Contains(got, "thinking: (not on wire)") {
		t.Fatal("expected explicit empty thinking marker")
	}
	if !strings.Contains(got, "assistant: (not on wire)") {
		t.Fatal("expected explicit empty assistant marker")
	}
	if !strings.Contains(got, "tool: (none)") {
		t.Fatal("expected explicit empty tool marker")
	}
	if !strings.Contains(got, "user: hello") {
		t.Fatal("expected user line")
	}
}

func TestWireCollectorFinalTextSingleTurn(t *testing.T) {
	col := newWireCollector(ModelSpec{Model: "moonshotai/kimi-k2"}, StreamOn)
	col.noteToolCalled(ToolRecord{Name: weatherToolName, Arguments: `{"city":"Jakarta"}`, ID: "call_1"})
	col.noteToolSucceeded()
	var turn turnResult
	turn.Content.WriteString("Jakarta cuacanya 32°C, lembab, dan berawan sebagian.")
	col.ingestTurn(turn, 2)
	got := col.report(time.Second, "").FinalText
	want := "Jakarta cuacanya 32°C, lembab, dan berawan sebagian."
	if got != want {
		t.Fatalf("FinalText = %q, want %q", got, want)
	}
}
