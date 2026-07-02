package llamacpp_test

import (
	"testing"
)

// TestCharacterizeNonNativeToolRoundTrip probes llama-server /v1/chat/completions
// directly with net/http (no SapaLOQ orchestrator). Each model runs twice:
// stream and non-stream. Default target: http://127.0.0.1:8080 (upstream llama.cpp);
// override with LLAMACPP_ENDPOINT (e.g. :16285 on custom deploy).
//
// Gated: SAPALOQ_LLAMACPP_CHARACTERIZE_E2E=1 + LLAMACPP_MODELS (+ optional LLAMACPP_API_KEY).
func TestCharacterizeNonNativeToolRoundTrip(t *testing.T) {
	specs := requireLlamacppModels(t)
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
