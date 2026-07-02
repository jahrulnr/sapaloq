package gemini

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
	"github.com/jahrulnr/sapaloq/internal/config"
)

type wireOptions struct {
	URL         string
	Token       string
	Body        []byte
	Stream      bool
	Timeout     time.Duration
	IdleTimeout time.Duration
}

func completeOnce(ctx context.Context, opts wireOptions) (turnAccum, error) {
	reqCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, opts.URL, bytes.NewReader(opts.Body))
	if err != nil {
		return turnAccum{}, err
	}
	req.Header.Set("X-goog-api-key", opts.Token)
	req.Header.Set("Content-Type", "application/json")
	if opts.Stream {
		req.Header.Set("Accept", "text/event-stream")
	} else {
		req.Header.Set("Accept", "application/json")
	}
	req.Header.Set("User-Agent", "SapaLOQ/1.0 (+https://github.com/jahrulnr/sapaloq)")

	client := &http.Client{Timeout: opts.Timeout}
	resp, err := client.Do(req)
	if err != nil {
		return turnAccum{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		rawBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return turnAccum{}, fmt.Errorf("upstream status %d: %s", resp.StatusCode, strings.TrimSpace(string(rawBody)))
	}
	if opts.Stream {
		return readSSE(ctx, resp.Body, opts.IdleTimeout)
	}
	return readJSON(ctx, resp.Body)
}

func completeWithFallbacks(ctx context.Context, entry config.LLMBridge, messages []bridge.Message, declaredTools []string, stream bool) (turnAccum, error) {
	base := normalizeAPIBase(entry.Endpoint)
	url := generateURL(base, entry.Model, stream)
	token := resolveAPIKey(entry)
	if token == "" {
		return turnAccum{}, fmt.Errorf("gemini-bridge: API key is empty")
	}
	timeout := entry.RequestTimeout()
	idle := entry.StreamIdleTimeout()

	opts := requestOptions{withToolChoice: len(declaredTools) > 0, withReasoning: true}
	for attempt := 0; attempt < 4; attempt++ {
		body, err := buildRequestBody(entry, messages, declaredTools, opts)
		if err != nil {
			return turnAccum{}, err
		}
		turn, err := completeOnce(ctx, wireOptions{
			URL: url, Token: token, Body: body, Stream: stream,
			Timeout: timeout, IdleTimeout: idle,
		})
		if err == nil {
			return turn, nil
		}
		if opts.withReasoning && isReasoningRejected(err) {
			opts.withReasoning = false
			continue
		}
		if opts.withToolChoice && isToolChoiceRejected(err) {
			opts.withToolChoice = false
			continue
		}
		return turnAccum{}, err
	}
	return turnAccum{}, fmt.Errorf("gemini-bridge: probe retry budget exceeded")
}

func readJSON(ctx context.Context, r io.Reader) (turnAccum, error) {
	rawBody, err := io.ReadAll(io.LimitReader(r, 32*1024*1024))
	if err != nil {
		return turnAccum{}, err
	}
	select {
	case <-ctx.Done():
		return turnAccum{}, ctx.Err()
	default:
	}
	var resp response
	if err := json.Unmarshal(rawBody, &resp); err != nil {
		return turnAccum{}, fmt.Errorf("decode json response: %w", err)
	}
	if resp.Error != nil && resp.Error.Message != "" {
		return turnAccum{}, fmt.Errorf("response error: %s", resp.Error.Message)
	}
	var out turnAccum
	mergeResponse(&out, resp)
	return out, nil
}

func readSSE(ctx context.Context, r io.Reader, idleTimeout time.Duration) (turnAccum, error) {
	var out turnAccum
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	for sc.Scan() {
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		default:
		}
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		if !bytes.HasPrefix(line, []byte("data: ")) {
			continue
		}
		data := bytes.TrimPrefix(line, []byte("data: "))
		if !json.Valid(data) {
			continue
		}
		var chunk response
		if err := json.Unmarshal(data, &chunk); err != nil {
			continue
		}
		if chunk.Error != nil && chunk.Error.Message != "" {
			return out, fmt.Errorf("stream error: %s", chunk.Error.Message)
		}
		mergeResponse(&out, chunk)
	}
	if err := sc.Err(); err != nil {
		return out, err
	}
	_ = idleTimeout
	return out, nil
}

func idleTimeoutOrDefault(d time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return 120 * time.Second
}
