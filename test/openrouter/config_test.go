package openrouter_test

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

// Config holds OpenRouter characterize-suite provider settings (env, paths, defaults).
type Config struct {
	Name                  string
	DisplayName           string
	GateEnv               string
	CredEnv               string
	ModelsEnv             string
	EndpointEnv           string
	DefaultEndpoint       string
	TmpSubdir             string
	DocKeyPrefix          string
	ConfigKeyPrefix       string
	CredentialsEnvDefault string
	MakeTarget            string
	TestDir               string
	DefaultReasoningEffort string
	DefaultThinkingType   string
}

// provider is the OpenRouter characterize config for this package.
var provider = Config{
	Name:                   "openrouter",
	DisplayName:            "OpenRouter",
	GateEnv:                "SAPALOQ_OPENROUTER_E2E",
	CredEnv:                "OPENROUTER_API_KEY",
	ModelsEnv:              "OPENROUTER_MODELS",
	EndpointEnv:            "OPENROUTER_ENDPOINT",
	DefaultEndpoint:        "https://openrouter.ai/api/v1/chat/completions",
	TmpSubdir:              "openrouter",
	DocKeyPrefix:           "openrouter",
	ConfigKeyPrefix:        "openrouter",
	CredentialsEnvDefault:  "OPENROUTER_API_KEY",
	MakeTarget:             "openrouter-characterize",
	TestDir:                "test/openrouter",
	DefaultReasoningEffort: defaultReasoningEffort,
	DefaultThinkingType:    defaultThinkingProbeType,
}

func (c Config) credEnvVar() string {
	return c.CredEnv
}

func (c Config) enabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(c.GateEnv)))
	return v == "1" || v == "true" || v == "yes"
}

func (c Config) apiKey() string {
	return strings.TrimSpace(os.Getenv(c.CredEnv))
}

func (c Config) endpoint() string {
	ep := strings.TrimSpace(os.Getenv(c.EndpointEnv))
	if ep == "" {
		return c.DefaultEndpoint
	}
	if strings.HasSuffix(strings.TrimRight(ep, "/"), "/v1") {
		return strings.TrimRight(ep, "/") + "/chat/completions"
	}
	return ep
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
		t.Skipf("set %s=1 (+ %s + %s) to run the %s characterize suite", c.GateEnv, c.CredEnv, c.ModelsEnv, c.DisplayName)
	}
	if c.apiKey() == "" {
		t.Skipf("%s is empty - skipping %s characterize suite", c.CredEnv, c.DisplayName)
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
