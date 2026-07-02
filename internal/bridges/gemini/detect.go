package gemini

import (
	"os"
	"strings"

	"github.com/jahrulnr/sapaloq/internal/config"
)

func resolveAPIKey(entry config.LLMBridge) string {
	if env := strings.TrimSpace(entry.CredentialsEnv); env != "" {
		if v := strings.TrimSpace(os.Getenv(env)); v != "" {
			return v
		}
	}
	if v := strings.TrimSpace(os.Getenv("GOOGLE_API_KEY")); v != "" {
		return v
	}
	return strings.TrimSpace(os.Getenv("GEMINI_API_KEY"))
}

func normalizeAPIBase(endpoint string) string {
	ep := strings.TrimSpace(endpoint)
	if ep == "" {
		ep = "https://generativelanguage.googleapis.com/v1beta"
	}
	ep = strings.TrimRight(ep, "/")
	if idx := strings.Index(ep, "/models/"); idx >= 0 {
		ep = ep[:idx]
	}
	if strings.HasSuffix(ep, ":generateContent") {
		ep = strings.TrimSuffix(ep, ":generateContent")
	}
	if strings.HasSuffix(ep, ":streamGenerateContent") {
		ep = strings.TrimSuffix(ep, ":streamGenerateContent")
	}
	return ep
}

func generateURL(base, model string, stream bool) string {
	action := "generateContent"
	if stream {
		action = "streamGenerateContent"
	}
	url := base + "/models/" + model + ":" + action
	if stream {
		url += "?alt=sse"
	}
	return url
}

func reasoningEffort(entry config.LLMBridge) string {
	if v := strings.TrimSpace(entry.ReasoningEffort); v != "" {
		return v
	}
	return "low"
}
