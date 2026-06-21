// Package provider is a multi-model LLM bridge that speaks OpenAI Chat
// Completions, Anthropic Messages, and Kimi (Moonshot) — selected automatically
// from config + endpoint URL. The wire layer is the same regardless of
// parser: HTTP/POST + Server-Sent Events; only the request body shape and
// per-line event format differ.
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
	token := b.token()
	if token == "" {
		errEv := bridge.NewEvent(bridge.EventError)
		errEv.SessionID = req.SessionID
		errEv.Error = fmt.Sprintf("provider-bridge: token env %s is empty", b.entry.CredentialsEnv)
		go func() {
			defer close(out)
			out <- errEv
		}()
		return out, nil
	}

	opts := b.buildWireOptions(req)
	debug.Debugf("provider-bridge: complete session=%s parser=%s auth=%s model=%s endpoint=%s",
		req.SessionID, opts.Parser, opts.Auth, req.Model, opts.Endpoint)

	go b.runStream(ctx, opts, req, out)
	return out, nil
}

// buildWireOptions translates the active entry + request into a WireOptions
// struct. Pulled out of Complete to keep the hot path readable.
func (b *Bridge) buildWireOptions(req bridge.Request) WireOptions {
	parser := DetectParser(b.entry)
	auth := DetectAuthScheme(b.entry, parser)
	apiVersion := DetectAPIVersion(b.entry)
	reasoning := DetectReasoningEffort(b.entry)
	maxTokens := DetectMaxTokens(b.entry)
	contextWindow := DetectContextWindow(b.entry)
	// Apply the context window to the messages we forward to the model.
	messages := FitMessagesToContext(req.Messages, contextWindow)
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
	}
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
	handler := func(ev WireEvent) bool {
		return b.handleWireEvent(ctx, &mu, out, req.SessionID, ev, &splitter)
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
func (b *Bridge) handleWireEvent(ctx context.Context, mu *sync.Mutex, out chan<- bridge.StreamEvent, sessionID string, ev WireEvent, splitter *thinkSplitter) bool {
	// Native reasoning (reasoning_content) is already classified as thinking.
	if ev.Thinking != "" && !b.emitThinking(ctx, mu, out, sessionID, ev.Thinking) {
		return false
	}
	// Visible content may contain inline <think> tags; classify before emit.
	if ev.Text != "" {
		for _, seg := range splitter.push(ev.Text) {
			if !b.emitSegment(ctx, mu, out, sessionID, seg) {
				return false
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

// emitText pushes one EventResponseDelta frame and surfaces any inline JSON
// tool call as EventToolLeak.
func (b *Bridge) emitText(ctx context.Context, mu *sync.Mutex, out chan<- bridge.StreamEvent, sessionID, delta string) bool {
	ev := bridge.NewEvent(bridge.EventResponseDelta)
	ev.SessionID = sessionID
	ev.Delta = delta
	if !sendStreamEvent(ctx, mu, out, ev) {
		return false
	}
	if tc, ok := leakpkg.ParseToolCallLeak(delta); ok {
		lev := bridge.NewEvent(bridge.EventToolLeak)
		lev.SessionID = sessionID
		lev.Leak = delta
		lev.ToolCall = &tc
		return sendStreamEvent(ctx, mu, out, lev)
	}
	return true
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
