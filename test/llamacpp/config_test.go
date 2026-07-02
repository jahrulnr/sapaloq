package llamacpp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	defaultReasoningEffort   = "low"
	defaultThinkingProbeType = "enabled"
)

// Config holds llama.cpp characterize-suite settings (local llama-server contract).
type Config struct {
	Name                   string
	DisplayName            string
	GateEnv                string
	CredEnv                string
	ModelsEnv              string
	EndpointEnv            string
	DefaultEndpoint        string
	TmpSubdir              string
	DocKeyPrefix           string
	ConfigKeyPrefix        string
	CredentialsEnvDefault  string
	CredentialsEnvOverride string
	MakeTarget             string
	TestDir                string
	DefaultReasoningEffort string
	DefaultThinkingType    string
}

// provider is the llama.cpp characterize config for this package.
var provider = Config{
	Name:                   "llamacpp",
	DisplayName:            "llama.cpp (llama-server)",
	GateEnv:                "SAPALOQ_LLAMACPP_CHARACTERIZE_E2E",
	CredEnv:                "LLAMACPP_API_KEY",
	ModelsEnv:              "LLAMACPP_MODELS",
	EndpointEnv:            "LLAMACPP_ENDPOINT",
	DefaultEndpoint:        "http://127.0.0.1:8080/v1/chat/completions",
	TmpSubdir:              "llamacpp",
	DocKeyPrefix:           "llamacpp",
	ConfigKeyPrefix:        "llamacpp",
	CredentialsEnvDefault:  "LLAMACPP_API_KEY",
	CredentialsEnvOverride: "LLAMACPP_CREDENTIALS_ENV",
	MakeTarget:             "llamacpp-characterize",
	TestDir:                "test/llamacpp",
	DefaultReasoningEffort: defaultReasoningEffort,
	DefaultThinkingType:    defaultThinkingProbeType,
}

func (c Config) credentialsEnv() string {
	if v := strings.TrimSpace(os.Getenv(c.CredentialsEnvOverride)); v != "" {
		return v
	}
	return c.CredentialsEnvDefault
}

func (c Config) credEnvVar() string {
	if k := strings.TrimSpace(os.Getenv("LLAMA_API_KEY")); k != "" {
		return "LLAMA_API_KEY"
	}
	return c.credentialsEnv()
}

func (c Config) enabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(c.GateEnv)))
	return v == "1" || v == "true" || v == "yes"
}

func (c Config) apiKey() string {
	if v := strings.TrimSpace(os.Getenv(c.credentialsEnv())); v != "" {
		return v
	}
	return strings.TrimSpace(os.Getenv("LLAMA_API_KEY"))
}

func (c Config) endpoint() string {
	ep := strings.TrimSpace(os.Getenv(c.EndpointEnv))
	if ep == "" {
		ep = strings.TrimSpace(os.Getenv("LLAMA_SERVER_URL"))
	}
	if ep == "" {
		return c.DefaultEndpoint
	}
	ep = strings.TrimRight(ep, "/")
	if strings.HasSuffix(ep, "/v1") {
		return ep + "/chat/completions"
	}
	if strings.Contains(ep, "/chat/completions") {
		return ep
	}
	return ep + "/v1/chat/completions"
}

func (c Config) clientTimeout() time.Duration {
	if v := strings.TrimSpace(os.Getenv("LLAMACPP_CLIENT_TIMEOUT_SEC")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 10 * time.Minute
}

func (c Config) preloadModel(ctx context.Context, model string) error {
	if strings.TrimSpace(model) == "" {
		return nil
	}
	ep := c.endpoint()
	base := strings.TrimSuffix(ep, "/v1/chat/completions")
	base = strings.TrimSuffix(base, "/chat/completions")
	url := strings.TrimRight(base, "/") + "/models/load"
	body, err := json.Marshal(map[string]any{"model": model})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if key := c.apiKey(); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("models/load status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

func (c Config) healthURL() string {
	ep := c.endpoint()
	for _, suffix := range []string{"/v1/chat/completions", "/chat/completions"} {
		if strings.HasSuffix(ep, suffix) {
			return strings.TrimSuffix(ep, suffix) + "/health"
		}
	}
	return strings.TrimRight(ep, "/") + "/health"
}

func (c Config) effectiveReasoningEffort(spec ModelSpec) string {
	if strings.TrimSpace(spec.ReasoningEffort) != "" {
		return strings.TrimSpace(spec.ReasoningEffort)
	}
	return c.DefaultReasoningEffort
}

func (c Config) parseModelSpecs(raw string) ([]ModelSpec, error) {
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

func (c Config) requireModels(t *testing.T) []ModelSpec {
	t.Helper()
	if !c.enabled() {
		t.Skipf("set %s=1 (+ %s + reachable llama-server) to run the %s characterize suite", c.GateEnv, c.ModelsEnv, c.DisplayName)
	}
	specs, err := c.parseModelSpecs(os.Getenv(c.ModelsEnv))
	if err != nil {
		t.Fatalf("parse %s: %v", c.ModelsEnv, err)
	}
	if len(specs) == 0 {
		t.Skipf("%s is empty - skipping (no models to characterize)", c.ModelsEnv)
	}
	if err := pingHealth(t); err != nil {
		t.Skipf("llama-server health check failed (%s): %v", c.healthURL(), err)
	}
	return specs
}

func pingHealth(t *testing.T) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, provider.healthURL(), nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode == http.StatusServiceUnavailable {
		return fmt.Errorf("status 503 (model loading): %s", strings.TrimSpace(string(body)))
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var probe struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(body, &probe) == nil && probe.Status != "" && probe.Status != "ok" {
		return fmt.Errorf("health status %q", probe.Status)
	}
	return nil
}

func (c Config) tmpDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(repoRoot(t), "tmp", c.TmpSubdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	return dir
}

func (c Config) rawPath(t *testing.T, model string, stream StreamMode) string {
	t.Helper()
	return filepath.Join(c.tmpDir(t), modelSlug(model)+"-"+stream.suffix()+".jsonl")
}

func (c Config) transcriptPath(t *testing.T, model string, stream StreamMode) string {
	t.Helper()
	return strings.TrimSuffix(c.rawPath(t, model, stream), ".jsonl") + ".md"
}

func (c Config) providerDocSlug(spec ModelSpec, stream StreamMode) string {
	return c.DocKeyPrefix + "-" + modelSlug(spec.Model) + "-" + stream.suffix()
}

func (c Config) configEntryKey(spec ModelSpec) string {
	return c.ConfigKeyPrefix + "-" + modelSlug(spec.Model)
}
