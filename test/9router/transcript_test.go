package nrouter_test

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

const wireNotPresent = "(not on wire)"
const wireNone = "(none)"

type transcriptDoc struct {
	b strings.Builder
}

func newTranscriptDoc(spec ModelSpec, stream StreamMode) *transcriptDoc {
	d := &transcriptDoc{}
	fmt.Fprintf(&d.b, "# %s → %s (%s)\n\n", provider.DisplayName, spec.Model, stream)
	fmt.Fprintf(&d.b, "> characterize probe transcript — %s\n\n", time.Now().Format("2006-01-02 15:04:05 UTC"))
	d.expose("mode", stream.String())
	d.expose("stream", fmt.Sprintf("%t", bool(stream)))
	d.expose("reasoning_effort_requested", effectiveReasoningEffort(spec))
	d.expose("thinking_probe", fmt.Sprintf("type: %s", defaultThinkingProbeType))
	d.b.WriteString("\n")
	return d
}

func (d *transcriptDoc) section(title string) {
	d.b.WriteString("## ")
	d.b.WriteString(title)
	d.b.WriteString("\n\n")
}

func (d *transcriptDoc) expose(label, value string) {
	fmt.Fprintf(&d.b, "%s: %s\n\n", label, strings.TrimSpace(value))
}

func (d *transcriptDoc) exposeWireText(label, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = wireNotPresent
	}
	d.expose(label, value)
}

func (d *transcriptDoc) appendProbeContract(report CharacterReport) {
	d.section("Probe contract")
	d.expose("reasoning_effort_requested", report.ReasoningEffortRequested)
	d.expose("reasoning_effort_request_support", report.ReasoningEffortSupport)
	d.expose("reasoning_effort_request_hint", reasoningEffortSupportNote(report.ReasoningEffortSupport, report.ReasoningEffortRequested))
	d.expose("reasoning_effort_fallback", yesNoBool(report.ReasoningEffortFallback))
	d.expose("thinking_request_support", report.ThinkingSupport)
	d.expose("thinking_request_hint", thinkingRequestSupportNote(report.ThinkingSupport))
	d.expose("thinking_fallback", yesNoBool(report.ThinkingFallback))
	d.expose("thinking_wire_exposed", report.ThinkingWireExposed)
	d.expose("thinking_wire_chars", fmt.Sprintf("%d", report.ThinkingChars))
	d.expose("reasoning_tokens_observed", fmt.Sprintf("%d", report.ReasoningTokensObserved))
	d.expose("thinking_wire_hint", thinkingWireNote(report.ThinkingWireExposed, report.ThinkingChars, report.ReasoningTokensObserved))
	d.expose("tool_choice_request_support", report.ToolChoiceSupport)
	d.expose("tool_choice_request_hint", toolChoiceSupportNote(report.ToolChoiceSupport))
	d.expose("tool_choice_fallback", yesNoBool(report.ToolChoiceFallback))
	d.expose("tool_called", yesNoBool(report.ToolCalled))
	d.expose("tool_succeeded", yesNoBool(report.ToolSucceeded))
	d.expose("content_before_tool", yesNoBool(report.ContentBeforeTool))
	d.expose("thinking_before_tool", yesNoBool(report.ThinkingBeforeTool))
	d.expose("answer_mentions_weather", yesNoBool(report.AnswerMentionsWeather))
	if report.Error != "" {
		d.expose("error", report.Error)
	}
}

func yesNoBool(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func (d *transcriptDoc) appendTurn1(user string, turn turnResult) {
	d.section("Turn 1")
	d.expose("user", user)
	d.exposeWireText("thinking", turn.Thinking.String())
	d.exposeWireText("assistant", turn.Content.String())
	if len(turn.ToolCalls) == 0 {
		d.expose("tool", wireNone)
	} else {
		for _, tc := range turn.ToolCalls {
			d.expose("tool", formatToolCall(tc))
		}
	}
	if fr := strings.TrimSpace(turn.FinishReason); fr != "" {
		d.expose("finish_reason", fr)
	} else {
		d.expose("finish_reason", wireNone)
	}
	if turn.ReasoningTokens > 0 {
		d.expose("reasoning_tokens", fmt.Sprintf("%d", turn.ReasoningTokens))
	} else {
		d.expose("reasoning_tokens", "0")
	}
}

func (d *transcriptDoc) appendToolResult(name, content string) {
	d.section("Tool result")
	d.expose("tool", name)
	d.expose("tool_result", content)
}

func (d *transcriptDoc) appendTurn2(turn turnResult) {
	d.section("Turn 2")
	d.exposeWireText("thinking", turn.Thinking.String())
	d.exposeWireText("assistant", turn.Content.String())
	if len(turn.ToolCalls) == 0 {
		d.expose("tool", wireNone)
	} else {
		for _, tc := range turn.ToolCalls {
			d.expose("tool", formatToolCall(tc))
		}
	}
	if fr := strings.TrimSpace(turn.FinishReason); fr != "" {
		d.expose("finish_reason", fr)
	} else {
		d.expose("finish_reason", wireNone)
	}
	if turn.ReasoningTokens > 0 {
		d.expose("reasoning_tokens", fmt.Sprintf("%d", turn.ReasoningTokens))
	} else {
		d.expose("reasoning_tokens", "0")
	}
}

func (d *transcriptDoc) appendError(errText string) {
	if strings.TrimSpace(errText) == "" {
		return
	}
	d.section("Error")
	d.expose("error", errText)
}

func formatToolCall(tc ToolRecord) string {
	parts := []string{tc.Name}
	if tc.Arguments != "" {
		parts = append(parts, tc.Arguments)
	}
	out := strings.Join(parts, " ")
	if tc.ID != "" {
		out += " (id=" + tc.ID + ")"
	}
	return out
}

func writeTranscript(t *testing.T, path string, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write transcript %s: %v", path, err)
	}
	t.Logf("wrote transcript -> %s", path)
}
