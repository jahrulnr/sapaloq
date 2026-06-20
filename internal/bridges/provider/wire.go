package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/debug"
	"github.com/jahrulnr/sapaloq/internal/parse"
	toolprovider "github.com/jahrulnr/sapaloq/internal/parse/tools/provider"
)

// WireOptions configures one streaming call. Built by the bridge from
// config.LLMBridge + config.Runtime + bridge.Request.
type WireOptions struct {
	Parser          ParserKind
	Auth            AuthScheme
	APIVersion      string
	Endpoint        string
	Token           string
	Model           string
	Messages        []bridge.Message
	Images          []bridge.Image
	ReasoningEffort string
	MaxTokens       int
	DeclaredTools   []string
	SessionID       string
	Timeout         time.Duration
	// ContextWindow is the maximum input the bridge will forward, in
	// tokens. The bridge estimates tokens as len(content)/4 and drops the
	// oldest non-system messages when the conversation exceeds this.
	// Zero means "use DetectContextWindow" — the bridge layer does not
	// re-detect here so callers control the contract explicitly.
	ContextWindow int
}

// WireEvent is what the wire layer pushes back into the bridge.
type WireEvent struct {
	Thinking string
	Text     string
	Tool     parse.ToolCall
}

// WireHandler is invoked for every normalised wire event. Returning false
// stops the stream (used by the bridge to honour ctx cancellation).
type WireHandler func(ev WireEvent) bool

// Stream dispatches the live HTTP/SSE call. It is the responsibility of the
// caller to forward events into bridge.StreamEvent channels.
func Stream(ctx context.Context, opts WireOptions, onEvent WireHandler) error {
	if opts.Token == "" {
		return fmt.Errorf("provider-bridge: token is required (set %s)", opts.Auth)
	}
	switch opts.Parser {
	case ParserClaude:
		return streamClaude(ctx, opts, onEvent)
	case ParserKimi:
		return streamKimi(ctx, opts, onEvent)
	default:
		return streamOpenAI(ctx, opts, onEvent)
	}
}

// streamOpenAI handles OpenAI Chat Completions, OpenRouter, TokenRouter, and
// any other OpenAI-compatible API.
func streamOpenAI(ctx context.Context, opts WireOptions, on WireHandler) error {
	body, err := buildOpenAIRequestBody(opts, "gpt-4o-mini")
	if err != nil {
		return err
	}
	acc := toolprovider.NewAccumulatorOpenAI("openai_inline")
	handler := newOpenAILineHandler(acc, on)
	return runSSE(ctx, opts, body, handler.Handle)
}

// streamKimi handles Moonshot AI / Kimi models. The wire is identical to
// OpenAI Chat Completions; the only addition is the `thinking` parameter on
// the request body and `reasoning_content` on each delta (handled by the
// generic openAI struct via the Reasoning field).
func streamKimi(ctx context.Context, opts WireOptions, on WireHandler) error {
	body, err := buildKimiRequestBody(opts, "kimi-k2.6")
	if err != nil {
		return err
	}
	acc := toolprovider.NewAccumulatorKimi("kimi_inline")
	handler := newKimiLineHandler(acc, on)
	return runSSE(ctx, opts, body, handler.Handle)
}

// streamClaude handles Anthropic Messages API. The event stream is JSON
// objects separated by blank lines (no `data:` prefix on event kind).
func streamClaude(ctx context.Context, opts WireOptions, on WireHandler) error {
	body, err := buildClaudeRequestBody(opts, "claude-sonnet-4-5")
	if err != nil {
		return err
	}
	acc := toolprovider.NewAccumulatorClaude("claude_inline")
	handler := newClaudeLineHandler(acc, on)
	return runSSE(ctx, opts, body, handler.Handle)
}

// buildOpenAIRequestBody assembles the OpenAI Chat Completions body for the
// given options. The fallback model is used when opts.Model is empty.
func buildOpenAIRequestBody(opts WireOptions, fallbackModel string) ([]byte, error) {
	req := openAIRequest{
		Model:    defaultIfEmpty(opts.Model, fallbackModel),
		Stream:   true,
		Messages: buildOpenAIMessages(opts.Messages, opts.Images),
	}
	if opts.ReasoningEffort != "" {
		req.ReasoningEffort = opts.ReasoningEffort
	}
	if opts.MaxTokens > 0 {
		req.MaxCompletionTokens = opts.MaxTokens
	}
	if len(opts.DeclaredTools) > 0 {
		req.Tools = buildOpenAITools(opts.DeclaredTools)
	}
	return json.Marshal(req)
}

// buildKimiRequestBody assembles the Kimi body. The `thinking.type` field is
// toggled to "enabled" when a reasoning effort is configured.
func buildKimiRequestBody(opts WireOptions, fallbackModel string) ([]byte, error) {
	req := openAIRequest{
		Model:    defaultIfEmpty(opts.Model, fallbackModel),
		Stream:   true,
		Messages: buildOpenAIMessages(opts.Messages, opts.Images),
	}
	if opts.ReasoningEffort != "" {
		req.ExtraBody = map[string]any{
			"thinking": map[string]any{"type": "enabled"},
		}
	}
	if opts.MaxTokens > 0 {
		req.MaxCompletionTokens = opts.MaxTokens
	}
	if len(opts.DeclaredTools) > 0 {
		req.Tools = buildOpenAITools(opts.DeclaredTools)
	}
	return json.Marshal(req)
}

// buildClaudeRequestBody assembles the Anthropic Messages body.
func buildClaudeRequestBody(opts WireOptions, fallbackModel string) ([]byte, error) {
	req := claudeRequest{
		Model:     defaultIfEmpty(opts.Model, fallbackModel),
		Stream:    true,
		MaxTokens: 8192,
		Messages:  buildClaudeMessages(opts.Messages, opts.Images),
	}
	if opts.MaxTokens > 0 {
		req.MaxTokens = opts.MaxTokens
	}
	if opts.ReasoningEffort != "" {
		req.Thinking = claudeThinkingFromEffort(opts.ReasoningEffort)
	}
	if len(opts.DeclaredTools) > 0 {
		req.Tools = buildClaudeTools(opts.DeclaredTools)
	}
	return json.Marshal(req)
}

// runSSE POSTs the body to the upstream endpoint and dispatches every SSE
// line to onLine. It honours ctx cancellation and returns errStreamStopped
// when the upstream emits the [DONE] sentinel or the line handler signals
// stop.
func runSSE(ctx context.Context, opts WireOptions, body []byte, onLine func([]byte) error) error {
	req, cancel, err := buildHTTPRequest(ctx, opts, body)
	if err != nil {
		return err
	}
	defer cancel()
	debug.Debugf("provider-bridge: POST %s parser=%s auth=%s model=%s messages=%d bytes=%d",
		opts.Endpoint, opts.Parser, opts.Auth, opts.Model, len(opts.Messages), len(body))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("provider-bridge: upstream status %d: %s", resp.StatusCode, upstreamErrorBody(raw))
	}
	reader := newSSEReader(resp.Body)
	for {
		line, lineErr := reader.ReadLine()
		if len(line) > 0 {
			if err := onLine(line); err != nil {
				if err == errStreamStopped {
					return nil
				}
				return err
			}
		}
		if lineErr != nil {
			if lineErr == io.EOF {
				return nil
			}
			return lineErr
		}
	}
}

// buildHTTPRequest assembles the net/http request with the right auth
// header layout for the chosen parser. The returned cancel func releases
// the timeout context and MUST be called by the caller (typically runSSE)
// once the response body has been fully read or the request is aborted.
func buildHTTPRequest(ctx context.Context, opts WireOptions, body []byte) (*http.Request, context.CancelFunc, error) {
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, opts.Endpoint, bytes.NewReader(body))
	if err != nil {
		cancel()
		return nil, cancel, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	switch opts.Auth {
	case AuthXAPIKey:
		req.Header.Set("x-api-key", opts.Token)
		req.Header.Set("anthropic-version", opts.APIVersion)
	default:
		req.Header.Set("Authorization", "Bearer "+opts.Token)
	}
	return req, cancel, nil
}

// errStreamStopped signals the WireHandler that we should stop streaming.
var errStreamStopped = fmt.Errorf("provider-bridge: stream stopped by handler")

func defaultIfEmpty(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func upstreamErrorBody(raw []byte) string {
	body := strings.TrimSpace(string(raw))
	if body == "" {
		return "empty response body"
	}
	if looksLikeHTML(body) {
		return "HTML error page from upstream"
	}
	const maxBodyLen = 1200
	if len(body) > maxBodyLen {
		return body[:maxBodyLen] + "…"
	}
	return body
}

func looksLikeHTML(body string) bool {
	lower := strings.ToLower(strings.TrimSpace(body))
	return strings.HasPrefix(lower, "<!doctype html") || strings.HasPrefix(lower, "<html")
}

// sseReader streams one line at a time from an HTTP response body. It is a
// thin wrapper around bufio.Reader so the wire layer can write `for { line, err
// := reader.ReadLine(); ... }` without sprinkling bufio imports at every call
// site.
type sseReader struct {
	r *bufio.Reader
}

func newSSEReader(rd io.Reader) *sseReader {
	return &sseReader{r: bufio.NewReaderSize(rd, 64*1024)}
}

// ReadLine returns the next line (without the trailing newline) and any error
// from the underlying reader. Empty reads return ("", nil) and the caller
// should keep looping until EOF.
func (s *sseReader) ReadLine() ([]byte, error) {
	for {
		line, err := s.r.ReadBytes('\n')
		if len(line) > 0 {
			return bytes.TrimRight(line, "\r\n"), nil
		}
		if err != nil {
			return nil, err
		}
	}
}
