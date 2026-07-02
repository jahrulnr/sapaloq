package llamacpp

import (
	"strings"
)

// NormalizeEndpoint returns an OpenAI chat-completions URL for llama-server.
// Empty input defaults to upstream llama.cpp port 8080.
func NormalizeEndpoint(endpoint string) string {
	ep := strings.TrimSpace(endpoint)
	if ep == "" {
		return defaultHostPort + chatCompletionsPath
	}
	ep = strings.TrimRight(ep, "/")
	lower := strings.ToLower(ep)
	if strings.HasSuffix(lower, "/v1/chat/completions") || strings.HasSuffix(lower, "/chat/completions") {
		return ep
	}
	return ep + chatCompletionsPath
}

// BaseURL strips the chat-completions suffix for health and models/load probes.
func BaseURL(endpoint string) string {
	ep := NormalizeEndpoint(endpoint)
	for _, suffix := range []string{"/v1/chat/completions", "/chat/completions"} {
		if strings.HasSuffix(ep, suffix) {
			return strings.TrimSuffix(ep, suffix)
		}
	}
	return strings.TrimRight(ep, "/")
}

// HealthURL returns GET /health for the llama-server base.
func HealthURL(endpoint string) string {
	return strings.TrimRight(BaseURL(endpoint), "/") + "/health"
}

// ModelsLoadURL returns POST /models/load for multi-model routers.
func ModelsLoadURL(endpoint string) string {
	return strings.TrimRight(BaseURL(endpoint), "/") + "/models/load"
}
