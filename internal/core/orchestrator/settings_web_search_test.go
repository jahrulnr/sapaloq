package orchestrator

import (
	"context"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/searchwire"
)

func TestSettingsAllowsWebSearchConfig(t *testing.T) {
	raw := map[string]any{}
	patch := map[string]any{
		"webSearch": map[string]any{
			"limit":      4,
			"timeoutSec": 12,
		},
	}
	if err := config.ApplyPatch(raw, patch, defaultAllowedPaths); err != nil {
		t.Fatalf("webSearch settings patch rejected: %v", err)
	}
}

func TestApplyConfigRebuildsWebSearcher(t *testing.T) {
	cfg := config.DefaultConfig()
	dirs := config.RuntimeDirs(cfg)
	stub := stubWebSearchClient{search: func(context.Context, string) (*searchwire.Response, error) {
		return &searchwire.Response{}, nil
	}}
	o := &Orchestrator{
		cfg:         cfg,
		entry:       cfg.LLMBridge.Providers[0],
		memoryDir:   dirs.MemoryDir,
		workersDir:  dirs.WorkersDir,
		webSearcher: stub,
	}
	next := cfg
	next.WebSearch = config.WebSearchConfig{Limit: 3, TimeoutSec: 7}
	if err := o.applyConfig(next); err != nil {
		t.Fatalf("applyConfig: %v", err)
	}
	if _, ok := o.webSearcher.(*searchwire.Searcher); !ok {
		t.Fatalf("web searcher was not rebuilt: %T", o.webSearcher)
	}
}
