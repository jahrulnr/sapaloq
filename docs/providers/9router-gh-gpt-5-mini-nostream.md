# 9router → gh/gpt-5-mini (nostream)

> Last updated: 2026-07-02 (characterize suite)

Live characterization via `test/9router` — raw `net/http` POST to 9router chat/completions (no SapaLOQ orchestrator). Mode: **`nostream`** (`stream: false`). Weather scenario: `get_weather` fake tool round-trip. Raw capture: `/apps/workspace/sapaloq/tmp/9router/gh-gpt-5-mini-nostream.jsonl` (4 records). Transcript: `/apps/workspace/sapaloq/tmp/9router/gh-gpt-5-mini-nostream.md`.

## Route

| Field | Value |
|-------|-------|
| Gateway | 9router (`http://127.0.0.1:20128/v1/chat/completions`) |
| Model slug | `gh/gpt-5-mini` |
| Wire mode | `nostream` (`stream: false`) |
| SapaLOQ parser hint (configured) | `openai (auto; set explicitly for 9router)` |
| Sniffed parser (model name) | `openai` |
| Auth | `bearer (default)` |
| Reasoning effort | `low (probe default)` |
| Duration | 3 ms |

## Recommended entry

```json
{
  "key": "9router-gh-gpt-5-mini",
  "driver": "provider-bridge",
  "endpoint": "http://127.0.0.1:20128/v1/chat/completions",
  "model": "gh/gpt-5-mini",
  "credentialsEnv": "NROUTER_API_KEY",
  "parser": "openai",
  "authScheme": "bearer",
  "requestTimeoutSec": 600
}
```

9router exposes OpenAI-compatible `/v1/chat/completions` locally (default `127.0.0.1:20128`). Model slugs are gateway aliases (e.g. `cu/default`, `codex-cursor`, `kr/claude-sonnet-4.5`). Prefer `parser: "openai"` + bearer; use `parser: "kimi"` for Kimi-routed slugs.

## Observed behavior

| Capability | Result |
|------------|--------|
| Thinking wire exposed | `no` (0 chars; reasoning_tokens=0) |
| reasoning_effort request support (`low`) | `unknown` |
| reasoning_effort implementation | reasoning_effort support not determined (probe failed before turn 1 completed) |
| thinking request support | `unknown` |
| thinking wire note | thinking/reasoning not exposed on wire (reasoning_content/reasoning empty; reasoning_tokens=0) |
| thinking request note | thinking support not determined (probe failed before turn 1 completed) |
| Tool round-trip (`get_weather`) | failed — upstream status 400: {"error":{"message":"[github/gpt-5-mini] [400]: {\"error\":{\"message\":\"The requested model is no… |
| tool_choice request support | `unknown` |
| tool_choice implementation | tool_choice support not determined (probe failed before turn 1 completed) |
| Final assistant text | (empty) |

### Event timeline (non-transcript kinds)

`session_start` → `turn_request_tool_choice_auto_reasoning` → `http_error`

### Upstream / stream error

```text
upstream status 400: {"error":{"message":"[github/gpt-5-mini] [400]: {\"error\":{\"message\":\"The requested model is not supported.\",\"code\":\"model_not_supported\",\"par (reset after 30s)"}}
```

### Notes

- no reasoning_content/reasoning observed on the wire for this run
- reasoning_effort support not determined (probe failed before turn 1 completed)
- thinking/reasoning not exposed on wire (reasoning_content/reasoning empty; reasoning_tokens=0)
- thinking support not determined (probe failed before turn 1 completed)
- tool_choice support not determined (probe failed before turn 1 completed)

## Verdict

**Characterization failed** — see upstream error.

## Reproduce

```bash
export SAPALOQ_9ROUTER_CHARACTERIZE_E2E=1
export NROUTER_API_KEY=...
export NROUTER_MODELS='gh/gpt-5-mini|openai|bearer|'
make 9router-characterize
```
