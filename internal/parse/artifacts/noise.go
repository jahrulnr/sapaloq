package artifacts

import (
	"regexp"
	"strings"
)

// Model-response artifact markers Cursor/api2 sometimes confabulate into the
// visible channel on innocent chat turns (prior edit sessions, patch dumps).
var artifactHeaderPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?m)^#{1,3}\s*Final[\w/_.-]+\.(json|jsx?|tsx?|go|py|md|css|html?)\b`),
	regexp.MustCompile(`(?m)^#{1,3}\s*Final[\w/_.-]+`),
	regexp.MustCompile(`(?m)^#{1,3}\s*Final file content\s*:`),
	regexp.MustCompile(`(?m)^#{1,3}\s*File content\s*:`),
	regexp.MustCompile(`(?m)^\*\*\*\s*(?:Begin|Update)\s+(?:File|Patch)`),
	regexp.MustCompile(`(?m)^<<<<<<<`),
	regexp.MustCompile(`(?m)^\|\|\|\|\|\|\|\s*BEGIN PATCH`),
}

// IsModelResponseArtifact reports whether text looks like a confabulated edit
// artifact rather than a genuine assistant reply.
func IsModelResponseArtifact(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	for _, re := range artifactHeaderPatterns {
		if re.MatchString(text) {
			return true
		}
	}
	if strings.Contains(text, "### Final file content") || strings.Contains(text, "### File content:") {
		return true
	}
	if strings.Contains(text, "### Final_data/") || strings.Contains(text, "### Final_") {
		return true
	}
	// Single-line JSON scrape dumps (e.g. ### Final_data/cars/006687.json\n{…}).
	if strings.HasPrefix(text, "### Final") && strings.Contains(text, `"url":`) && strings.Contains(text, `"title":`) {
		return true
	}
	// Large unrelated source dump: long, many lines, multiple code-structure signals.
	if len(text) > 1500 {
		lower := strings.ToLower(text)
		if strings.Count(text, "\n") > 40 {
			signals := 0
			if strings.Contains(lower, "import ") || strings.Contains(lower, "from '") || strings.Contains(lower, "from \"") {
				signals++
			}
			if strings.Contains(lower, "export function") || strings.Contains(lower, "export default") || strings.Contains(lower, "module.exports") {
				signals++
			}
			if strings.Contains(text, "```") || strings.Contains(text, "function ") {
				signals++
			}
			if signals >= 2 {
				return true
			}
		}
		// Long single-line JSON / scrape payloads without code markers.
		if strings.Contains(text, `"url":`) && strings.Contains(text, `"title":`) && strings.Count(text, `"`) > 40 {
			return true
		}
	}
	return false
}

// StripModelResponseArtifact returns empty when text is artifact noise.
func StripModelResponseArtifact(text string) string {
	if IsModelResponseArtifact(text) {
		return ""
	}
	return text
}

// IsThinkingConfabulation reports cross-session thinking bleed: multiple unrelated
// "The user wants …" task narratives Cursor/api2 sometimes emits on chat turns.
func IsThinkingConfabulation(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	if strings.Count(text, "The user wants") >= 2 {
		return true
	}
	// Single block but clearly not about the current turn: long + external URLs/tasks.
	if len(text) > 400 && strings.Contains(text, "The user wants") {
		urls := strings.Count(text, "github.com/")
		if urls >= 1 && strings.Count(text, "Let me ") >= 2 {
			return true
		}
	}
	return false
}

// IsConversationalPing reports short casual chat openers (e.g. "heyy", "hi").
func IsConversationalPing(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" || len(text) > 80 {
		return false
	}
	if strings.Contains(text, "/") || strings.Contains(text, "```") || strings.Contains(text, ".go") {
		return false
	}
	return true
}

// FallbackAskGreeting is used when a conversational ping gets thinking-only noise
// and no visible reply from the provider.
func FallbackAskGreeting() string {
	return "Hey! How can I help?"
}

// FallbackAskNoiseRetry is used when the provider confabulates an unrelated artifact
// instead of answering the user's ask turn.
func FallbackAskNoiseRetry() string {
	return "The model returned unrelated noise from a prior session. Please try again."
}

// IsAutopilotEcho reports assistant text that merely repeats an internal
// autopilot/resume nudge (Cursor/api2 sometimes echoes "SapaLOQ received: …").
func IsAutopilotEcho(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	if strings.HasPrefix(text, "SapaLOQ received:") {
		return true
	}
	return strings.Contains(text, "<sapaloq:autopilot>")
}

// IsUnanchoredThinkingConfabulation reports cross-session thinking bleed: a task
// narrative that does not overlap the current task anchor.
func IsUnanchoredThinkingConfabulation(thinking, taskAnchor string) bool {
	if IsThinkingConfabulation(thinking) {
		return true
	}
	thinking = strings.TrimSpace(thinking)
	anchor := strings.ToLower(strings.TrimSpace(taskAnchor))
	if thinking == "" {
		return false
	}
	if hasCrossSessionTaskNarrative(thinking) {
		tokens := significantAnchorTokens(anchor)
		if len(tokens) == 0 {
			return true
		}
		return !thinkingOverlapsAnchor(thinking, anchor)
	}
	if anchor != "" && len(thinking) > 300 && !thinkingOverlapsAnchor(thinking, anchor) && crossSessionBleedSignals(thinking) {
		return true
	}
	return false
}

func hasCrossSessionTaskNarrative(thinking string) bool {
	for _, prefix := range []string{
		"The user wants",
		"The user is encountering",
		"The user asked",
		"The user needs",
		"The user has asked",
		"I'm in plan mode",
	} {
		if strings.Contains(thinking, prefix) {
			return true
		}
	}
	return false
}

func crossSessionBleedSignals(thinking string) bool {
	lower := strings.ToLower(thinking)
	signals := 0
	if strings.Contains(lower, "let me ") || strings.Contains(lower, "i need to explore") || strings.Contains(lower, "i'll search") {
		signals++
	}
	if strings.Contains(thinking, "```") || strings.Contains(lower, "error:") {
		signals++
	}
	if strings.Contains(thinking, ".terraform") || strings.Contains(thinking, "github.com/") {
		signals++
	}
	if strings.Contains(lower, "workspace") && (strings.Contains(lower, "search") || strings.Contains(lower, "explore")) {
		signals++
	}
	return signals >= 2
}

func thinkingOverlapsAnchor(thinking, anchorLower string) bool {
	lowerThinking := strings.ToLower(thinking)
	for _, word := range significantAnchorTokens(anchorLower) {
		if strings.Contains(lowerThinking, word) {
			return true
		}
	}
	return false
}

func significantAnchorTokens(anchor string) []string {
	stop := map[string]struct{}{
		"the": {}, "and": {}, "with": {}, "from": {}, "that": {}, "this": {},
		"into": {}, "each": {}, "must": {}, "have": {}, "will": {}, "your": {},
		"using": {}, "under": {}, "after": {}, "before": {}, "through": {},
	}
	var out []string
	seen := map[string]struct{}{}
	for _, raw := range strings.FieldsFunc(anchor, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' && r != '-'
	}) {
		if len(raw) < 5 {
			continue
		}
		if _, skip := stop[raw]; skip {
			continue
		}
		if _, dup := seen[raw]; dup {
			continue
		}
		seen[raw] = struct{}{}
		out = append(out, raw)
	}
	return out
}
