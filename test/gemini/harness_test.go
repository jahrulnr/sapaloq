package gemini_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const weatherToolName = "get_weather"

const weatherPrompt = "Test harness. User asks Jakarta weather. You MUST call get_weather with city Jakarta before answering. Reason step-by-step. After the tool result, reply in one short sentence citing the temperature."

const fakeWeatherToolResult = `{"city":"Jakarta","temperature_c":32,"conditions":"humid, partly cloudy"}`

type ModelSpec struct {
	Model           string
	Parser          string
	AuthScheme      string
	ReasoningEffort string
}

func effectiveReasoningEffort(spec ModelSpec) string {
	return provider.effectiveReasoningEffort(spec)
}

func geminiEnabled() bool { return provider.enabled() }

func geminiAPIKey() string { return provider.apiKey() }

func geminiAPIBase() string { return provider.apiBase() }

func geminiGenerateURL(model string, stream StreamMode) string {
	return provider.generateURL(model, stream)
}

func requireGeminiModels(t *testing.T) []ModelSpec {
	return provider.requireModels(t)
}

func geminiRawPath(t *testing.T, model string, stream StreamMode) string {
	return provider.rawPath(t, model, stream)
}

func geminiTranscriptPath(t *testing.T, model string, stream StreamMode) string {
	return provider.transcriptPath(t, model, stream)
}

func modelSlug(model string) string {
	s := strings.NewReplacer("/", "-", ":", "-", " ", "-").Replace(model)
	return strings.ToLower(s)
}

func sniffParser(model string) string {
	lower := strings.ToLower(model)
	if strings.Contains(lower, "gemini") {
		return "gemini"
	}
	return "gemini"
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
