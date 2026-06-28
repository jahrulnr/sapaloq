package cursor

import (
	"context"
	"encoding/base64"
	"os"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/bridges/cursor/credentials"
	"github.com/jahrulnr/sapaloq/internal/bridges/cursor/wire"
	"github.com/jahrulnr/sapaloq/internal/debug"
)

// wantsAgentPath decides whether this request should go through the
// agent.v1.AgentService/Run RPC (api5) instead of the legacy chat stream
// (api2 StreamUnifiedChatWithTools).
//
// Agent path is used for vision (images in messages). Chat stream uses the
// Node wire driver when available (see bridge.go streamLive).
func wantsAgentPath(req bridge.Request) bool {
	if len(req.Images) > 0 {
		return true
	}
	if messageHasVisionSignal(req.Messages) {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("SAPALOQ_AGENT_PATH")), "1") {
		return true
	}
	return false
}

func messageHasVisionSignal(messages []bridge.Message) bool {
	for _, m := range messages {
		if strings.Contains(m.Content, "data:image/") {
			return true
		}
		if imageURLRe.MatchString(m.Content) {
			return true
		}
	}
	return false
}

var imageURLRe = regexp.MustCompile(`https?://[^\s"']+\.(?:png|jpg|jpeg|gif|webp)(?:\?[^\s"']*)?`)

// streamLiveAgent routes the request through the Agent API path. It encodes
// the OpenAI-style messages into an AgentClientMessage.RunRequest protobuf and
// streams the AgentServerMessage response back as bridge events.
//
// The Agent host is selected by creds.GhostMode - privacy mode routes
// through `agent.global.api5.cursor.sh`, non-privacy through
// `agentn.global.api5.cursor.sh` (mirrors 9router's
// src/lib/oauth/constants/oauth.js). Operators can override either with the
// CURSOR_AGENT_HOST env var (for testing against mocks or alternate
// deployments).
func (b *Bridge) streamLiveAgent(ctx context.Context, req bridge.Request, creds credentials.Credentials, out chan<- bridge.StreamEvent) {
	host := strings.TrimSpace(os.Getenv("CURSOR_AGENT_HOST"))
	if host == "" {
		host = wire.AgentHost(creds.GhostMode)
	}
	path := strings.TrimSpace(os.Getenv("CURSOR_AGENT_PATH"))
	if path == "" {
		path = wire.AgentAgentPath
	}
	debug.Debugf("cursor-bridge: using agent API path (host=%s path=%s ghost=%v)", host, path, creds.GhostMode)

	body := wire.BuildAgentRequestBody(wire.AgentRunOptions{
		UserText:       flattenMessages(req.Messages),
		ModelID:        defaultIfEmpty(req.Model, b.entry.Model),
		ConversationID: req.SessionID,
		Images:         encodeImages(req.Images),
	})
	debug.Debugf("cursor-bridge: agent body bytes=%d", len(body))

	var frameCount int
	var responseBuf strings.Builder
	streamFn := wire.SelectAgentStreamFn()
	err := streamFn(ctx, wire.AgentStreamOptions{
		Host:        host,
		Path:        path,
		Token:       creds.AccessToken,
		Body:        body,
		InsecureTLS: os.Getenv("SAPALOQ_WIRE_INSECURE_TLS") == "1",
		Timeout:     b.timeout,
	}, func(decoded []wire.AgentDecoded, raw []byte) {
		frameCount++
		_ = raw // exposed for future exec-handshake use
		for _, d := range decoded {
			switch d.Kind {
			case "text":
				responseBuf.WriteString(d.Text)
				ev := bridge.NewEvent(bridge.EventResponseDelta)
				ev.SessionID = req.SessionID
				ev.Delta = d.Text
				send(ctx, out, ev)
			case "thinking":
				ev := bridge.NewEvent(bridge.EventThinkingDelta)
				ev.SessionID = req.SessionID
				ev.Delta = d.Thinking
				send(ctx, out, ev)
			case "turn_ended":
				return
			}
		}
	})
	if err != nil {
		debug.Debugf("cursor-bridge: agent stream error: %v", err)
		errEv := bridge.NewEvent(bridge.EventError)
		errEv.SessionID = req.SessionID
		errEv.Error = b.explainStreamError(err)
		send(ctx, out, errEv)
		return
	}
	debug.Debugf("cursor-bridge: agent stream done frames=%d response_bytes=%d", frameCount, responseBuf.Len())
	done := bridge.NewEvent(bridge.EventDone)
	done.SessionID = req.SessionID
	send(ctx, out, done)
}

// flattenMessages is a tiny placeholder - full Phase-6 implementation lives in
// proto_agent.go's flattenMessages-equivalent. For now we just join user
// messages with a newline.
func flattenMessages(messages []bridge.Message) string {
	var parts []string
	for _, m := range messages {
		if m.Role == "system" {
			continue
		}
		parts = append(parts, m.Content)
	}
	return strings.Join(parts, "\n\n")
}

// encodeImages converts base64 data URIs into wire.AgentImage. Used by the
// bridge layer when the caller hands us pre-decoded image bytes.
func encodeImages(images []bridge.Image) []wire.AgentImage {
	var out []wire.AgentImage
	for _, img := range images {
		decoded, mime, ok := decodeDataURI(img.DataURI)
		if !ok {
			// Caller passed raw bytes; mime is set explicitly.
			out = append(out, wire.AgentImage{
				UUID:     uuid.NewString(),
				MimeType: img.MimeType,
				Width:    img.Width,
				Height:   img.Height,
				Data:     img.Data,
			})
			continue
		}
		if mime == "" {
			mime = img.MimeType
		}
		out = append(out, wire.AgentImage{
			UUID:     uuid.NewString(),
			MimeType: mime,
			Width:    img.Width,
			Height:   img.Height,
			Data:     decoded,
		})
	}
	return out
}

// decodeDataURI splits a data URI of the form data:image/png;base64,XXX.
func decodeDataURI(s string) ([]byte, string, bool) {
	if !strings.HasPrefix(s, "data:") {
		return nil, "", false
	}
	comma := strings.IndexByte(s, ',')
	if comma < 0 {
		return nil, "", false
	}
	header := s[5:comma] // skip "data:"
	payload := s[comma+1:]
	mime := header
	if semi := strings.IndexByte(header, ';'); semi >= 0 {
		mime = header[:semi]
	}
	var data []byte
	if strings.Contains(header, ";base64") {
		raw, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return nil, "", false
		}
		data = raw
	} else {
		data = []byte(payload)
	}
	return data, mime, true
}
