package cursor

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	cursoragent "github.com/jahrulnr/sapaloq/internal/bridges/cursor/agent"
	"github.com/jahrulnr/sapaloq/internal/bridges/cursor/credentials"
	"github.com/jahrulnr/sapaloq/internal/bridges/cursor/wire"
	"github.com/jahrulnr/sapaloq/internal/debug"
	"github.com/jahrulnr/sapaloq/internal/parse"
)

// wantsAgentPath decides whether this request should go through the
// agent.v1.AgentService/Run RPC (api5) instead of the legacy chat stream
// (api2 StreamUnifiedChatWithTools).
func (b *Bridge) wantsAgentPath(req bridge.Request) bool {
	if b.entry.UseAgentPath {
		return true
	}
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

// streamLiveAgent routes the request through the Agent API path (cursor-agent
// wire port). It encodes messages into agent.v1.RunRequest, drives the
// bidirectional exec/KV handshake, and maps InteractionUpdate events to
// bridge.StreamEvent.
func (b *Bridge) streamLiveAgent(ctx context.Context, req bridge.Request, creds credentials.Credentials, out chan<- bridge.StreamEvent) {
	host := strings.TrimSpace(os.Getenv("CURSOR_AGENT_HOST"))
	if host == "" {
		host = wire.AgentHost(creds.GhostMode)
	}
	path := strings.TrimSpace(os.Getenv("CURSOR_AGENT_PATH"))
	if path == "" {
		path = wire.AgentAgentPath
	}
	declared := declaredToolsForRequest(req.DeclaredTools, b.entry.DeclaredTools)
	agentTools := buildAgentTools(declared)
	debug.Debugf("cursor-bridge: agent path host=%s ghost=%v tools=%d", host, creds.GhostMode, len(agentTools))

	body := wire.BuildAgentRequestBody(wire.AgentRunOptions{
		UserText:       flattenMessages(req.Messages),
		ModelID:        defaultIfEmpty(req.Model, b.entry.Model),
		ConversationID: req.SessionID,
		Tools:          agentTools,
		Images:         encodeImages(req.Images),
	})

	mapper := cursoragent.NewMapper(req.SessionID)
	var frameCount int
	streamFn := wire.SelectAgentStreamFn()
	err := streamFn(ctx, wire.AgentStreamOptions{
		Host:      host,
		Path:      path,
		Token:     creds.AccessToken,
		MachineID: creds.MachineID,
		GhostMode: creds.GhostMode,
		Tools:     agentTools,
		Body:      body,
		OnMCPTool: func(toolName, toolCallID string, args map[string]any) {
			argsJSON, _ := json.Marshal(args)
			resolved := ResolveToolCall(b.schema, parse.ToolCall{
				ID:        toolCallID,
				Name:      toolName,
				Arguments: argsJSON,
				Source:    "cursor",
			})
			ev := bridge.NewEvent(bridge.EventToolCall)
			ev.SessionID = req.SessionID
			ev.ToolCall = &resolved
			send(ctx, out, ev)
		},
		MCPExecutor: func(callCtx context.Context, toolName, toolCallID string, args map[string]any) (string, bool, error) {
			if req.ToolExecutor == nil {
				return "", true, nil
			}
			argsJSON, err := json.Marshal(args)
			if err != nil {
				return "", true, err
			}
			resolved := ResolveToolCall(b.schema, parse.ToolCall{
				ID:        toolCallID,
				Name:      toolName,
				Arguments: argsJSON,
				Source:    "cursor",
			})
			text, err := req.ToolExecutor(callCtx, resolved)
			if err != nil {
				return err.Error(), true, nil
			}
			return text, false, nil
		},
		InsecureTLS: os.Getenv("SAPALOQ_WIRE_INSECURE_TLS") == "1",
		Timeout:     b.timeout,
	}, func(decoded []wire.AgentDecoded, _ []byte) {
		frameCount++
		for _, ev := range mapper.Map(decoded) {
			send(ctx, out, ev)
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
	debug.Debugf("cursor-bridge: agent stream done frames=%d", frameCount)
	done := bridge.NewEvent(bridge.EventDone)
	done.SessionID = req.SessionID
	send(ctx, out, done)
}

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

func encodeImages(images []bridge.Image) []wire.AgentImage {
	var out []wire.AgentImage
	for _, img := range images {
		decoded, mime, ok := decodeDataURI(img.DataURI)
		if !ok {
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

func decodeDataURI(s string) ([]byte, string, bool) {
	if !strings.HasPrefix(s, "data:") {
		return nil, "", false
	}
	comma := strings.IndexByte(s, ',')
	if comma < 0 {
		return nil, "", false
	}
	header := s[5:comma]
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
