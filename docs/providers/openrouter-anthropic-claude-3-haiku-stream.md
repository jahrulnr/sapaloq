# OpenRouter → anthropic/claude-3-haiku (stream)

> Last updated: 2026-07-02 (characterize suite)

Live characterization via `test/openrouter` — raw `net/http` POST to OpenRouter chat/completions (no SapaLOQ orchestrator). Mode: **`stream`** (`stream: true`). Weather scenario: `get_weather` fake tool round-trip. Raw capture: `/apps/workspace/sapaloq/tmp/openrouter/anthropic-claude-3-haiku-stream.jsonl` (30 records). Transcript: `/apps/workspace/sapaloq/tmp/openrouter/anthropic-claude-3-haiku-stream.md`.

## Route

| Field | Value |
|-------|-------|
| Gateway | OpenRouter (`https://openrouter.ai/api/v1/chat/completions`) |
| Model slug | `anthropic/claude-3-haiku` |
| Wire mode | `stream` (`stream: true`) |
| SapaLOQ parser hint (configured) | `claude (auto; set explicitly for OpenRouter)` |
| Sniffed parser (model name) | `claude` |
| Auth | `bearer (default)` |
| Reasoning effort | `low (probe default)` |
| Duration | 3614 ms |

## Recommended entry

```json
{
  "key": "openrouter-anthropic-claude-3-haiku",
  "driver": "provider-bridge",
  "endpoint": "https://openrouter.ai/api/v1/chat/completions",
  "model": "anthropic/claude-3-haiku",
  "credentialsEnv": "OPENROUTER_API_KEY",
  "parser": "openai",
  "authScheme": "bearer",
  "reasoningEffort": "low",
  "requestTimeoutSec": 600
}
```

OpenRouter is OpenAI-shaped at the gateway. Prefer explicit `parser: "openai"` + `authScheme: "bearer"` for Anthropic models; use `parser: "kimi"` only for Moonshot/Kimi slugs.

## Observed behavior

| Capability | Result |
|------------|--------|
| Thinking exposed | no |
| reasoning_effort support (`low`) | `yes` |
| thinking support | `yes` |
| Tool round-trip (`get_weather`) | ok |
| tool_choice support | `yes` |
| Final assistant text | The temperature in Jakarta is 32 degrees Celsius. |
| Tool calls (order) | `get_weather` |
| Content before first tool | yes |

### Event timeline (non-transcript kinds)

`session_start` → `turn_request_tool_choice_auto_reasoning` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_done` → `reasoning_probe` → `tool_choice_probe` → `turn_request_tools_only_reasoning` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_done`

### Notes

- no reasoning_content/reasoning observed on the wire for this run

## Verdict

**Tool loop works** on OpenRouter (get_weather → fake result → assistant reply). `reasoningEffort: low` accepted on this route. `thinking` probe accepted. `tool_choice: auto` accepted on this route. Thinking/reasoning was not visible on the wire for this run.

## Reproduce

```bash
export SAPALOQ_OPENROUTER_E2E=1
export OPENROUTER_API_KEY=sk-or-...
export OPENROUTER_MODELS='anthropic/claude-3-haiku|openai|bearer|'
make openrouter-characterize
```
