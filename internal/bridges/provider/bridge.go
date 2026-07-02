// Package provider is a multi-model LLM bridge that speaks OpenAI Chat
// Completions, Anthropic Messages, and Kimi (Moonshot) - selected automatically
// from config + endpoint URL. The wire layer is the same regardless of
// parser: HTTP/POST; only the request body shape and per-event format differ.
//
// Each provider entry chooses its framing via the `stream` config flag
// (default true): streaming dispatches token deltas as Server-Sent Events,
// while non-stream sends a single request and parses one complete response.
// Both paths normalise into the same WireEvent sequence, so the bridge and the
// orchestrator above are agnostic to which framing produced a turn.
package provider

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/debug"
	"github.com/jahrulnr/sapaloq/internal/parse"
	leakpkg "github.com/jahrulnr/sapaloq/internal/parse/tools/provider"
)

// Bridge is the OpenAI/Claude/Kimi multi-provider bridge. It implements
// bridge.Bridge so it can be slotted into the runtime alongside cursor.
type Bridge struct {
	entry config.LLMBridge
}

// New returns a fresh Bridge. The constructor does not probe the network; the
// first Complete call validates the configuration lazily.
func New(entry config.LLMBridge) (*Bridge, error) {
	if strings.TrimSpace(entry.Endpoint) == "" {
		return nil, fmt.Errorf("provider-bridge: endpoint is required")
	}
	if strings.TrimSpace(entry.CredentialsEnv) == "" {
		return nil, fmt.Errorf("provider-bridge: credentials env is required (e.g. OPENAI_API_KEY)")
	}
	return &Bridge{entry: entry}, nil
}

// ID is the registry key. The runtime uses it to look up the bridge when a
// caller names it in the request or config.
func (b *Bridge) ID() string { return "provider-bridge" }

// Caps advertises the capabilities the bridge supports. LiveAPI is true only
// when an access token can actually be loaded from the env var; otherwise we
// refuse to promise a live stream.
func (b *Bridge) Caps() bridge.BridgeCaps {
	parser := DetectParser(b.entry)
	token := b.token()
	return bridge.BridgeCaps{
		Thinking: parser == ParserOpenAI || parser == ParserKimi || parser == ParserClaude,
		Tools:    true,
		LiveAPI:  token != "",
	}
}

// Complete opens a streaming channel and dispatches the wire layer. Each
// emitted StreamEvent is a thin wrapper over the wire event so the runtime
// can route thinking / text / tool_call into the orchestrator.
func (b *Bridge) Complete(ctx context.Context, req bridge.Request) (<-chan bridge.StreamEvent, error) {
	out := make(chan bridge.StreamEvent, 32)
	parser := DetectParser(b.entry)
	auth := DetectAuthScheme(b.entry, parser)
	token := b.token()
	if token == "" && auth != AuthNone {
		errEv := bridge.NewEvent(bridge.EventError)
		errEv.SessionID = req.SessionID
		errEv.Error = fmt.Sprintf("provider-bridge: token env %s is empty", b.entry.CredentialsEnv)
		go func() {
			defer close(out)
			out <- errEv
		}()
		return out, nil
	}

	opts, err := b.buildWireOptions(req)
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
	debug.Debugf("provider-bridge: complete session=%s parser=%s auth=%s model=%s endpoint=%s",
		req.SessionID, opts.Parser, opts.Auth, req.Model, opts.Endpoint)

	go b.runStream(ctx, opts, req, out)
	return out, nil
}

// buildWireOptions translates the active entry + request into a WireOptions
// struct. Pulled out of Complete to keep the hot path readable.
func (b *Bridge) buildWireOptions(req bridge.Request) (WireOptions, error) {
	parser := DetectParser(b.entry)
	auth := DetectAuthScheme(b.entry, parser)
	apiVersion := DetectAPIVersion(b.entry)
	reasoning := DetectReasoningEffort(b.entry)
	maxTokens := DetectMaxTokens(b.entry)
	contextWindow := DetectContextWindow(b.entry)
	// Apply the context window to the messages we forward to the model.
	messages, err := FitMessagesToContextStrict(req.Messages, contextWindow)
	if err != nil {
		return WireOptions{}, err
	}
	declaredTools := req.DeclaredTools
	if len(declaredTools) == 0 {
		declaredTools = b.entry.DeclaredTools
	}
	return WireOptions{
		Parser:          parser,
		Auth:            auth,
		APIVersion:      apiVersion,
		Endpoint:        b.entry.Endpoint,
		Token:           b.token(),
		Model:           req.Model,
		Messages:        messages,
		Images:          req.Images,
		ReasoningEffort: reasoning,
		MaxTokens:       maxTokens,
		DeclaredTools:   declaredTools,
		SessionID:       req.SessionID,
		ContextWindow:   contextWindow,
		Timeout:         b.entry.RequestTimeout(),
		IdleTimeout:     b.entry.StreamIdleTimeout(),
		MaxRetries:      b.entry.ResolveMaxRetries(),
		Stream:          b.entry.StreamEnabled(),
	}, nil
}

// runStream wires the per-event handler. Returning errStreamStopped is the
// normal way to end the stream; any other error is surfaced as EventError.
func (b *Bridge) runStream(ctx context.Context, opts WireOptions, req bridge.Request, out chan<- bridge.StreamEvent) {
	defer close(out)
	var mu sync.Mutex
	finishOnce := sync.Once{}
	finish := func() {
		finishOnce.Do(func() {
			ev := bridge.NewEvent(bridge.EventDone)
			ev.SessionID = req.SessionID
			sendStreamEvent(ctx, &mu, out, ev)
		})
	}

	// splitter separates inline <think>...</think> reasoning from the visible
	// answer for providers that stream reasoning in the content channel.
	var splitter thinkSplitter
	// leak reassembles a tool call that the model emitted inline in the content
	// channel split across many deltas (a single delta never holds a balanced
	// {...} for a big argument like a whole file, so per-delta scanning loses
	// it). Restricted to declared tool names to avoid misreading JSON inside
	// file content as a call.
	leak := newLeakScanner(opts.DeclaredTools)
	handler := func(ev WireEvent) bool {
		return b.handleWireEvent(ctx, &mu, out, req.SessionID, ev, &splitter, leak)
	}

	err := Stream(ctx, opts, handler)
	if err != nil && err != errStreamStopped {
		eev := bridge.NewEvent(bridge.EventError)
		eev.SessionID = req.SessionID
		eev.Error = b.explainStreamError(err)
		sendStreamEvent(ctx, &mu, out, eev)
	}
	// Drain any text buffered by the splitter (e.g. a dangling partial tag).
	for _, seg := range splitter.flush() {
		if !b.emitSegment(ctx, &mu, out, req.SessionID, seg) {
			break
		}
	}
	// Final leak pass: a tool call whose closing brace arrived in the very last
	// content delta is only complete now. Emit any still-pending reassembled
	// calls before we close the stream.
	for _, tc := range leak.flush() {
		if !b.emitToolCall(ctx, &mu, out, req.SessionID, tc) {
			break
		}
	}
	finish()
}

// explainStreamError turns an opaque transport failure into an actionable
// message. A bare "context deadline exceeded" tells the user nothing; this maps
// it to the real cause (the per-request timeout) and the knob to raise it.
func (b *Bridge) explainStreamError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if strings.Contains(msg, "SSE idle timeout") {
		secs := int(b.entry.StreamIdleTimeout() / time.Second)
		return fmt.Sprintf("inference stream went silent for %ds mid-response (upstream stalled; set llmBridge.providers[].streamIdleTimeoutSec to tune): %s", secs, msg)
	}
	if strings.Contains(msg, "context deadline exceeded") || strings.Contains(msg, "deadline exceeded") {
		secs := int(b.entry.RequestTimeout() / time.Second)
		return fmt.Sprintf("inference request timed out after %ds (set llmBridge.providers[].requestTimeoutSec higher for long sub-agent steps): %s", secs, msg)
	}
	return msg
}

// handleWireEvent fans a single WireEvent out into one or more StreamEvents.
// Returns false when the runtime has hung up so the wire layer should stop.
func (b *Bridge) handleWireEvent(ctx context.Context, mu *sync.Mutex, out chan<- bridge.StreamEvent, sessionID string, ev WireEvent, splitter *thinkSplitter, leak *leakScanner) bool {
	// Native reasoning (reasoning_content) is already classified as thinking.
	if ev.Thinking != "" && !b.emitThinking(ctx, mu, out, sessionID, ev.Thinking) {
		return false
	}
	// Visible content may contain inline <think> tags; classify before emit.
	if ev.Text != "" {
		for _, seg := range splitter.push(ev.Text) {
			// Strip leaked chat-template control markers (e.g. [/ask],
			// <|im_end|>) from visible content before both emit and
			// leak-scan: they corrupt inline tool-call reassembly and must
			// never reach the user. Thinking segments are passed through
			// untouched (they are not user-facing and not leak-scanned).
			if !seg.thinking {
				seg.text = leakpkg.StripTemplateLeakTokens(seg.text)
			}
			if !b.emitSegment(ctx, mu, out, sessionID, seg) {
				return false
			}
			// Feed only the visible (non-thinking) text into the leak scanner;
			// a reassembled inline tool call is emitted as a real tool call.
			if !seg.thinking {
				for _, tc := range leak.feed(seg.text) {
					if !b.emitToolCall(ctx, mu, out, sessionID, tc) {
						return false
					}
				}
			}
		}
	}
	if ev.Tool.Name != "" && !b.emitToolCall(ctx, mu, out, sessionID, ev.Tool) {
		return false
	}
	return true
}

// emitSegment routes one classified text segment to the thinking or response
// channel.
func (b *Bridge) emitSegment(ctx context.Context, mu *sync.Mutex, out chan<- bridge.StreamEvent, sessionID string, seg thinkSegment) bool {
	if seg.text == "" {
		return true
	}
	if seg.thinking {
		return b.emitThinking(ctx, mu, out, sessionID, seg.text)
	}
	return b.emitText(ctx, mu, out, sessionID, seg.text)
}

// emitThinking pushes one EventThinkingDelta frame. Extracted to keep
// handleWireEvent under the cognitive-complexity limit.
func (b *Bridge) emitThinking(ctx context.Context, mu *sync.Mutex, out chan<- bridge.StreamEvent, sessionID, delta string) bool {
	ev := bridge.NewEvent(bridge.EventThinkingDelta)
	ev.SessionID = sessionID
	ev.Delta = delta
	return sendStreamEvent(ctx, mu, out, ev)
}

// emitText pushes one EventResponseDelta frame. Inline tool-call reassembly is
// handled separately by the per-stream leakScanner (which accumulates content
// across deltas), because a single delta never holds a complete tool-call JSON
// for a large argument.
func (b *Bridge) emitText(ctx context.Context, mu *sync.Mutex, out chan<- bridge.StreamEvent, sessionID, delta string) bool {
	ev := bridge.NewEvent(bridge.EventResponseDelta)
	ev.SessionID = sessionID
	ev.Delta = delta
	return sendStreamEvent(ctx, mu, out, ev)
}

// leakScanner reassembles tool calls that a model emits inline in its content
// channel, accumulating text across streamed deltas and scanning the buffer for
// complete {"name":...,"arguments":{...}} objects. It is restricted to declared
// tool names so JSON inside file content is not misread as a call. State is the
// growing buffer plus a frontier offset past the last fully-scanned region, so
// each feed only scans the new tail (no O(n²) rescans, no duplicate emits).
type leakScanner struct {
	buf     strings.Builder
	scanned int // bytes [0:scanned) already searched for COMPLETE objects
	known   func(string) bool
}

// newLeakScanner builds a scanner that only accepts the given tool names. When
// the list is empty the scanner is disabled (nil known would accept anything,
// which risks false positives, so we require an explicit declared-tools list).
func newLeakScanner(declared []string) *leakScanner {
	if len(declared) == 0 {
		return &leakScanner{known: func(string) bool { return false }}
	}
	set := make(map[string]struct{}, len(declared))
	for _, n := range declared {
		set[n] = struct{}{}
	}
	return &leakScanner{known: func(n string) bool { _, ok := set[n]; return ok }}
}

// feed appends a content fragment and returns any tool calls that became
// complete. It advances the scan frontier past each emitted object and, when no
// object completes, up to the last point that cannot start a future match.
func (s *leakScanner) feed(text string) []parse.ToolCall {
	if text != "" {
		s.buf.WriteString(text)
	}
	var calls []parse.ToolCall
	for {
		full := s.buf.String()
		tc, next, ok := leakpkg.ParseToolCallLeakFrom(full, s.scanned, s.known)
		if !ok {
			// `next` is the scan frontier: either EOF (nothing pending) or the
			// index of a '{' that begins a still-incomplete object. Keep it so
			// the next feed resumes from there.
			s.scanned = next
			return calls
		}
		calls = append(calls, tc)
		s.scanned = next
	}
}

// flush is called once the stream ends; the final delta may have closed an
// object, so do one last scan. (feed already handles the common case.)
func (s *leakScanner) flush() []parse.ToolCall {
	return s.feed("")
}

// emitToolCall pushes one EventToolCall frame.
func (b *Bridge) emitToolCall(ctx context.Context, mu *sync.Mutex, out chan<- bridge.StreamEvent, sessionID string, tc parse.ToolCall) bool {
	ev := bridge.NewEvent(bridge.EventToolCall)
	ev.SessionID = sessionID
	ev.ToolCall = &tc
	return sendStreamEvent(ctx, mu, out, ev)
}

// sendStreamEvent blocks until the event is queued or ctx is cancelled.
// Returns false when the channel accepted nothing (i.e. the runtime has hung up).
func sendStreamEvent(ctx context.Context, mu *sync.Mutex, out chan<- bridge.StreamEvent, ev bridge.StreamEvent) bool {
	mu.Lock()
	defer mu.Unlock()
	select {
	case <-ctx.Done():
		return false
	case out <- ev:
		return true
	}
}

// token returns the bearer token from the configured env var.
func (b *Bridge) token() string {
	return strings.TrimSpace(os.Getenv(b.entry.CredentialsEnv))
}
