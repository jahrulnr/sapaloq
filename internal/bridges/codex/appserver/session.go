package appserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/parse"
)

type ToolExecutor func(context.Context, parse.ToolCall) (string, error)

type TurnRequest struct {
	SessionID    string
	ResumeThread string
	FreshPrompt  string
	ResumePrompt string
	Model        string
	Reasoning    string
	Cwd          string
	Sandbox      string
	Images       []bridge.Image
	DynamicTools []DynamicToolNamespace
	ToolExecutor ToolExecutor
}

type TurnResult struct {
	ThreadID string
	TurnID   string
	Resumed  bool
}

func RunTurn(ctx context.Context, endpoint string, req TurnRequest, out chan<- bridge.StreamEvent) (TurnResult, error) {
	mapper := NewMapper(req.SessionID)
	handler := func(callCtx context.Context, serverReq ServerRequest) (any, error) {
		return handleServerRequest(callCtx, req.ToolExecutor, mapper, serverReq, out)
	}
	c, err := Dial(ctx, endpoint, handler)
	if err != nil {
		return TurnResult{}, err
	}
	defer c.Close()
	if err := c.Initialize(ctx); err != nil {
		return TurnResult{}, err
	}

	result := TurnResult{}
	prompt := req.FreshPrompt
	var resumeErr error
	if req.ResumeThread != "" {
		var resumed threadResponse
		resumeErr = c.Call(ctx, "thread/resume", map[string]any{
			"threadId":       req.ResumeThread,
			"model":          nilIfEmpty(req.Model),
			"cwd":            nilIfEmpty(req.Cwd),
			"approvalPolicy": "never",
			"sandbox":        req.Sandbox,
		}, &resumed)
		if resumeErr == nil {
			result.ThreadID = resumed.Thread.ID
			result.Resumed = true
			prompt = req.ResumePrompt
		}
	}
	if result.ThreadID == "" {
		params := map[string]any{
			"model":          nilIfEmpty(req.Model),
			"cwd":            req.Cwd,
			"approvalPolicy": "never",
			"sandbox":        req.Sandbox,
		}
		if len(req.DynamicTools) > 0 {
			params["dynamicTools"] = req.DynamicTools
		}
		var started threadResponse
		if err := c.Call(ctx, "thread/start", params, &started); err != nil {
			if resumeErr != nil {
				return TurnResult{}, fmt.Errorf("resume failed (%v), then fresh thread/start failed: %w", resumeErr, err)
			}
			return TurnResult{}, fmt.Errorf("thread/start: %w", err)
		}
		result.ThreadID = started.Thread.ID
	}
	if result.ThreadID == "" {
		return TurnResult{}, fmt.Errorf("thread lifecycle returned an empty thread id")
	}

	input, err := buildInput(prompt, req.Images)
	if err != nil {
		return TurnResult{}, err
	}
	turnParams := map[string]any{
		"threadId": result.ThreadID,
		"input":    input,
		"model":    nilIfEmpty(req.Model),
		"effort":   nilIfEmpty(req.Reasoning),
	}
	var started turnStartResponse
	if err := c.Call(ctx, "turn/start", turnParams, &started); err != nil {
		return TurnResult{}, fmt.Errorf("turn/start: %w", err)
	}
	result.TurnID = started.Turn.ID

	lastError := ""
	for {
		select {
		case <-ctx.Done():
			interruptCtx, cancel := context.WithTimeout(context.Background(), interruptTimeout)
			_ = c.Call(interruptCtx, "turn/interrupt", map[string]any{"threadId": result.ThreadID, "turnId": result.TurnID}, nil)
			cancel()
			return result, ctx.Err()
		case <-c.Done():
			return result, c.Err()
		case n, ok := <-c.Notifications():
			if !ok {
				return result, c.Err()
			}
			switch n.Method {
			case "error":
				var p errorNotificationParams
				if json.Unmarshal(n.Params, &p) == nil && !p.WillRetry {
					lastError = p.Error.Message
				}
				continue
			case "turn/completed":
				var p turnCompletedParams
				if json.Unmarshal(n.Params, &p) != nil || p.ThreadID != result.ThreadID || (result.TurnID != "" && p.Turn.ID != result.TurnID) {
					continue
				}
				if p.Turn.Status == "failed" || lastError != "" {
					if p.Turn.Error != nil && p.Turn.Error.Message != "" {
						lastError = p.Turn.Error.Message
					}
					if lastError == "" {
						lastError = "codex turn failed"
					}
					sendEvent(ctx, out, errorEvent(req.SessionID, lastError))
					return result, nil
				}
				sendEvent(ctx, out, doneEvent(req.SessionID))
				return result, nil
			}
			for _, ev := range mapper.Map(n) {
				if !sendEvent(ctx, out, ev) {
					return result, ctx.Err()
				}
			}
		}
	}
}

const interruptTimeout = 2 * time.Second

func handleServerRequest(ctx context.Context, executor ToolExecutor, mapper *Mapper, req ServerRequest, out chan<- bridge.StreamEvent) (any, error) {
	switch req.Method {
	case "item/tool/call":
		var call DynamicToolCallParams
		if err := json.Unmarshal(req.Params, &call); err != nil {
			return nil, fmt.Errorf("decode dynamic tool call: %w", err)
		}
		if executor == nil {
			return DynamicToolCallResponse{ContentItems: []DynamicToolContentItem{{Type: "inputText", Text: "tool executor unavailable"}}, Success: false}, nil
		}
		if !sendEvent(ctx, out, mapper.DynamicToolCall(call)) {
			return nil, ctx.Err()
		}
		name := call.Tool
		if call.Namespace != "" && call.Namespace != "sapaloq" {
			name = call.Namespace + "." + call.Tool
		}
		text, err := executor(ctx, parse.ToolCall{ID: call.CallID, Name: name, Arguments: call.Arguments, Source: "codex"})
		if err != nil {
			return DynamicToolCallResponse{ContentItems: []DynamicToolContentItem{{Type: "inputText", Text: err.Error()}}, Success: false}, nil
		}
		return DynamicToolCallResponse{ContentItems: []DynamicToolContentItem{{Type: "inputText", Text: text}}, Success: true}, nil
	case "item/commandExecution/requestApproval":
		return map[string]any{"decision": "accept"}, nil
	case "item/fileChange/requestApproval":
		return map[string]any{"decision": "accept"}, nil
	case "item/tool/requestUserInput":
		return map[string]any{"answers": map[string]any{}}, nil
	case "mcpServer/elicitation/request":
		return map[string]any{"action": "decline"}, nil
	case "item/permissions/requestApproval":
		return map[string]any{"decision": "decline"}, nil
	default:
		return nil, fmt.Errorf("unsupported server request %s", req.Method)
	}
}

func buildInput(prompt string, images []bridge.Image) ([]map[string]any, error) {
	input := []map[string]any{{"type": "text", "text": prompt, "text_elements": []any{}}}
	for _, image := range images {
		uri := strings.TrimSpace(image.DataURI)
		if uri == "" && len(image.Data) > 0 {
			mime := image.MimeType
			if mime == "" {
				mime = "image/png"
			}
			uri = "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(image.Data)
		}
		if uri == "" {
			continue
		}
		if !strings.HasPrefix(uri, "data:image/") {
			return nil, fmt.Errorf("invalid image data URI")
		}
		input = append(input, map[string]any{"type": "image", "image_url": uri})
	}
	return input, nil
}

func nilIfEmpty(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

func sendEvent(ctx context.Context, out chan<- bridge.StreamEvent, ev bridge.StreamEvent) bool {
	select {
	case <-ctx.Done():
		return false
	case out <- ev:
		return true
	}
}

func doneEvent(sessionID string) bridge.StreamEvent {
	ev := bridge.NewEvent(bridge.EventDone)
	ev.SessionID = sessionID
	return ev
}

func errorEvent(sessionID, message string) bridge.StreamEvent {
	ev := bridge.NewEvent(bridge.EventError)
	ev.SessionID = sessionID
	ev.Error = message
	return ev
}
