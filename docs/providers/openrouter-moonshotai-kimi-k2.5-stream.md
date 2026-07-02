# OpenRouter → moonshotai/kimi-k2.5 (stream)

> Last updated: 2026-07-02 (characterize suite)

Live characterization via `test/openrouter` — raw `net/http` POST to OpenRouter chat/completions (no SapaLOQ orchestrator). Mode: **`stream`** (`stream: true`). Weather scenario: `get_weather` fake tool round-trip. Raw capture: `/apps/workspace/sapaloq/tmp/openrouter/moonshotai-kimi-k2.5-stream.jsonl` (186 records). Transcript: `/apps/workspace/sapaloq/tmp/openrouter/moonshotai-kimi-k2.5-stream.md`.

## Route

| Field | Value |
|-------|-------|
| Gateway | OpenRouter (`https://openrouter.ai/api/v1/chat/completions`) |
| Model slug | `moonshotai/kimi-k2.5` |
| Wire mode | `stream` (`stream: true`) |
| SapaLOQ parser hint (configured) | `kimi (auto; set explicitly for OpenRouter)` |
| Sniffed parser (model name) | `kimi` |
| Auth | `bearer (default)` |
| Reasoning effort | `low (probe default)` |
| Duration | 5711 ms |

## Recommended entry

```json
{
  "key": "openrouter-moonshotai-kimi-k2.5",
  "driver": "provider-bridge",
  "endpoint": "https://openrouter.ai/api/v1/chat/completions",
  "model": "moonshotai/kimi-k2.5",
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
| Thinking wire exposed | `yes` (666 chars; reasoning_tokens=131) |
| reasoning_effort request support (`low`) | `yes` |
| thinking request support | `yes` |
| Tool round-trip (`get_weather`) | ok |
| tool_choice request support | `yes` |
| Final assistant text | It's 32°C and humid with partly cloudy skies in Jakarta. |
| Tool calls (order) | `get_weather` |
| Thinking before first tool | yes |

### Event timeline (non-transcript kinds)

`session_start` → `turn_request_tool_choice_auto_reasoning` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_done` → `turn_request_tools_only_reasoning` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_done`

## Verdict

**Tool loop works** on OpenRouter (get_weather → fake result → assistant reply). `reasoningEffort: low` accepted on this route. `thinking` probe accepted. `tool_choice: auto` accepted on this route.

## Reproduce

```bash
export SAPALOQ_OPENROUTER_E2E=1
export OPENROUTER_API_KEY=sk-or-...
export OPENROUTER_MODELS='moonshotai/kimi-k2.5|openai|bearer|'
make openrouter-characterize
```
