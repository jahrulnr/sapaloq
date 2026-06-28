# Codex App-Server Contract

> Wire contract implemented by `internal/bridges/codex/appserver`.
> Last updated: 2026-06-28 (native tool output deltas + turn progress in widget transcript)

## Transport and lifecycle

`codex-bridge` never invokes `codex exec`. It connects to `codex app-server`
using WebSocket JSON-RPC 2.0. A `unix://` endpoint is still WebSocket: the client
dials the Unix socket, performs an HTTP Upgrade using `ws://localhost/rpc`, and
sends one JSON-RPC object per WebSocket text frame. Explicit `ws://`/`wss://`
endpoints are supported for development.

| Environment | Default | Contract |
|---|---|---|
| `SAPALOQ_CODEX_APP_SERVER_MODE` | `auto` | `auto`: probe, then spawn/reap a child; `external`: connect only; `managed`: connect to Codex's managed control socket |
| `SAPALOQ_CODEX_APP_SERVER_LISTEN` | `unix://~/SapaLOQ/run/codex-app-server.sock` | Explicit Unix/WebSocket endpoint |
| `SAPALOQ_CODEX_BINARY` | `codex` from `PATH` | Binary used only to launch app-server and inspect login/version |
| `CODEX_HOME` | `~/.codex` | Codex auth/config/session state; managed socket is `$CODEX_HOME/app-server-control/app-server-control.sock` |

The child is placed in its own process group. `Bridge.Close`, core shutdown, and
provider reload send `SIGTERM`, wait, then use `SIGKILL` only if reap times out.
External/managed processes are never killed by SapaLOQ.

## JSON-RPC sequence

Every connection performs:

1. `initialize` with `capabilities.experimentalApi=true`.
2. `initialized` notification.
3. `thread/start` for a new SapaLOQ session, or `thread/resume` for a compatible
   persisted app-server thread.
4. `turn/start` with text/image inputs.
5. Consume notifications and server requests until matching `turn/completed`.

`thread/start` carries model, cwd, sandbox, `approvalPolicy:"never"`, and the
request-scoped SapaLOQ `dynamicTools` namespace. The bridge persists
`SessionID → threadId` in `vault/codex-threads.jsonl`. Records created by the
removed CLI transport have no `transport:"app-server"` marker and are not
resumed. A missing app-server thread self-heals by starting fresh and sending
the full bounded transcript.

Cancellation sends `turn/interrupt` with a short independent deadline before
closing the connection. A transport close without `turn/completed` is an error.

## Notification mapping

| App-server notification | `bridge.StreamEvent` |
|---|---|
| `thread/started` | status `session` |
| `turn/started` | progress label `Codex sedang bekerja…` |
| `item/agentMessage/delta` | response delta |
| `item/reasoning/textDelta`, `item/reasoning/summaryTextDelta` | thinking delta |
| `item/started` for Codex-native tools | tool-call telemetry with `Source:"codex"` |
| `item/commandExecution/outputDelta`, `item/fileChange/outputDelta` | `EventToolUpdate` with `Status:"running"` (streamed output chunks) |
| `item/fileChange/patchUpdated` | status `file_patch` |
| `item/completed` for native tools | `EventToolUpdate` with final `aggregatedOutput` |
| `item/completed` | batch fallback for message/reasoning when not already streamed |
| `thread/tokenUsage/updated` | `token_usage` status telemetry (skipped in widget transcript) |
| non-retrying `error` or failed `turn/completed` | one terminal error |
| successful/interrupted `turn/completed` | one terminal done |

Delta item IDs are tracked so a completed item does not duplicate already
streamed text. Unknown notifications and item kinds are ignored for forward
compatibility.

## Server requests and dynamic tools

`bridge.Request.ToolExecutor` is the callback boundary. Declared tool names,
registered descriptions, and registered JSON schemas become function entries
inside the `sapaloq` dynamic-tools namespace. On `item/tool/call` the bridge:

1. emits UI telemetry (`Source:"codex"`),
2. invokes `ToolExecutor` exactly once,
3. returns `DynamicToolCallResponse{contentItems:[inputText],success}`.

The orchestrator treats every `Source:"codex"` call as telemetry and does not
put it into `pendingTools`; this prevents both Codex-native tools and dynamic
callbacks from being dispatched twice. A terminal SapaLOQ tool result is
captured by the callback and ends the outer actor run after Codex completes its
current turn.

Headless fallback responses accept command/file approvals, decline interactive
MCP/permission escalation, and return empty answers for user-input requests.
Normal threads use `approvalPolicy:"never"`, so approval callbacks are not the
ordinary execution path.

## Verification

Offline tests cover UDS WebSocket upgrade, RPC routing, unknown messages,
stream/batch mapping, resume fallback, dynamic tool success/failure,
cancellation/interrupt, external mode, concurrent-safe process ownership, and
spawn/reap. Real checks:

```bash
go test -tags=e2e ./internal/bridges/codex/... -run TestE2EAppServerLifecycle -v
SAPALOQ_CODEX_E2E=1 go test -tags=e2e ./internal/bridges/codex -run TestE2EAppServerTurn -v
```

The implementation was verified against the local Codex v2 JSON schemas and a
live `codex-cli 0.141.0` app-server on 2026-06-28.
