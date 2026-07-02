# Bridge creator reference

## Existing drivers

| Driver | Package | Wire | Characterize suite |
|--------|---------|------|------------------|
| `provider-bridge` | `internal/bridges/provider/` | OpenAI / Claude / Kimi HTTP | `test/openrouter/`, `test/blackbox/`, `test/9router/` |
| `cursor-bridge` | `internal/bridges/cursor/` | Cursor api2/api5 | (orchestrator e2e) |
| `codex-bridge` | `internal/bridges/codex/` | Codex app-server JSON-RPC | simulate tests |
| `gemini-bridge` | `internal/bridges/gemini/` | Google generateContent | `test/gemini/` |
| `llama-cpp` | `internal/bridges/llamacpp/` | llama-server OpenAI `/v1/chat/completions` | `test/llamacpp/` |

## Characterize env pattern

| Provider | Gate | Credentials | Models |
|----------|------|-------------|--------|
| OpenRouter | `SAPALOQ_OPENROUTER_CHARACTERIZE_E2E` | `OPENROUTER_API_KEY` | `OPENROUTER_MODELS` |
| Blackbox | `SAPALOQ_BLACKBOX_CHARACTERIZE_E2E` | `BLACKBOX_API_KEY` | `BLACKBOX_MODELS` |
| 9router | `SAPALOQ_9ROUTER_CHARACTERIZE_E2E` | `NROUTER_API_KEY` | `NROUTER_MODELS` |
| Gemini | `SAPALOQ_GEMINI_CHARACTERIZE_E2E` | `GOOGLE_API_KEY` / `GEMINI_API_KEY` | `GEMINI_MODELS` |
| llama.cpp | `SAPALOQ_LLAMACPP_CHARACTERIZE_E2E` | `LLAMACPP_API_KEY` (optional) | `LLAMACPP_MODELS` |

`LLAMACPP_ENDPOINT` overrides default `http://127.0.0.1:8080/v1/chat/completions`.

`GEMINI_MODELS` / others format: `model|parser|authScheme|reasoningEffort` (comma-separated).

## Lift characterize → bridge

When production bridge is ready, port from `test/{provider}/raw_client_test.go`:

- `build*Body`, SSE/JSON readers, merge helpers
- Probe fallback detection (`isToolChoiceRejected`, etc.)
- Keep characterize suite as **regression oracle**; do not delete after bridge lands.

## WireMeta JSON (Gemini)

```json
{
  "driver": "gemini-bridge",
  "model_parts": [
    {"text": "...", "thought": true},
    {
      "functionCall": {"id": "...", "name": "...", "args": {}},
      "thoughtSignature": "..."
    }
  ]
}
```

## Key orchestrator touchpoints

- `conversation.go` — `Complete()` loop, tool dispatch
- `prompt.go` — `actorTurnsToMessages` (replay mapper)
- `stream_persist.go` — `persistContinuationRound`
- `session_context.go` — turn persist helpers

## Doc-sync (from AGENTS.md)

Bridge/provider wire changes → `docs/BRIDGE.md`, `docs/PROVIDER-BRIDGE.md` (or dedicated `docs/*-BRIDGE.md`), `docs/STATUS.md`.
