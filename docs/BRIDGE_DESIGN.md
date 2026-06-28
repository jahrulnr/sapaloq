# Bridge Design

> LLM bridge execution architecture and ownership boundaries.
> Last updated: 2026-06-28 (Codex app-server socket transport)

## Codex socket-only path

```text
runTurnLoop
  └─ bridge.Request { messages, images, declared tools, ToolExecutor }
      └─ codex-bridge
          ├─ lifecycle manager: probe or spawn codex app-server
          ├─ WebSocket JSON-RPC over UDS/WS
          ├─ thread start/resume + one turn
          ├─ notifications → StreamEvent
          └─ item/tool/call → ToolExecutor → JSON-RPC response
```

The app-server process is long-lived and process-owned; JSON-RPC connections
are turn-scoped. This keeps cancellation and request state isolated while
avoiding a Codex process per inference turn. Session continuity lives in Codex
threads plus SapaLOQ's append-only thread mapping.

## Tool ownership

- Provider HTTP bridges emit tool calls; the orchestrator batches and executes
  them after the provider turn.
- Codex app-server runs its native tools internally. Those events are telemetry.
- SapaLOQ tools offered to Codex use app-server dynamic tool callbacks and run
  through the same orchestrator dispatcher during the Codex turn.
- `Source:"codex"` is the no-re-dispatch invariant. The callback remains the
  only execution point, including terminal tools.

Descriptions and JSON parameter schemas are shared with provider-bridge via
the registered tool catalog, so OpenAI/Claude/Codex see the same contract.

## Lifecycle ownership

`auto` owns a child and must reap it. `external` and `managed` own only a
connection. `Orchestrator.Close` and config replacement close an optional
bridge `Close() error` capability after active actors stop. A provider switch
therefore cannot leak the prior app-server child.

See [CODEX_APP_SERVER_CONTRACT.md](./CODEX_APP_SERVER_CONTRACT.md) for the exact
wire and [BRIDGE.md](./BRIDGE.md) for driver selection.
