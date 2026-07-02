package gemini_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func providerDocSlug(spec ModelSpec, stream StreamMode) string {
	return provider.providerDocSlug(spec, stream)
}

func providerDocPath(t *testing.T, spec ModelSpec, stream StreamMode) string {
	t.Helper()
	return filepath.Join(repoRoot(t), "docs", "providers", providerDocSlug(spec, stream)+".md")
}

// writeProviderCharacterizationDoc materializes docs/providers/gemini-<model>-<mode>.md
// from one live characterize run (report + raw jsonl path).
func writeProviderCharacterizationDoc(t *testing.T, spec ModelSpec, stream StreamMode, report CharacterReport, rawPath string, eventCount int) {
	t.Helper()
	dir := filepath.Join(repoRoot(t), "docs", "providers")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir docs/providers: %v", err)
	}
	path := filepath.Join(dir, providerDocSlug(spec, stream)+".md")
	body := renderProviderDoc(spec, stream, report, rawPath, eventCount)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write provider doc %s: %v", path, err)
	}
	t.Logf("wrote provider doc -> %s", path)
}

func renderProviderDoc(spec ModelSpec, stream StreamMode, report CharacterReport, rawPath string, eventCount int) string {
	parser := spec.Parser
	if parser == "" {
		parser = report.AutoDetectedParser + " (auto; set explicitly for " + provider.DisplayName + ")"
	}
	auth := spec.AuthScheme
	if auth == "" {
		auth = "x-goog-api-key (default)"
	}
	reasoning := spec.ReasoningEffort
	if reasoning == "" {
		reasoning = report.ReasoningEffortRequested + " (probe default)"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# %s → %s (%s)\n\n", provider.DisplayName, spec.Model, stream)
	fmt.Fprintf(&b, "> Last updated: %s (characterize suite)\n\n", time.Now().Format("2006-01-02"))
	fmt.Fprintf(&b, "Live characterization via `%s` — raw `net/http` POST to Google **generateContent** / **streamGenerateContent** (no SapaLOQ orchestrator). Mode: **`%s`** (`stream: %t`). Weather scenario: `get_weather` fake tool round-trip. Raw capture: `%s` (%d records). Transcript: `%s`.\n\n", provider.TestDir, stream, bool(stream), rawPath, eventCount, strings.TrimSuffix(rawPath, ".jsonl")+".md")

	b.WriteString("## Route\n\n")
	b.WriteString("| Field | Value |\n|-------|-------|\n")
	fmt.Fprintf(&b, "| Gateway | %s (`%s/models/<model>:generateContent`) |\n", provider.DisplayName, provider.DefaultEndpoint)
	fmt.Fprintf(&b, "| Model slug | `%s` |\n", spec.Model)
	fmt.Fprintf(&b, "| Wire mode | `%s` (`stream: %t`) |\n", stream, bool(stream))
	fmt.Fprintf(&b, "| SapaLOQ parser hint (configured) | `%s` |\n", parser)
	fmt.Fprintf(&b, "| Sniffed parser (model name) | `%s` |\n", report.AutoDetectedParser)
	fmt.Fprintf(&b, "| Auth | `%s` |\n", auth)
	fmt.Fprintf(&b, "| Reasoning effort | `%s` |\n", reasoning)
	fmt.Fprintf(&b, "| Duration | %d ms |\n\n", report.DurationMS)

	b.WriteString("## Recommended entry\n\n")
	b.WriteString("```json\n")
	fmt.Fprintf(&b, "{\n  \"key\": \"%s\",\n", provider.configEntryKey(spec))
	b.WriteString("  \"driver\": \"provider-bridge\",\n")
	fmt.Fprintf(&b, "  \"endpoint\": \"%s\",\n", provider.recommendedEndpoint(spec.Model))
	fmt.Fprintf(&b, "  \"model\": \"%s\",\n", spec.Model)
	fmt.Fprintf(&b, "  \"credentialsEnv\": \"%s\",\n", provider.CredentialsEnvDefault)
	if spec.Parser != "" {
		fmt.Fprintf(&b, "  \"parser\": \"%s\",\n", spec.Parser)
	} else {
		b.WriteString("  \"parser\": \"gemini\",\n")
	}
	if spec.AuthScheme != "" {
		fmt.Fprintf(&b, "  \"authScheme\": \"%s\",\n", spec.AuthScheme)
	} else {
		b.WriteString("  \"authScheme\": \"x-goog-api-key\",\n")
	}
	if spec.ReasoningEffort != "" {
		fmt.Fprintf(&b, "  \"reasoningEffort\": \"%s\",\n", spec.ReasoningEffort)
	} else if report.ReasoningEffortSupport == probeSupportYes {
		fmt.Fprintf(&b, "  \"reasoningEffort\": \"%s\",\n", report.ReasoningEffortRequested)
	}
	b.WriteString("  \"requestTimeoutSec\": 600\n")
	b.WriteString("}\n```\n\n")
	fmt.Fprintf(&b, "%s uses the Google Generative Language API (`generateContent`). Auth header **`X-goog-api-key`**. Thinking is probed via `generationConfig.thinkingConfig` (`thinkingLevel` + `includeThoughts`); wire may expose `thoughtSignature` / `thoughtsTokenCount` without visible thought text (see `thinking_wire_exposed`). Tools use `functionDeclarations` + optional `toolConfig.functionCallingConfig.mode: AUTO`.\n\n", provider.DisplayName)

	b.WriteString("## Observed behavior\n\n")
	b.WriteString("| Capability | Result |\n|------------|--------|\n")
	b.WriteString(fmt.Sprintf("| Thinking wire exposed | `%s` (%d chars; reasoning_tokens=%d) |\n", report.ThinkingWireExposed, report.ThinkingChars, report.ReasoningTokensObserved))
	fmt.Fprintf(&b, "| reasoning_effort request support (`%s`) | `%s` |\n", report.ReasoningEffortRequested, report.ReasoningEffortSupport)
	if report.ReasoningEffortSupport != probeSupportYes {
		fmt.Fprintf(&b, "| reasoning_effort implementation | %s |\n", mdCell(reasoningEffortSupportNote(report.ReasoningEffortSupport, report.ReasoningEffortRequested)))
	}
	if report.ReasoningEffortFallback {
		b.WriteString("| reasoning_effort fallback | yes (retried unset) |\n")
	}
	fmt.Fprintf(&b, "| thinking request support | `%s` |\n", report.ThinkingSupport)
	if report.ThinkingWireExposed != probeSupportYes {
		fmt.Fprintf(&b, "| thinking wire note | %s |\n", mdCell(thinkingWireNote(report.ThinkingWireExposed, report.ThinkingChars, report.ReasoningTokensObserved)))
	}
	if report.ThinkingSupport != probeSupportYes {
		fmt.Fprintf(&b, "| thinking request note | %s |\n", mdCell(thinkingRequestSupportNote(report.ThinkingSupport)))
	}
	if report.ThinkingFallback {
		b.WriteString("| thinking fallback | yes (retried unset) |\n")
	}
	b.WriteString(fmt.Sprintf("| Tool round-trip (`get_weather`) | %s |\n", toolVerdict(report)))
	fmt.Fprintf(&b, "| tool_choice request support | `%s` |\n", report.ToolChoiceSupport)
	if report.ToolChoiceSupport != probeSupportYes {
		fmt.Fprintf(&b, "| tool_choice implementation | %s |\n", mdCell(toolChoiceSupportNote(report.ToolChoiceSupport)))
	}
	if report.ToolChoiceFallback {
		b.WriteString("| tool_choice fallback | yes (retried with tools only) |\n")
	}
	b.WriteString(fmt.Sprintf("| Final assistant text | %s |\n", mdCell(report.FinalText)))
	if len(report.ToolCalls) > 0 {
		names := make([]string, 0, len(report.ToolCalls))
		for _, tc := range report.ToolCalls {
			names = append(names, tc.Name)
		}
		fmt.Fprintf(&b, "| Tool calls (order) | `%s` |\n", strings.Join(names, "` → `"))
	}
	if report.ContentBeforeTool {
		b.WriteString("| Content before first tool | yes |\n")
	}
	if report.ThinkingBeforeTool {
		b.WriteString("| Thinking before first tool | yes |\n")
	}
	if report.ToolBeforeFinalText {
		b.WriteString("| Tool after assistant text started | yes |\n")
	}
	b.WriteString("\n")

	if len(report.Timeline) > 0 {
		b.WriteString("### Event timeline (non-transcript kinds)\n\n")
		b.WriteString("`" + strings.Join(report.Timeline, "` → `") + "`\n\n")
	}

	if report.Error != "" {
		b.WriteString("### Upstream / stream error\n\n")
		b.WriteString("```text\n")
		b.WriteString(report.Error)
		b.WriteString("\n```\n\n")
	}

	if len(report.Warnings) > 0 {
		b.WriteString("### Notes\n\n")
		for _, w := range report.Warnings {
			fmt.Fprintf(&b, "- %s\n", w)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Verdict\n\n")
	b.WriteString(verdictParagraph(spec, report))
	b.WriteString("\n\n")

	b.WriteString("## Reproduce\n\n")
	b.WriteString("```bash\n")
	fmt.Fprintf(&b, "export %s=1\n", provider.GateEnv)
	fmt.Fprintf(&b, "export %s=...\n", provider.credEnvVar())
	fmt.Fprintf(&b, "export %s='%s|openai|bearer|'\n", provider.ModelsEnv, spec.Model)
	fmt.Fprintf(&b, "make %s\n", provider.MakeTarget)
	b.WriteString("```\n")

	return b.String()
}

func yesNo(ok bool, chars int) string {
	if !ok {
		return "no"
	}
	if chars > 0 {
		return fmt.Sprintf("yes (~%d chars)", chars)
	}
	return "yes"
}

func toolVerdict(r CharacterReport) string {
	if r.Error != "" && !r.ToolSucceeded {
		return "failed — " + truncate(r.Error, 120)
	}
	if r.ToolCalled && r.ToolSucceeded {
		return "ok"
	}
	if r.ToolCalled {
		return "called but did not complete"
	}
	return "no tool call"
}

func verdictParagraph(_ ModelSpec, r CharacterReport) string {
	if r.Error != "" && !r.ToolSucceeded {
		if strings.Contains(r.Error, "tool_choice") || strings.Contains(r.Error, "toolConfig") {
			return "**toolConfig rejected and tools-only retry still failed** — see error. Characterize notes capture upstream limits for this slug."
		}
		if strings.Contains(r.Error, "tools") || strings.Contains(r.Error, "functionDeclarations") {
			return fmt.Sprintf("**Tools not usable** on this %s route — upstream rejected tool payloads (see error).", provider.DisplayName)
		}
		return "**Characterization failed** — see upstream error."
	}
	if r.ToolSucceeded && r.AnswerMentionsWeather {
		parts := []string{fmt.Sprintf("**Tool loop works** on %s (get_weather → fake result → assistant reply).", provider.DisplayName)}
		switch r.ReasoningEffortSupport {
		case probeSupportYes:
			parts = append(parts, "`thinkingLevel: "+r.ReasoningEffortRequested+"` accepted on this route.")
		case probeSupportNo:
			parts = append(parts, "**Leave `reasoningEffort` / thinkingConfig unset** on this route.")
		}
		switch r.ThinkingSupport {
		case probeSupportYes:
			parts = append(parts, "`generationConfig.thinkingConfig` probe accepted.")
		case probeSupportNo:
			parts = append(parts, "**Omit `thinkingConfig`** on this route.")
		}
		switch r.ToolChoiceSupport {
		case probeSupportYes:
			parts = append(parts, "`toolConfig.functionCallingConfig.mode: AUTO` accepted on this route.")
		case probeSupportNo:
			parts = append(parts, "**Omit `toolConfig`** on this route (tools / functionDeclarations only).")
		}
		if r.ToolChoiceFallback {
			parts = append(parts, "Probe used tools-only fallback after upstream rejected tool_choice.")
		}
		if !r.HasThinking {
			parts = append(parts, "Thinking/reasoning was not visible on the wire for this run.")
		}
		return strings.Join(parts, " ")
	}
	return "**Partial / inconclusive** — re-run characterize suite and refresh this doc."
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func mdCell(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "(empty)"
	}
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}
