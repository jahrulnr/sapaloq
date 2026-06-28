package cursor

import (
	"regexp"
	"strings"

	"github.com/jahrulnr/sapaloq/internal/parse/tools/kimi"
)

var (
	kimiControlTokenRE    = regexp.MustCompile(`(?i)<\|(?:final(?:_answer)?|assistant|user|system|im_start|im_end|eot|begin(?:_of(?:_sentence)?)?|end(?:_of(?:_sentence)?)?|redacted_[a-z0-9_]+)\|>`)
	kimiOrphanTokenTailRE = regexp.MustCompile(`(?m)^[a-z]{1,24}[|｜]>\s*`)
	kimiOrphanPipeGtRE    = regexp.MustCompile(`(?m)^[|｜]>\s*`)
)

// stripKimiControlTokenArtifacts removes Kimi control tokens and split-stream orphan tails.
func stripKimiControlTokenArtifacts(text string) string {
	if text == "" {
		return text
	}
	cleaned := text
	cleaned = kimiControlTokenRE.ReplaceAllString(cleaned, "")
	cleaned = kimiOrphanTokenTailRE.ReplaceAllString(cleaned, "")
	cleaned = kimiOrphanPipeGtRE.ReplaceAllString(cleaned, "")
	return cleaned
}

// CleanKimiAssistantContent strips Kimi control tokens and inline tool syntax from visible text.
func CleanKimiAssistantContent(content string, kimiTokens []string) string {
	if strings.TrimSpace(content) == "" {
		return content
	}
	cleaned := stripKimiControlTokenArtifacts(content)
	if kimi.ToolBlockActive(cleaned, kimiTokens) {
		cleaned = kimi.ExtractWithTokens(cleaned, kimiTokens).CleanedText
	}
	return strings.TrimSpace(cleaned)
}

// FinalizeAssistantContentWithToolCalls drops short pre-tool narration when tools are present.
func FinalizeAssistantContentWithToolCalls(content string, toolCallCount int) string {
	if toolCallCount == 0 {
		return strings.TrimSpace(content)
	}
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return ""
	}
	if len(trimmed) > 150 || strings.Contains(trimmed, "\n\n") {
		return trimmed
	}
	return ""
}

// shouldStreamCursorContentDelta reports whether a text delta should contribute to visible content during accumulation.
func shouldStreamCursorContentDelta(delta, total string, kimiBlockStarted, toolCallActive bool, kimiTokens []string) bool {
	if strings.TrimSpace(delta) == "" {
		return false
	}
	if toolCallActive {
		return false
	}
	if ShouldSuppressKimiToolStreamChunk(delta, kimiTokens) {
		return false
	}
	combined := total + delta
	if kimiBlockStarted || kimi.ToolBlockActive(combined, kimiTokens) {
		return false
	}
	return true
}
