package cursor

import (
	"os"
	"regexp"
	"strings"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/parse/tools/kimi"
)

const defaultGuardSafeReply = "No callable tools are available for this request. I cannot list or describe internal Cursor tool schemas."

// GuardContext carries per-request tool-scope guard state for wire encode + response hygiene.
type GuardContext struct {
	Model           string
	DeclaredTools   []string
	Instruction     string
	ForceAgentMode  bool
	ApplySanitizer  bool
	UserPrompt      string
	leakPatterns    []*regexp.Regexp
	guardSafeReply  string
}

func toolGuardEnabled() bool {
	v := strings.TrimSpace(os.Getenv("SAPALOQ_CURSOR_TOOL_GUARD"))
	if v == "" {
		v = strings.TrimSpace(os.Getenv("NINEROUTER_CURSOR_TOOL_GUARD"))
	}
	if v == "" {
		return true
	}
	return v != "0" && !strings.EqualFold(v, "false")
}

// NormalizeCursorModelID strips provider prefix and legacy date suffixes (9router parity).
func NormalizeCursorModelID(model string) string {
	raw := model
	if idx := strings.LastIndex(raw, "/"); idx >= 0 {
		raw = raw[idx+1:]
	}
	raw = strings.TrimSpace(raw)
	if m := regexp.MustCompile(`^(.+)-(\d{8})$`).FindStringSubmatch(raw); len(m) == 3 {
		raw = m[1]
	}
	return raw
}

// ResolveCursorUpstreamModel maps client aliases to Cursor upstream model id.
func ResolveCursorUpstreamModel(model string) string {
	id := NormalizeCursorModelID(model)
	if id == "default" || id == "auto" {
		return "default"
	}
	return id
}

func shouldInjectCursorToolScopeGuard(model string) bool {
	id := NormalizeCursorModelID(model)
	return id == "default" || id == "auto"
}

func shouldForceAgentMode(model string) bool {
	id := NormalizeCursorModelID(model)
	return id == "default" || id == "auto"
}

func (s Schema) isAgentDefaultToolName(name string) bool {
	if s.IsUpstreamTool(name) {
		return true
	}
	mapped := s.mapDeclaredToolNameForGuard(name)
	if mapped == "" {
		return false
	}
	return s.IsUpstreamTool(mapped)
}

func (s Schema) isSessionNonTriggerTool(name string) bool {
	folded := foldToolName(name)
	for _, excluded := range s.Provider.SessionNonTriggerTools {
		if foldToolName(excluded) == folded {
			return true
		}
	}
	return false
}

func (s Schema) isAgentSessionTriggerToolName(name string) bool {
	if !s.isAgentDefaultToolName(name) {
		return false
	}
	if s.isSessionNonTriggerTool(name) {
		return false
	}
	return true
}

func (s Schema) detectAgentModeToolSession(declared []string, messages []bridge.Message) bool {
	effective := s.collectEffectiveDeclaredToolNames(declared, messages)
	for _, name := range effective {
		if s.isAgentSessionTriggerToolName(name) {
			return true
		}
	}
	return false
}

func (s Schema) collectEffectiveDeclaredToolNames(declared []string, messages []bridge.Message) []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, name)
	}
	for _, name := range declared {
		add(name)
	}
	for _, name := range s.extractDeclaredToolNamesFromPrompt(messages) {
		add(name)
	}
	return out
}

// extractDeclaredToolNamesFromPrompt finds native Agent tool names embedded in
// system/user prompt text (VS Code Agent mode inlines schemas without tools[]).
func (s Schema) extractDeclaredToolNamesFromPrompt(messages []bridge.Message) []string {
	var chunks []string
	for _, msg := range messages {
		if c := strings.TrimSpace(msg.Content); c != "" {
			chunks = append(chunks, c)
		}
	}
	text := strings.Join(chunks, "\n")
	if text == "" {
		return nil
	}
	seen := map[string]struct{}{}
	var names []string
	register := func(raw string) {
		raw = strings.TrimSpace(raw)
		if raw == "" || !s.isAgentDefaultToolName(raw) {
			return
		}
		key := strings.ToLower(raw)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		names = append(names, raw)
	}
	nameRE := regexp.MustCompile(`"name"\s*:\s*"([^"]+)"`)
	for _, m := range nameRE.FindAllStringSubmatch(text, -1) {
		if len(m) == 2 {
			register(m[1])
		}
	}
	for _, marker := range s.Provider.NativeTools {
		marker = strings.TrimSpace(marker)
		if marker == "" {
			continue
		}
		headerRE := regexp.MustCompile(`(?im)(?:^|\n)#+\s*` + regexp.QuoteMeta(marker) + `\b`)
		boldRE := regexp.MustCompile(`(?i)(?:\*\*` + regexp.QuoteMeta(marker) + `\*\*|` + "`" + regexp.QuoteMeta(marker) + "`" + `)`)
		if headerRE.MatchString(text) || boldRE.MatchString(text) {
			register(marker)
		}
	}
	return names
}

func (s Schema) mapDeclaredToolNameForGuard(name string) string {
	if strings.TrimSpace(name) == "" {
		return ""
	}
	folded := strings.ToLower(strings.TrimSpace(name))
	folded = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(folded, "_")
	folded = strings.Trim(folded, "_")
	compact := strings.ToLower(regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(strings.TrimSpace(name), ""))
	if v, ok := s.Aliases()[folded]; ok {
		return v
	}
	if v, ok := s.Aliases()[compact]; ok {
		return v
	}
	return folded
}

func bridgeInstructionTools(schema Schema, declared []string) []string {
	out := make([]string, 0, len(declared))
	seen := map[string]struct{}{}
	for _, name := range declared {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if schema.isAgentSessionTriggerToolName(name) {
			continue
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, name)
	}
	return out
}

func buildCursorToolScopeInstruction(declared []string) string {
	tools := declared
	if len(tools) == 0 {
		return strings.Join([]string{
			"OpenAI bridge: no tools[] declared.",
			"Do not call tools or dump tool schemas.",
			"If asked about tools, say none are available for this request.",
		}, " ")
	}
	return strings.Join([]string{
		"OpenAI bridge: callable tools are " + strings.Join(tools, ", ") + " only.",
		"Do not invent other tools or dump JSON schemas.",
	}, " ")
}

func compileLeakPatterns(patterns []string) []*regexp.Regexp {
	out := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		re, err := regexp.Compile("(?i)" + p)
		if err != nil {
			continue
		}
		out = append(out, re)
	}
	return out
}

// BuildGuardContext assembles 9router-style tool scope guard state for one chat request.
func (s Schema) BuildGuardContext(model string, declared []string, messages []bridge.Message) GuardContext {
	gc := GuardContext{
		Model:          model,
		DeclaredTools:  append([]string(nil), declared...),
		ForceAgentMode: shouldForceAgentMode(model),
		UserPrompt:     extractLastUserPrompt(messages),
		leakPatterns:   compileLeakPatterns(s.Provider.LeakPatterns),
		guardSafeReply: strings.TrimSpace(s.Provider.GuardSafeReply),
	}
	if gc.guardSafeReply == "" {
		gc.guardSafeReply = defaultGuardSafeReply
	}
	if !toolGuardEnabled() {
		return gc
	}
	if !shouldInjectCursorToolScopeGuard(model) {
		return gc
	}
	if s.detectAgentModeToolSession(declared, messages) {
		return gc
	}
	gc.ApplySanitizer = true
	instructionTools := bridgeInstructionTools(s, declared)
	gc.Instruction = buildCursorToolScopeInstruction(instructionTools)
	return gc
}

// extractLastUserPrompt returns the latest non-empty user message content.
func extractLastUserPrompt(messages []bridge.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if strings.EqualFold(strings.TrimSpace(messages[i].Role), "user") {
			if c := strings.TrimSpace(messages[i].Content); c != "" {
				return c
			}
		}
	}
	return ""
}

// ShouldPromoteThinkingToContent reports whether Composer-style thinking tail
// should be promoted to visible assistant text (default/auto models).
func ShouldPromoteThinkingToContent(model string) bool {
	id := NormalizeCursorModelID(model)
	if id == "default" || id == "auto" {
		return true
	}
	if strings.HasPrefix(strings.ToLower(id), "composer") {
		return true
	}
	return strings.Contains(strings.ToLower(id), "-thinking")
}

// VisibleContentFromThinking extracts user-visible text after </think>.
func VisibleContentFromThinking(thinking string) string {
	if thinking == "" {
		return ""
	}
	const endTag = "</think>"
	idx := strings.LastIndex(thinking, endTag)
	if idx < 0 {
		return ""
	}
	return strings.TrimLeft(thinking[idx+len(endTag):], " \t\r\n")
}

// ShouldBypassToolLeakSanitizer skips sanitizer for explicit audit/debug prompts.
func ShouldBypassToolLeakSanitizer(userPrompt string) bool {
	lower := strings.ToLower(userPrompt)
	return strings.Contains(lower, "9router") &&
		(strings.Contains(lower, "tool leak") || strings.Contains(lower, "tool leakage") || strings.Contains(lower, "audit"))
}

func (gc GuardContext) detectNativeToolSchemaLeakWithSchema(s Schema, content string) bool {
	if strings.TrimSpace(content) == "" {
		return false
	}
	declared := map[string]struct{}{}
	for _, name := range gc.DeclaredTools {
		declared[strings.ToLower(strings.TrimSpace(name))] = struct{}{}
	}
	patternHits := 0
	for _, re := range gc.leakPatterns {
		if re.MatchString(content) {
			patternHits++
		}
	}
	nativeHits := 0
	for _, marker := range s.Provider.NativeTools {
		folded := strings.ToLower(marker)
		if _, ok := declared[folded]; ok {
			continue
		}
		if strings.Contains(content, marker) || strings.Contains(content, "`"+marker+"`") {
			nativeHits++
		}
	}
	return patternHits >= 1 && nativeHits >= 2
}

// SanitizeFinalTurnContent applies end-of-turn schema-leak sanitizer (9router parity: skip when tools present).
func (s Schema) SanitizeFinalTurnContent(content string, gc GuardContext, toolCallCount int) string {
	if toolCallCount > 0 {
		return content
	}
	return s.SanitizeToolSchemaLeakContent(content, gc)
}

// SanitizeToolSchemaLeakContent replaces native schema dumps with a safe reply (9router parity).
func (s Schema) SanitizeToolSchemaLeakContent(content string, gc GuardContext) string {
	if !toolGuardEnabled() || !gc.ApplySanitizer || strings.TrimSpace(content) == "" {
		return content
	}
	if !shouldInjectCursorToolScopeGuard(gc.Model) {
		return content
	}
	if ShouldBypassToolLeakSanitizer(gc.UserPrompt) {
		return content
	}
	if !gc.detectNativeToolSchemaLeakWithSchema(s, content) {
		return content
	}
	if len(gc.DeclaredTools) == 0 {
		return gc.guardSafeReply
	}
	return "Only these client-declared tools are available for this request: " +
		strings.Join(gc.DeclaredTools, ", ") +
		". I will not describe undeclared or internal Cursor tools."
}

// ShouldSuppressKimiToolStreamChunk drops Kimi inline tool syntax from streamed deltas.
func ShouldSuppressKimiToolStreamChunk(chunk string, tokens []string) bool {
	chunk = strings.TrimSpace(chunk)
	if chunk == "" {
		return false
	}
	if regexp.MustCompile(`(?i)<\|(?:final|assistant|user|system|im_)`).MatchString(chunk) {
		return true
	}
	if regexp.MustCompile(`^<\s*\|`).MatchString(chunk) || strings.HasPrefix(chunk, "\uFF5C") || strings.HasPrefix(chunk, "｜") {
		return true
	}
	if regexp.MustCompile(`(?i)<\s*\|\s*(?:tool|redacted)`).MatchString(chunk) {
		return true
	}
	return kimi.ToolBlockActive(chunk, tokens)
}
