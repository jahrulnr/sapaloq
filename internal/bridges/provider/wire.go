package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
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
	// IdleTimeout bounds the silence between two consecutive SSE events once
	// the stream is open. Timeout is a generous whole-request cap; IdleTimeout
	// catches a stream that connects and then hangs (no data) far sooner, so
	// the sub-agent loop can retry the turn before the worker watchdog fails
	// it. Zero disables the idle check (whole-request Timeout still applies).
	IdleTimeout time.Duration
	// ContextWindow is the maximum input the bridge will forward, in
	// tokens. The bridge estimates tokens as len(content)/4 and drops the
	// oldest non-system messages when the conversation exceeds this.
	// Zero means "use DetectContextWindow" - the bridge layer does not
	// re-detect here so callers control the contract explicitly.
	ContextWindow int
	// MaxRetries bounds how many extra attempts runSSE makes after a transient
	// *pre-stream* failure (connection error, or a retryable status: 408, 429,
	// 5xx). Retries fire only before the first SSE byte is dispatched, so no
	// emitted delta is ever duplicated. Zero disables retries.
	MaxRetries int
	// Stream selects the wire framing. true (the default) opens an SSE stream
	// and dispatches token deltas as they arrive; false sends a single
	// non-stream request and parses one complete response, surfaced through the
	// same WireHandler as one batch of WireEvents. Use false for gateways that
	// buffer or don't support SSE. The IdleTimeout idle-gap check only applies
	// to the streaming path (a non-stream call is bounded by Timeout alone).
	Stream bool
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
	// Non-stream mode: one request, one complete response, parsed into the same
	// WireEvents the SSE handlers emit. The bridge layer is agnostic to which
	// path produced them.
	if !opts.Stream {
		return complete(ctx, opts, onEvent)
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
	err = runSSE(ctx, opts, body, handler.Handle)
	if err == nil {
		_ = handler.flushAndStop()
		return nil
	}
	return err
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
	err = runSSE(ctx, opts, body, handler.Handle)
	if err == nil {
		_ = handler.flushAndStop()
		return nil
	}
	return err
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
	err = runSSE(ctx, opts, body, handler.Handle)
	if err == nil {
		_ = handler.flushAndStop()
		return nil
	}
	return err
}

// buildOpenAIRequestBody assembles the OpenAI Chat Completions body for the
// given options. The fallback model is used when opts.Model is empty.
func buildOpenAIRequestBody(opts WireOptions, fallbackModel string) ([]byte, error) {
	req := openAIRequest{
		Model:    defaultIfEmpty(opts.Model, fallbackModel),
		Stream:   opts.Stream,
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
		Stream:   opts.Stream,
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
	system, messages := buildClaudePayload(opts.Messages, opts.Images)
	req := claudeRequest{
		Model:     defaultIfEmpty(opts.Model, fallbackModel),
		Stream:    opts.Stream,
		MaxTokens: 8192,
		System:    system,
		Messages:  messages,
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

// retryableError wraps a transient *pre-stream* failure (connection error or a
// retryable status) so runSSE can tell it apart from an error raised once the
// SSE stream has already started emitting deltas. Only pre-stream failures are
// safe to retry: re-sending after deltas were dispatched would duplicate output.
type retryableError struct{ err error }

func (e retryableError) Error() string { return e.err.Error() }
func (e retryableError) Unwrap() error { return e.err }

// runSSE POSTs the body to the upstream endpoint and dispatches every SSE
// line to onLine. It honours ctx cancellation and returns errStreamStopped
// when the upstream emits the [DONE] sentinel or the line handler signals
// stop.
//
// A transient pre-stream failure (connection error, or a retryable status:
// 408, 429, 5xx) is retried up to opts.MaxRetries times with exponential
// backoff + jitter. Once the stream is open and the first byte has been
// dispatched, no retry is attempted (so emitted deltas are never duplicated).
func runSSE(ctx context.Context, opts WireOptions, body []byte, onLine func([]byte) error) error {
	attempts := opts.MaxRetries + 1 // MaxRetries extra tries after the first
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			delay := retryBackoff(attempt)
			debug.Debugf("provider-bridge: retry %d/%d after %v (last error: %v)",
				attempt, attempts-1, delay, lastErr)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
		err := attemptSSE(ctx, opts, body, onLine)
		if err == nil || err == errStreamStopped {
			return err
		}
		var retryable retryableError
		if !errors.As(err, &retryable) {
			// Non-retryable: a 4xx (except 408/429), or any failure raised
			// after the stream already started emitting.
			return err
		}
		lastErr = retryable.err
	}
	return lastErr
}

// attemptSSE performs a single POST + SSE pump. Pre-stream failures are wrapped
// in retryableError; failures during streaming are returned bare so runSSE does
// not retry them.
func attemptSSE(ctx context.Context, opts WireOptions, body []byte, onLine func([]byte) error) error {
	req, cancel, err := buildHTTPRequest(ctx, opts, body)
	if err != nil {
		return err
	}
	defer cancel()
	debug.Debugf("provider-bridge: POST %s parser=%s auth=%s model=%s messages=%d bytes=%d",
		opts.Endpoint, opts.Parser, opts.Auth, opts.Model, len(opts.Messages), len(body))

	resp, err := streamHTTPClient(opts.IdleTimeout).Do(req)
	if err != nil {
		// A connection / transport failure is pre-stream by definition.
		if ctx.Err() != nil {
			return err // caller-driven cancellation: do not retry.
		}
		return retryableError{err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, readErr := readProviderBody(resp.Body, maxProviderErrorBytes)
		if readErr != nil {
			raw = []byte(readErr.Error())
		}
		statusErr := fmt.Errorf("provider-bridge: upstream status %d: %s", resp.StatusCode, upstreamErrorBody(raw))
		if isRetryableStatus(resp.StatusCode) {
			return retryableError{err: statusErr}
		}
		return statusErr
	}
	reader := newSSEReader(resp.Body)
	// From here the stream is open; pumpSSE may dispatch deltas, so its error
	// is returned bare (non-retryable).
	return pumpSSE(ctx, reader, resp.Body, opts.IdleTimeout, onLine)
}

// isRetryableStatus reports whether an HTTP status warrants a pre-stream retry.
// These are the transient classes: request timeout, rate limit, and the 5xx
// server / gateway failures (e.g. the Vercel AI Gateway `500 Connection error`
// seen routing Anthropic models behind api.blackbox.ai).
func isRetryableStatus(code int) bool {
	switch code {
	case http.StatusRequestTimeout, // 408
		http.StatusTooManyRequests: // 429
		return true
	default:
		return code >= 500
	}
}

// retryBackoff returns the wait before the given (1-based) retry attempt:
// exponential growth (500ms, 1s, 2s, 4s, …) capped at 8s, plus up to 250ms of
// jitter to avoid synchronised retries against a recovering gateway.
func retryBackoff(attempt int) time.Duration {
	const base = 500 * time.Millisecond
	const maxDelay = 8 * time.Second
	shift := attempt - 1
	if shift < 0 {
		shift = 0
	}
	if shift > 30 { // guard against overflow on absurd retry counts
		shift = 30
	}
	delay := base << shift
	if delay > maxDelay || delay <= 0 {
		delay = maxDelay
	}
	jitter := time.Duration(rand.Int63n(int64(250 * time.Millisecond)))
	return delay + jitter
}

// streamHTTPClient returns an HTTP client bounded for streaming. The idle
// timeout in pumpSSE only starts AFTER the response headers arrive, so it can't
// catch an upstream that accepts the TCP/TLS connection then stalls before
// sending the first byte (a common overloaded-gateway failure that otherwise
// hangs up to the whole-request timeout with no progress). We therefore bound
// the connect + TLS + time-to-first-byte (response header) phases to the same
// idle window so a pre-stream hang is surfaced just as fast as a mid-stream one.
// idle <= 0 falls back to the default client (no extra bounds).
func streamHTTPClient(idle time.Duration) *http.Client {
	if idle <= 0 {
		return http.DefaultClient
	}
	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: idle, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   idle,
		ResponseHeaderTimeout: idle,
		ExpectContinueTimeout: time.Second,
		ForceAttemptHTTP2:     true,
	}
	return &http.Client{Transport: tr}
}

// sseLine is one result from the background reader goroutine.
type sseLine struct {
	data []byte
	err  error
}

// errStreamIdle signals that the upstream stopped sending data mid-stream for
// longer than the configured idle window. It is distinct from a whole-request
// deadline so the bridge can explain it (and the sub-agent loop can retry).
var errStreamIdle = fmt.Errorf("provider-bridge: SSE idle timeout: no data from upstream")

// pumpSSE reads SSE lines while enforcing a per-event idle timeout. The
// blocking bufio read runs in a goroutine so a hung socket (connection open,
// no bytes) is bounded by idleTimeout rather than the much larger whole-request
// timeout. When the idle timer fires we close the body to unblock the reader
// goroutine, then return errStreamIdle. idleTimeout <= 0 disables the check.
func pumpSSE(ctx context.Context, reader *sseReader, body io.Closer, idleTimeout time.Duration, onLine func([]byte) error) error {
	lines := make(chan sseLine, 8)
	go func() {
		for {
			data, err := reader.ReadLine()
			lines <- sseLine{data: data, err: err}
			if err != nil {
				return
			}
		}
	}()

	var idle *time.Timer
	var idleC <-chan time.Time
	if idleTimeout > 0 {
		idle = time.NewTimer(idleTimeout)
		idleC = idle.C
		defer idle.Stop()
	}
	resetIdle := func() {
		if idle == nil {
			return
		}
		if !idle.Stop() {
			select {
			case <-idle.C:
			default:
			}
		}
		idle.Reset(idleTimeout)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-idleC:
			// Unblock the reader goroutine by closing the body; it will
			// observe a read error and exit, draining into the channel.
			_ = body.Close()
			return errStreamIdle
		case ln := <-lines:
			// Reset the idle timer ONLY on a meaningful (non-empty) line. SSE
			// servers and proxies emit blank-line keep-alives between events;
			// sseReader.ReadLine returns ("", nil) for those. Resetting on a
			// blank line would let a stream that delivers nothing but newlines
			// keep the connection "alive" forever - the model never responds yet
			// the idle timeout never fires (the observed "listening but
			// receiving nothing" stall). Only real payload counts as progress.
			if len(ln.data) > 0 {
				resetIdle()
				if err := onLine(ln.data); err != nil {
					if err == errStreamStopped {
						return nil
					}
					return err
				}
			}
			if ln.err != nil {
				if ln.err == io.EOF {
					return nil
				}
				return ln.err
			}
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

const (
	maxProviderResponseBytes = 32 * 1024 * 1024
	maxProviderErrorBytes    = 64 * 1024
	maxSSELineBytes          = 8 * 1024 * 1024
)

func newSSEReader(rd io.Reader) *sseReader {
	return &sseReader{r: bufio.NewReaderSize(rd, 64*1024)}
}

// ReadLine returns the next line (without the trailing newline) and any error
// from the underlying reader. Empty reads return ("", nil) and the caller
// should keep looping until EOF.
func (s *sseReader) ReadLine() ([]byte, error) {
	var out bytes.Buffer
	for {
		fragment, err := s.r.ReadSlice('\n')
		if len(fragment) > 0 {
			if out.Len()+len(fragment) > maxSSELineBytes {
				return nil, fmt.Errorf("provider-bridge: SSE event exceeds %d bytes", maxSSELineBytes)
			}
			_, _ = out.Write(fragment)
			if err == nil {
				return bytes.TrimRight(out.Bytes(), "\r\n"), nil
			}
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			if out.Len() > 0 && err == io.EOF {
				return bytes.TrimRight(out.Bytes(), "\r\n"), io.EOF
			}
			return nil, err
		}
	}
}

func readProviderBody(r io.Reader, limit int64) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("provider-bridge: response exceeds %d bytes", limit)
	}
	return raw, nil
}
