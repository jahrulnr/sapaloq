package llamacpp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jahrulnr/sapaloq/internal/config"
)

// Doctor probes llama-server health and best-effort model preload for multi-model routers.
func Doctor(ctx context.Context, entry config.LLMBridge) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	base := BaseURL(entry.Endpoint)
	healthURL := HealthURL(entry.Endpoint)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
	if err != nil {
		return "", err
	}
	token := tokenForEntry(entry)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("llama-cpp: health probe failed (%s): %w", healthURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("llama-cpp: health %s returned %d: %s", healthURL, resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	msg := fmt.Sprintf("llama-cpp %s (health ok)", base)
	if model := strings.TrimSpace(entry.Model); model != "" {
		if warn := preloadModel(ctx, entry, model, token); warn != "" {
			msg += "; " + warn
		}
	}
	return msg, nil
}

func preloadModel(ctx context.Context, entry config.LLMBridge, model, token string) string {
	url := ModelsLoadURL(entry.Endpoint)
	body, err := json.Marshal(map[string]any{"model": model})
	if err != nil {
		return fmt.Sprintf("models/load marshal: %v", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Sprintf("models/load request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Sprintf("models/load warning (multi-model preload): %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Sprintf("models/load warning status %d: %s (ok for single-model startup)", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return "models/load ok for " + model
}

func tokenForEntry(entry config.LLMBridge) string {
	b := &Bridge{entry: entry}
	return b.token()
}
