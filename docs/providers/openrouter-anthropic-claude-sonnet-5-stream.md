# OpenRouter → anthropic/claude-sonnet-5 (stream)

> Last updated: 2026-07-02 (characterize suite)

Live characterization via `test/openrouter` — raw `net/http` POST to OpenRouter chat/completions (no SapaLOQ orchestrator). Mode: **`stream`** (`stream: true`). Weather scenario: `get_weather` fake tool round-trip. Raw capture: `/apps/workspace/sapaloq/tmp/openrouter/anthropic-claude-sonnet-5-stream.jsonl` (16 records). Transcript: `/apps/workspace/sapaloq/tmp/openrouter/anthropic-claude-sonnet-5-stream.md`.

## Route

| Field | Value |
|-------|-------|
| Gateway | OpenRouter (`https://openrouter.ai/api/v1/chat/completions`) |
| Model slug | `anthropic/claude-sonnet-5` |
| Wire mode | `stream` (`stream: true`) |
| SapaLOQ parser hint (configured) | `claude (auto; set explicitly for OpenRouter)` |
| Sniffed parser (model name) | `claude` |
| Auth | `bearer (default)` |
| Reasoning effort | `low (probe default)` |
| Duration | 6073 ms |

## Recommended entry

```json
{
  "key": "openrouter-anthropic-claude-sonnet-5",
  "driver": "provider-bridge",
  "endpoint": "https://openrouter.ai/api/v1/chat/completions",
  "model": "anthropic/claude-sonnet-5",
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
| Thinking wire exposed | `no` (0 chars; reasoning_tokens=0) |
| reasoning_effort request support (`low`) | `yes` |
| thinking request support | `yes` |
| thinking wire note | thinking/reasoning not exposed on wire (reasoning_content/reasoning empty; reasoning_tokens=0) |
| Tool round-trip (`get_weather`) | ok |
| tool_choice request support | `yes` |
| Final assistant text | It's 32°C and partly cloudy with humidity in Jakarta right now. |
| Tool calls (order) | `get_weather` |

### Event timeline (non-transcript kinds)

`session_start` → `turn_request_tool_choice_auto_reasoning` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_done` → `turn_request_tools_only_reasoning` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_done`

### Notes

- no reasoning_content/reasoning observed on the wire for this run
- thinking/reasoning not exposed on wire (reasoning_content/reasoning empty; reasoning_tokens=0)

## Verdict

**Tool loop works** on OpenRouter (get_weather → fake result → assistant reply). `reasoningEffort: low` accepted on this route. `thinking` probe accepted. `tool_choice: auto` accepted on this route. Thinking/reasoning was not visible on the wire for this run.

## Reproduce

```bash
export SAPALOQ_OPENROUTER_E2E=1
export OPENROUTER_API_KEY=...
export OPENROUTER_MODELS='anthropic/claude-sonnet-5|openai|bearer|'
make openrouter-characterize
```
