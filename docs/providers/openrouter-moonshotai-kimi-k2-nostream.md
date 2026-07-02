# OpenRouter → moonshotai/kimi-k2 (nostream)

> Last updated: 2026-07-02 (characterize suite)

Live characterization via `test/openrouter` — raw `net/http` POST to OpenRouter chat/completions (no SapaLOQ orchestrator). Mode: **`nostream`** (`stream: false`). Weather scenario: `get_weather` fake tool round-trip. Raw capture: `/apps/workspace/sapaloq/tmp/openrouter/moonshotai-kimi-k2-nostream.jsonl` (7 records). Transcript: `/apps/workspace/sapaloq/tmp/openrouter/moonshotai-kimi-k2-nostream.md`.

## Route

| Field | Value |
|-------|-------|
| Gateway | OpenRouter (`https://openrouter.ai/api/v1/chat/completions`) |
| Model slug | `moonshotai/kimi-k2` |
| Wire mode | `nostream` (`stream: false`) |
| SapaLOQ parser hint (configured) | `kimi (auto; set explicitly for OpenRouter)` |
| Sniffed parser (model name) | `kimi` |
| Auth | `bearer (default)` |
| Reasoning effort | `low (probe default)` |
| Duration | 7081 ms |

## Recommended entry

```json
{
  "key": "openrouter-moonshotai-kimi-k2",
  "driver": "provider-bridge",
  "endpoint": "https://openrouter.ai/api/v1/chat/completions",
  "model": "moonshotai/kimi-k2",
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
| Final assistant text | Jakarta's temperature is 32°C. |
| Tool calls (order) | `get_weather` |
| Content before first tool | yes |

### Event timeline (non-transcript kinds)

`session_start` → `turn_request_tool_choice_auto_reasoning` → `json_response` → `reasoning_probe` → `tool_choice_probe` → `turn_request_tools_only_reasoning` → `json_response`

### Notes

- no reasoning_content/reasoning observed on the wire for this run

## Verdict

**Tool loop works** on OpenRouter (get_weather → fake result → assistant reply). `reasoningEffort: low` accepted on this route. `thinking` probe accepted. `tool_choice: auto` accepted on this route. Thinking/reasoning was not visible on the wire for this run.

## Reproduce

```bash
export SAPALOQ_OPENROUTER_E2E=1
export OPENROUTER_API_KEY=sk-or-...
export OPENROUTER_MODELS='moonshotai/kimi-k2|openai|bearer|'
make openrouter-characterize
```
