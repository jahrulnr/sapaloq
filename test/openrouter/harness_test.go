package openrouter_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	openRouterCredEnv        = "OPENROUTER_API_KEY"
	defaultOpenRouterEP      = "https://openrouter.ai/api/v1/chat/completions"
	weatherToolName          = "get_weather"
	defaultReasoningEffort   = "low"
	defaultThinkingProbeType = "enabled"
)

// effectiveReasoningEffort returns the configured OPENROUTER_MODELS effort or default "low".
func effectiveReasoningEffort(spec ModelSpec) string {
	if strings.TrimSpace(spec.ReasoningEffort) != "" {
		return strings.TrimSpace(spec.ReasoningEffort)
	}
	return defaultReasoningEffort
}

// weatherPrompt forces a tool round-trip through the fake get_weather tool.
const weatherPrompt = "Test harness. User asks Jakarta weather. You MUST call get_weather with city Jakarta before answering. Reason step-by-step. After the tool result, reply in one short sentence citing the temperature."

const fakeWeatherToolResult = `{"city":"Jakarta","temperature_c":32,"conditions":"humid, partly cloudy"}`

// ModelSpec is one OpenRouter model entry from OPENROUTER_MODELS.
type ModelSpec struct {
	Model           string
	Parser          string
	AuthScheme      string
	ReasoningEffort string
}

func openRouterEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("SAPALOQ_OPENROUTER_E2E")))
	return v == "1" || v == "true" || v == "yes"
}

func openRouterAPIKey() string {
	return strings.TrimSpace(os.Getenv(openRouterCredEnv))
}

func openRouterEndpoint() string {
	ep := strings.TrimSpace(os.Getenv("OPENROUTER_ENDPOINT"))
	if ep == "" {
		return defaultOpenRouterEP
	}
	if strings.HasSuffix(strings.TrimRight(ep, "/"), "/v1") {
		return strings.TrimRight(ep, "/") + "/chat/completions"
	}
	return ep
}

// parseModelSpecs reads OPENROUTER_MODELS (comma-separated, pipe-delimited fields).
// Format per entry: model|parser|authScheme|reasoningEffort
func parseModelSpecs(raw string) ([]ModelSpec, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var out []ModelSpec
	for _, chunk := range strings.Split(raw, ",") {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}
		parts := strings.Split(chunk, "|")
		spec := ModelSpec{Model: strings.TrimSpace(parts[0])}
		if spec.Model == "" {
			continue
		}
		if len(parts) > 1 {
			spec.Parser = strings.TrimSpace(parts[1])
		}
		if len(parts) > 2 {
			spec.AuthScheme = strings.TrimSpace(parts[2])
		}
		if len(parts) > 3 {
			spec.ReasoningEffort = strings.TrimSpace(parts[3])
		}
		out = append(out, spec)
	}
	return out, nil
}

func requireOpenRouterModels(t *testing.T) []ModelSpec {
	t.Helper()
	if !openRouterEnabled() {
		t.Skip("set SAPALOQ_OPENROUTER_E2E=1 (+ OPENROUTER_API_KEY + OPENROUTER_MODELS) to run the OpenRouter characterize suite")
	}
	if openRouterAPIKey() == "" {
		t.Skipf("%s is empty - skipping OpenRouter characterize suite", openRouterCredEnv)
	}
	specs, err := parseModelSpecs(os.Getenv("OPENROUTER_MODELS"))
	if err != nil {
		t.Fatalf("parse OPENROUTER_MODELS: %v", err)
	}
	if len(specs) == 0 {
		t.Skip("OPENROUTER_MODELS is empty - skipping (no models to characterize)")
	}
	return specs
}

func modelSlug(model string) string {
	s := strings.NewReplacer("/", "-", ":", "-", " ", "-").Replace(model)
	return strings.ToLower(s)
}

func sniffParser(model string) string {
	lower := strings.ToLower(model)
	for _, marker := range []string{"claude", "opus", "sonnet", "haiku"} {
		if strings.Contains(lower, marker) {
			return "claude"
		}
	}
	for _, marker := range []string{"kimi", "moonshot"} {
		if strings.Contains(lower, marker) {
			return "kimi"
		}
	}
	return "openai"
}

func openRouterRawDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(repoRoot(t), "tmp", "openrouter")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	return dir
}

func openRouterRawPath(t *testing.T, model string, stream StreamMode) string {
	t.Helper()
	return filepath.Join(openRouterRawDir(t), modelSlug(model)+"-"+stream.suffix()+".jsonl")
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found while resolving repo root for tmp/openrouter output")
		}
		dir = parent
	}
}
