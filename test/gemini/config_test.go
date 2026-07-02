package gemini_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	defaultReasoningEffort   = "low"
	defaultThinkingProbeType = "enabled"
)

// Config holds Gemini characterize-suite provider settings (env, paths, defaults).
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

// provider is the Gemini characterize config for this package.
var provider = Config{
	Name:                   "gemini",
	DisplayName:            "Gemini",
	GateEnv:                "SAPALOQ_GEMINI_CHARACTERIZE_E2E",
	CredEnv:                "GEMINI_API_KEY",
	ModelsEnv:              "GEMINI_MODELS",
	EndpointEnv:            "GEMINI_ENDPOINT",
	DefaultEndpoint:        "https://generativelanguage.googleapis.com/v1beta",
	TmpSubdir:              "gemini",
	DocKeyPrefix:           "gemini",
	ConfigKeyPrefix:        "gemini",
	CredentialsEnvDefault:  "GEMINI_API_KEY",
	CredentialsEnvOverride: "GEMINI_CREDENTIALS_ENV",
	MakeTarget:             "gemini-characterize",
	TestDir:                "test/gemini",
	DefaultReasoningEffort: defaultReasoningEffort,
	DefaultThinkingType:    defaultThinkingProbeType,
}

func (c Config) apiKey() string {
	if v := strings.TrimSpace(os.Getenv(c.CredentialsEnvOverride)); v != "" {
		return strings.TrimSpace(os.Getenv(v))
	}
	// Google client convention: GOOGLE_API_KEY wins when both are set.
	// https://ai.google.dev/gemini-api/docs/api-key
	if k := strings.TrimSpace(os.Getenv("GOOGLE_API_KEY")); k != "" {
		return k
	}
	return strings.TrimSpace(os.Getenv(c.CredentialsEnvDefault))
}

func (c Config) credEnvVar() string {
	if v := strings.TrimSpace(os.Getenv(c.CredentialsEnvOverride)); v != "" {
		return v
	}
	if strings.TrimSpace(os.Getenv("GOOGLE_API_KEY")) != "" {
		return "GOOGLE_API_KEY"
	}
	return c.CredentialsEnvDefault
}

func (c Config) enabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(c.GateEnv)))
	return v == "1" || v == "true" || v == "yes"
}

func (c Config) apiBase() string {
	ep := strings.TrimSpace(os.Getenv(c.EndpointEnv))
	if ep == "" {
		return strings.TrimRight(c.DefaultEndpoint, "/")
	}
	return strings.TrimRight(ep, "/")
}

func (c Config) generateURL(model string, stream StreamMode) string {
	action := "generateContent"
	if stream {
		action = "streamGenerateContent"
	}
	url := c.apiBase() + "/models/" + model + ":" + action
	if stream {
		url += "?alt=sse"
	}
	return url
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
		t.Skipf("set %s=1 (+ GOOGLE_API_KEY or %s + %s) to run the %s characterize suite", c.GateEnv, c.CredentialsEnvDefault, c.ModelsEnv, c.DisplayName)
	}
	if c.apiKey() == "" {
		t.Skipf("GOOGLE_API_KEY and %s are empty - skipping %s characterize suite", c.CredentialsEnvDefault, c.DisplayName)
	}
	specs, err := c.parseModelSpecs(os.Getenv(c.ModelsEnv))
	if err != nil {
		t.Fatalf("parse %s: %v", c.ModelsEnv, err)
	}
	if len(specs) == 0 {
		t.Skipf("%s is empty - skipping (no models to characterize)", c.ModelsEnv)
	}
	return specs
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

func (c Config) recommendedEndpoint(model string) string {
	return c.apiBase() + "/models/" + model + ":generateContent"
}
