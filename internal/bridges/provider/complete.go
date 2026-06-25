package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/jahrulnr/sapaloq/internal/debug"
	"github.com/jahrulnr/sapaloq/internal/parse"
)

// complete is the non-stream counterpart to Stream's SSE path. It sends a
// single request (stream:false in the body) and parses one complete response
// into WireEvents, dispatched through the same WireHandler the streaming path
// uses. The bridge above is agnostic: a non-stream turn surfaces as one batch
// of WireEvents (thinking, then text, then any tool calls) followed by done,
// exactly the StreamEvent contract the orchestrator already consumes.
//
// Unlike the streaming path, the ENTIRE call is pre-stream: nothing has been
// dispatched until the body is fully read and parsed, so a transient failure
// at any point is safe to retry (no risk of duplicated output). We reuse the
// same backoff/jitter retry budget as runSSE.
func complete(ctx context.Context, opts WireOptions, on WireHandler) error {
	body, err := buildRequestBody(opts)
	if err != nil {
		return err
	}
	raw, err := postOnce(ctx, opts, body)
	if err != nil {
		return err
	}
	events, err := parseCompleteResponse(opts.Parser, raw)
	if err != nil {
		return err
	}
	for _, ev := range events {
		if !on(ev) {
			return errStreamStopped
		}
	}
	return nil
}

// buildRequestBody assembles the request body for the active parser. It is the
// non-stream sibling of the streamX dispatch in wire.go and reuses the exact
// same builders (which now honour opts.Stream), so the only difference from the
// streaming body is the stream:false flag.
func buildRequestBody(opts WireOptions) ([]byte, error) {
	switch opts.Parser {
	case ParserClaude:
		return buildClaudeRequestBody(opts, "claude-sonnet-4-5")
	case ParserKimi:
		return buildKimiRequestBody(opts, "kimi-k2.6")
	default:
		return buildOpenAIRequestBody(opts, "gpt-4o-mini")
	}
}

// postOnce POSTs the body and returns the full response body. A transient
// failure (connection error, or a retryable status: 408, 429, 5xx) is retried
// up to opts.MaxRetries times with exponential backoff + jitter - identical to
// runSSE, but because the non-stream path dispatches nothing until the response
// is parsed, retries are unconditionally safe here.
func postOnce(ctx context.Context, opts WireOptions, body []byte) ([]byte, error) {
	attempts := opts.MaxRetries + 1
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			delay := retryBackoff(attempt)
			debug.Debugf("provider-bridge(non-stream): retry %d/%d after %v (last error: %v)",
				attempt, attempts-1, delay, lastErr)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}
		raw, err := attemptPost(ctx, opts, body)
		if err == nil {
			return raw, nil
		}
		var retryable retryableError
		if !errors.As(err, &retryable) {
			return nil, err
		}
		lastErr = retryable.err
	}
	return nil, lastErr
}

// attemptPost performs a single non-stream POST and returns the response body.
// Pre-stream failures are wrapped in retryableError so postOnce can retry them;
// a definitive 4xx (except 408/429) is returned bare.
func attemptPost(ctx context.Context, opts WireOptions, body []byte) ([]byte, error) {
	req, cancel, err := buildHTTPRequest(ctx, opts, body)
	if err != nil {
		return nil, err
	}
	defer cancel()
	// A non-stream response is a single JSON document, not an event stream.
	req.Header.Set("Accept", "application/json")
	debug.Debugf("provider-bridge(non-stream): POST %s parser=%s auth=%s model=%s messages=%d bytes=%d",
		opts.Endpoint, opts.Parser, opts.Auth, opts.Model, len(opts.Messages), len(body))

	resp, err := streamHTTPClient(opts.IdleTimeout).Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, err // caller-driven cancellation: do not retry.
		}
		return nil, retryableError{err: err}
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		statusErr := fmt.Errorf("provider-bridge: upstream status %d: %s", resp.StatusCode, upstreamErrorBody(raw))
		if isRetryableStatus(resp.StatusCode) {
			return nil, retryableError{err: statusErr}
		}
		return nil, statusErr
	}
	if readErr != nil {
		// The connection accepted the request and returned 200, then failed
		// mid-body. Nothing was dispatched, so this is still safe to retry.
		return nil, retryableError{err: fmt.Errorf("provider-bridge: reading response body: %w", readErr)}
	}
	return raw, nil
}

// parseCompleteResponse turns one complete (non-stream) response document into
// the ordered WireEvents the bridge expects: thinking first, then visible text,
// then any tool calls - matching the order the streaming handlers emit.
func parseCompleteResponse(parser ParserKind, raw []byte) ([]WireEvent, error) {
	switch parser {
	case ParserClaude:
		return parseClaudeComplete(raw)
	default:
		// openai + kimi share the Chat Completions response shape.
		return parseOpenAIComplete(raw)
	}
}

// openAIResponse is the non-stream Chat Completions response (also used by Kimi
// and OpenAI-compatible gateways). Only the fields the bridge needs are
// modelled; unknown fields are ignored.
type openAIResponse struct {
	Choices []struct {
		Message struct {
			Content   string          `json:"content"`
			Reasoning string          `json:"reasoning_content,omitempty"`
			ToolCalls []openAIToolDel `json:"tool_calls,omitempty"`
		} `json:"message"`
		FinishReason string `json:"finish_reason,omitempty"`
	} `json:"choices"`
}

// parseOpenAIComplete extracts thinking, text, and tool calls from a complete
// Chat Completions response. tool_calls arguments arrive whole (not split
// across deltas), so each is emitted as a single tool event.
func parseOpenAIComplete(raw []byte) ([]WireEvent, error) {
	var resp openAIResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("provider-bridge: decoding non-stream response: %w", err)
	}
	var events []WireEvent
	for _, choice := range resp.Choices {
		msg := choice.Message
		if msg.Reasoning != "" {
			events = append(events, WireEvent{Thinking: msg.Reasoning})
		}
		if msg.Content != "" {
			events = append(events, WireEvent{Text: msg.Content})
		}
		for _, tc := range msg.ToolCalls {
			if tc.Function.Name == "" {
				continue
			}
			args := json.RawMessage(tc.Function.Arguments)
			if len(args) == 0 {
				args = json.RawMessage("{}")
			}
			call := parse.NewToolCall(tc.Function.Name, args, "openai_complete")
			call.ID = tc.ID
			events = append(events, WireEvent{Tool: call})
		}
	}
	return events, nil
}

// claudeResponse is the non-stream Anthropic Messages response. content is a
// list of typed blocks (text | thinking | tool_use); we surface each in order.
type claudeResponse struct {
	Content []struct {
		Type     string          `json:"type"`
		Text     string          `json:"text,omitempty"`
		Thinking string          `json:"thinking,omitempty"`
		ID       string          `json:"id,omitempty"`
		Name     string          `json:"name,omitempty"`
		Input    json.RawMessage `json:"input,omitempty"`
	} `json:"content"`
	StopReason string `json:"stop_reason,omitempty"`
}

// parseClaudeComplete extracts thinking, text, and tool_use blocks from a
// complete Anthropic Messages response, preserving the document order.
func parseClaudeComplete(raw []byte) ([]WireEvent, error) {
	var resp claudeResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("provider-bridge: decoding non-stream claude response: %w", err)
	}
	var events []WireEvent
	for _, block := range resp.Content {
		switch block.Type {
		case "thinking", "redacted_thinking":
			if block.Thinking != "" {
				events = append(events, WireEvent{Thinking: block.Thinking})
			}
		case "text":
			if block.Text != "" {
				events = append(events, WireEvent{Text: block.Text})
			}
		case "tool_use":
			if block.Name == "" {
				continue
			}
			args := block.Input
			if len(args) == 0 {
				args = json.RawMessage("{}")
			}
			call := parse.NewToolCall(block.Name, args, "claude_complete")
			call.ID = block.ID
			events = append(events, WireEvent{Tool: call})
		}
	}
	return events, nil
}
