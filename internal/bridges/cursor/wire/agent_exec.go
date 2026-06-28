package wire

import "context"

// AgentMCPExecutor runs one MCP tool request from the Agent API exec channel.
type AgentMCPExecutor func(ctx context.Context, toolName, toolCallID string, args map[string]any) (content string, isError bool, err error)

// AgentMCPTelemetry fires before an MCP tool executes (UI telemetry hook).
type AgentMCPTelemetry func(toolName, toolCallID string, args map[string]any)

func (s *agentStreamState) handleExecEvent(ctx context.Context, ev ExecServerEvent) error {
	key := execDedupKey(ev)
	if _, seen := s.ackedExec[key]; seen {
		return nil
	}
	s.ackedExec[key] = struct{}{}

	switch ev.Kind {
	case "exec_request_context":
		return s.writeFrame(BuildRequestContextResponse(ev.ExecMsgID, ev.ExecID, s.tools))
	case "exec_mcp":
		return s.handleExecMCP(ctx, ev)
	default:
		frame, ok := BuildExecRejection(ev)
		if !ok || len(frame) == 0 {
			return nil
		}
		return s.writeFrame(frame)
	}
}

func (s *agentStreamState) handleExecMCP(ctx context.Context, ev ExecServerEvent) error {
	if s.pauseIdle != nil {
		s.pauseIdle()
		defer func() {
			if s.resumeIdle != nil {
				s.resumeIdle()
			}
		}()
	}
	if s.onMCPTool != nil {
		s.onMCPTool(ev.ToolName, ev.ToolCallID, ev.Args)
	}
	if s.mcpExecutor == nil {
		return s.writeFrame(BuildExecMCPError(ev.ExecMsgID, ev.ExecID, "tool executor unavailable"))
	}
	content, isError, err := s.mcpExecutor(ctx, ev.ToolName, ev.ToolCallID, ev.Args)
	if err != nil {
		return s.writeFrame(BuildExecMCPError(ev.ExecMsgID, ev.ExecID, err.Error()))
	}
	return s.writeFrame(BuildExecMCPResult(ev.ExecMsgID, ev.ExecID, content, isError))
}
