package llamacpp

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/debug"
	"github.com/jahrulnr/sapaloq/internal/bridges/provider"
)

// Bridge is a thin preset over provider OpenAI wire for local llama-server.
type Bridge struct {
	entry config.LLMBridge
}

// New validates the entry and returns a llama-cpp bridge.
func New(entry config.LLMBridge) (*Bridge, error) {
	if strings.TrimSpace(entry.Model) == "" {
		return nil, fmt.Errorf("llama-cpp: model is required")
	}
	return &Bridge{entry: entry}, nil
}

func (b *Bridge) ID() string { return driverID }

func (b *Bridge) Caps() bridge.BridgeCaps {
	parser := b.resolvedParser()
	return bridge.BridgeCaps{
		Thinking: parser == provider.ParserOpenAI || parser == provider.ParserKimi || parser == provider.ParserClaude,
		Tools:    true,
		LiveAPI:  NormalizeEndpoint(b.entry.Endpoint) != "",
	}
}

func (b *Bridge) Complete(ctx context.Context, req bridge.Request) (<-chan bridge.StreamEvent, error) {
	out := make(chan bridge.StreamEvent, 32)
	entry := b.entry
	entry.Endpoint = NormalizeEndpoint(entry.Endpoint)

	opts, err := b.buildWireOptions(entry, req)
	if err != nil {
		errEv := bridge.NewEvent(bridge.EventError)
		errEv.SessionID = req.SessionID
		errEv.Error = err.Error()
		go func() {
			defer close(out)
			out <- errEv
		}()
		return out, nil
	}

	debug.Debugf("llama-cpp: complete session=%s parser=%s auth=%s model=%s endpoint=%s",
		req.SessionID, opts.Parser, opts.Auth, opts.Model, opts.Endpoint)

	go provider.RunStream(ctx, entry, opts, req, out)
	return out, nil
}

func (b *Bridge) buildWireOptions(entry config.LLMBridge, req bridge.Request) (provider.WireOptions, error) {
	opts, err := provider.BuildWireOptions(entry, req)
	if err != nil {
		return provider.WireOptions{}, err
	}
	opts.Parser = b.resolvedParser()
	opts.Endpoint = NormalizeEndpoint(entry.Endpoint)
	opts.Auth = b.resolvedAuth(opts.Parser)
	opts.Token = b.token()
	if opts.Token == "" {
		opts.Auth = provider.AuthNone
	}
	if strings.TrimSpace(opts.Model) == "" {
		opts.Model = entry.Model
	}
	return opts, nil
}

func (b *Bridge) resolvedParser() provider.ParserKind {
	if p := parserFromConfig(b.entry.Parser); p != "" {
		return p
	}
	return provider.ParserOpenAI
}

func parserFromConfig(s string) provider.ParserKind {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "openai":
		return provider.ParserOpenAI
	case "claude", "anthropic":
		return provider.ParserClaude
	case "kimi", "moonshot":
		return provider.ParserKimi
	}
	return ""
}

func (b *Bridge) resolvedAuth(parser provider.ParserKind) provider.AuthScheme {
	if auth, ok := parseAuthScheme(b.entry.AuthScheme); ok {
		return auth
	}
	if b.token() != "" {
		if parser == provider.ParserClaude {
			return provider.AuthXAPIKey
		}
		return provider.AuthBearer
	}
	return provider.AuthNone
}

func parseAuthScheme(s string) (provider.AuthScheme, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "bearer":
		return provider.AuthBearer, true
	case "x-api-key", "x_api_key", "anthropic":
		return provider.AuthXAPIKey, true
	case "none":
		return provider.AuthNone, true
	}
	return "", false
}

func (b *Bridge) token() string {
	if env := strings.TrimSpace(b.entry.CredentialsEnv); env != "" {
		return strings.TrimSpace(os.Getenv(env))
	}
	for _, name := range []string{"LLAMACPP_API_KEY", "LLAMA_API_KEY"} {
		if t := strings.TrimSpace(os.Getenv(name)); t != "" {
			return t
		}
	}
	return ""
}
