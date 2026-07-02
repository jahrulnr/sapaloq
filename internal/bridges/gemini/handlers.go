package gemini

import (
	"bytes"
	"strings"

	"github.com/jahrulnr/sapaloq/internal/parse"
)

func mergeResponse(out *turnAccum, resp response) {
	if resp.UsageMetadata.ThoughtsTokenCount > out.reasoningTokens {
		out.reasoningTokens = resp.UsageMetadata.ThoughtsTokenCount
	}
	for _, cand := range resp.Candidates {
		if cand.FinishReason != "" {
			out.finishReason = cand.FinishReason
		}
		for _, p := range cand.Content.Parts {
			if p.Thought && strings.TrimSpace(p.Text) != "" {
				out.thinking += p.Text
			}
			if !p.Thought && strings.TrimSpace(p.Text) != "" {
				out.content += p.Text
			}
			if p.FunctionCall != nil && p.FunctionCall.Name != "" {
				args := strings.TrimSpace(string(p.FunctionCall.Args))
				if args == "" {
					args = "{}"
				}
				id := p.FunctionCall.ID
				if id == "" {
					id = p.FunctionCall.Name
				}
				out.toolCalls = append(out.toolCalls, toolCallRecord{
					id: id, name: p.FunctionCall.Name, arguments: args,
				})
			}
			if isReplayableModelPart(p) {
				mergeModelPart(&out.modelParts, p)
			}
		}
	}
}

func isReplayableModelPart(p part) bool {
	if p.FunctionCall != nil && p.FunctionCall.Name != "" {
		return true
	}
	if p.Thought && strings.TrimSpace(p.Text) != "" {
		return true
	}
	return !p.Thought && strings.TrimSpace(p.Text) != ""
}

func mergeModelPart(parts *[]part, p part) {
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
		*parts = append(*parts, clonePart(p))
		return
	}
	if p.Thought && strings.TrimSpace(p.Text) != "" {
		for i, existing := range *parts {
			if existing.Thought {
				(*parts)[i].Text = existing.Text + p.Text
				return
			}
		}
		*parts = append(*parts, clonePart(p))
		return
	}
	if strings.TrimSpace(p.Text) != "" {
		*parts = append(*parts, clonePart(p))
	}
}

func clonePart(p part) part {
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

func toParseToolCall(rec toolCallRecord) parse.ToolCall {
	return parse.ToolCall{
		ID:        rec.id,
		Name:      rec.name,
		Arguments: []byte(rec.arguments),
		Source:    driverID,
	}
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
