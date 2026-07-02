# 9router → cx/gpt-5.4-mini (nostream)

> Last updated: 2026-07-02 (characterize suite)

Live characterization via `test/9router` — raw `net/http` POST to 9router chat/completions (no SapaLOQ orchestrator). Mode: **`nostream`** (`stream: false`). Weather scenario: `get_weather` fake tool round-trip. Raw capture: `/apps/workspace/sapaloq/tmp/9router/cx-gpt-5.4-mini-nostream.jsonl` (6 records). Transcript: `/apps/workspace/sapaloq/tmp/9router/cx-gpt-5.4-mini-nostream.md`.

## Route

| Field | Value |
|-------|-------|
| Gateway | 9router (`http://127.0.0.1:20128/v1/chat/completions`) |
| Model slug | `cx/gpt-5.4-mini` |
| Wire mode | `nostream` (`stream: false`) |
| SapaLOQ parser hint (configured) | `openai (auto; set explicitly for 9router)` |
| Sniffed parser (model name) | `openai` |
| Auth | `bearer (default)` |
| Reasoning effort | `low (probe default)` |
| Duration | 8836 ms |

## Recommended entry

```json
{
  "key": "9router-cx-gpt-5.4-mini",
  "driver": "provider-bridge",
  "endpoint": "http://127.0.0.1:20128/v1/chat/completions",
  "model": "cx/gpt-5.4-mini",
  "credentialsEnv": "NROUTER_API_KEY",
  "parser": "openai",
  "authScheme": "bearer",
  "reasoningEffort": "low",
  "requestTimeoutSec": 600
}
```

9router exposes OpenAI-compatible `/v1/chat/completions` locally (default `127.0.0.1:20128`). Model slugs are gateway aliases (e.g. `cu/default`, `codex-cursor`, `kr/claude-sonnet-4.5`). Prefer `parser: "openai"` + bearer; use `parser: "kimi"` for Kimi-routed slugs.

## Observed behavior

| Capability | Result |
|------------|--------|
| Thinking wire exposed | `no` (0 chars; reasoning_tokens=0) |
| reasoning_effort request support (`low`) | `yes` |
| thinking request support | `yes` |
| thinking wire note | thinking/reasoning not exposed on wire (reasoning_content/reasoning empty; reasoning_tokens=0) |
| Tool round-trip (`get_weather`) | ok |
| tool_choice request support | `yes` |
| Final assistant text | Jakarta is 32°C and humid, partly cloudy. |
| Tool calls (order) | `get_weather` |

### Event timeline (non-transcript kinds)

`session_start` → `turn_request_tool_choice_auto_reasoning` → `json_response` → `turn_request_tools_only_reasoning` → `json_response`

### Notes

- no reasoning_content/reasoning observed on the wire for this run
- thinking/reasoning not exposed on wire (reasoning_content/reasoning empty; reasoning_tokens=0)

## Verdict

**Tool loop works** on 9router (get_weather → fake result → assistant reply). `reasoningEffort: low` accepted on this route. `thinking` probe accepted. `tool_choice: auto` accepted on this route. Thinking/reasoning was not visible on the wire for this run.

## Reproduce

```bash
export SAPALOQ_9ROUTER_CHARACTERIZE_E2E=1
export NROUTER_API_KEY=...
export NROUTER_MODELS='cx/gpt-5.4-mini|openai|bearer|'
make 9router-characterize
```
