package llamacpp_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const weatherToolName = "get_weather"

// weatherPrompt forces a tool round-trip through the fake get_weather tool.
const weatherPrompt = "Test harness. User asks Jakarta weather. You MUST call get_weather with city Jakarta before answering. Reason step-by-step. After the tool result, reply in one short sentence citing the temperature."

const fakeWeatherToolResult = `{"city":"Jakarta","temperature_c":32,"conditions":"humid, partly cloudy"}`

// ModelSpec is one provider model entry from the characterize models env var.
// Format per entry: model|parser|authScheme|reasoningEffort
type ModelSpec struct {
	Model           string
	Parser          string
	AuthScheme      string
	ReasoningEffort string
}

func effectiveReasoningEffort(spec ModelSpec) string {
	return provider.effectiveReasoningEffort(spec)
}

func llamacppEnabled() bool { return provider.enabled() }

func llamacppAPIKey() string { return provider.apiKey() }

func llamacppEndpoint() string { return provider.endpoint() }

func requireLlamacppModels(t *testing.T) []ModelSpec {
	return provider.requireModels(t)
}

func llamacppRawDir(t *testing.T) string { return provider.tmpDir(t) }

func llamacppRawPath(t *testing.T, model string, stream StreamMode) string {
	return provider.rawPath(t, model, stream)
}

func llamacppTranscriptPath(t *testing.T, model string, stream StreamMode) string {
	return provider.transcriptPath(t, model, stream)
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
			t.Fatal("go.mod not found while resolving repo root for characterize output")
		}
		dir = parent
	}
}
