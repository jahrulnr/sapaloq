# Cursor Agent API Contract

> Wire contract for the cursor-agent port in `internal/bridges/cursor/wire`.
> Last updated: 2026-06-29 (causal durable-turn ordering ownership)

See [BOUNDARIES.md](./BOUNDARIES.md) for simplification sequence before more orchestrator guards.

## Transport

`cursor-bridge` does **not** spawn `cursor agent` per turn. It speaks the same
Connect-RPC protocol the CLI uses internally:

| Item | Value |
|------|-------|
| RPC | `agent.v1.AgentService/Run` |
| Host (default) | `agentn.global.api5.cursor.sh` |
| Host (privacy) | `agent.global.api5.cursor.sh` when ghost mode on |
| Headers | `x-cursor-client-type: cli` via `wire.BuildAgentHeaders` |
| Driver | Default: `wire.StreamAgentNode` — Node `cursor-agent-h2-gateway.mjs` (HTTP/2 transport only); Go owns headers, protobuf, exec/MCP. Override: `SAPALOQ_AGENT_WIRE_DRIVER=raw|http2|node`. |

Enable on a provider entry: `"useAgentPath": true` or `SAPALOQ_AGENT_PATH=1`.

## Turn sequence

1. Client sends one framed `AgentClientMessage.run_request`.
2. Server streams `AgentServerMessage` frames until `turn_ended`.
3. Client must answer exec/KV sub-channels on the **same** upload half:

`conversation_id` is scoped per chat **generation** (`sessionID:runSeq`), not the bare session id, so each user send starts a fresh provider conversation aligned with SapaLOQ's active (possibly compacted) turns. `user_text` is built by `bridge.ComposeAgentUserText`: first inference includes `[system]` + `[conversation]` + `[user]`; tool continuations send only the tail since the last assistant turn (no duplicate flatten of full history).

| Server frame | Client reply |
|--------------|--------------|
| `exec_request_context` | `request_context_result` + declared MCP tools |
| `exec_mcp` | `mcp_result` (success or error) via `ToolExecutor` |
| `exec_read/write/shell/…` | typed rejection (built-ins not run in-bridge) |
| `kv_get_blob` / `kv_set_blob` | blob result / ack |

Dedup key: `{kind}:{exec_id}:{exec_msg_id}`.

## Event mapping

`internal/bridges/cursor/agent/mapper.go` maps `InteractionUpdate` decode output:

| Decoded kind | `bridge.StreamEvent` |
|--------------|----------------------|
| `text` | `response_delta` |
| `thinking` | `thinking_delta` |
| `tool_call_started/completed` | `status` telemetry |
| `turn_ended` | stream driver emits `done` |

Cursor/provider event timestamps remain transport chronology. Durable ordering
is owned by `runTurnLoop`, which appends thinking/assistant before the tool or
autopilot continuation; cold compatibility ordering is owned by
`session_transcript.go`. Tool events anchor to the matching persisted
`[Called tools: …]` marker. The bridge must not group or persist rows by role.

## MCP tool ownership

Declared SapaLOQ tools (`DeclaredTools` + registered schemas) are sent in the
request-context handshake. On `exec_mcp`:

1. Bridge emits `EventToolCall` with `Source:"cursor"` (UI telemetry).
2. Bridge calls `bridge.Request.ToolExecutor` once inside the api5 turn.
3. Orchestrator treats `Source:"cursor"` like `Source:"codex"` — no second dispatch.

**Boundary note:** this split is the main source of loop/stop/transcript bugs.
Phase 2 in [BOUNDARIES.md](./BOUNDARIES.md) must pick design A or B before more
top-level patches. Code markers: `sapaloq:boundary` in `bridge_agent.go` and
`conversation.go` streamLoop.

## Reference sources (L0)

- `cursor-agent` bundle `agent.proto` descriptor
- `9router/open-sse/utils/cursorAgentProtobuf.js` (field numbers only)
- `9router/open-sse/executors/cursorAgent.js` (`driveH2` behavior)

**Not** reference: `9router/open-sse/executors/cursor.js` (api2 OpenAI shim).

## Verification

```bash
go test ./internal/bridges/cursor/wire/... -run Exec -v
go test ./internal/bridges/cursor/agent/... -v
SAPALOQ_AGENT_PATH=1 make e2e-live   # live smoke when token authorized
```
