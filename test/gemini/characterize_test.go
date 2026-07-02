package gemini_test

import (
	"testing"
)

// TestCharacterizeNonNativeToolRoundTrip probes Gemini generateContent directly
// with net/http (no SapaLOQ orchestrator). Each model runs twice: stream and non-stream.
// Each run must call get_weather, accept a fake tool result, and answer with Jakarta data.
//
// If upstream rejects toolConfig AUTO, the probe retries with tools only.
//
// Gated: SAPALOQ_GEMINI_CHARACTERIZE_E2E=1 + GEMINI_API_KEY + GEMINI_MODELS.
func TestCharacterizeNonNativeToolRoundTrip(t *testing.T) {
	specs := requireGeminiModels(t)
	for _, spec := range specs {
		spec := spec
		t.Run(spec.Model, func(t *testing.T) {
			for _, mode := range []struct {
				name   string
				stream StreamMode
			}{
				{name: "stream", stream: StreamOn},
				{name: "nostream", stream: StreamOff},
			} {
				mode := mode
				t.Run(mode.name, func(t *testing.T) {
					rawPath, report := runRawCharacterize(t, spec, mode.stream)
					logCharacterReport(t, report)
					writeProviderCharacterizationDoc(t, spec, mode.stream, report, rawPath, countJSONLLines(t, rawPath))
					assertCharacterReport(t, report)
				})
			}
		})
	}
}
