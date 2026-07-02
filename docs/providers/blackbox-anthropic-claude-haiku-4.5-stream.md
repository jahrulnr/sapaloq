# Blackbox → anthropic/claude-haiku-4.5 (stream)

> Last updated: 2026-07-02 (characterize suite)

Live characterization via `test/blackbox` — raw `net/http` POST to Blackbox chat/completions (no SapaLOQ orchestrator). Mode: **`stream`** (`stream: true`). Weather scenario: `get_weather` fake tool round-trip. Raw capture: `/apps/workspace/sapaloq/tmp/blackbox/anthropic-claude-haiku-4.5-stream.jsonl` (4 records). Transcript: `/apps/workspace/sapaloq/tmp/blackbox/anthropic-claude-haiku-4.5-stream.md`.

## Route

| Field | Value |
|-------|-------|
| Gateway | Blackbox (`https://api.blackbox.ai/v1/chat/completions`) |
| Model slug | `anthropic/claude-haiku-4.5` |
| Wire mode | `stream` (`stream: true`) |
| SapaLOQ parser hint (configured) | `claude (auto; set explicitly for Blackbox)` |
| Sniffed parser (model name) | `claude` |
| Auth | `bearer (default)` |
| Reasoning effort | `low (probe default)` |
| Duration | 982 ms |

## Recommended entry

```json
{
  "key": "blackbox-anthropic-claude-haiku-4.5",
  "driver": "provider-bridge",
  "endpoint": "https://api.blackbox.ai/v1/chat/completions",
  "model": "anthropic/claude-haiku-4.5",
  "credentialsEnv": "BLACKBOX_API_KEY",
  "parser": "openai",
  "authScheme": "bearer",
  "requestTimeoutSec": 600
}
```

Blackbox is OpenAI-shaped at the gateway. Prefer explicit `parser: "openai"` + `authScheme: "bearer"` for Anthropic models; use `parser: "kimi"` only for Moonshot/Kimi slugs.

## Observed behavior

| Capability | Result |
|------------|--------|
| Thinking wire exposed | `no` (0 chars; reasoning_tokens=0) |
| reasoning_effort request support (`low`) | `unknown` |
| reasoning_effort implementation | reasoning_effort support not determined (probe failed before turn 1 completed) |
| thinking request support | `unknown` |
| thinking wire note | thinking/reasoning not exposed on wire (reasoning_content/reasoning empty; reasoning_tokens=0) |
| thinking request note | thinking support not determined (probe failed before turn 1 completed) |
| Tool round-trip (`get_weather`) | failed — upstream status 400: {"error":{"message":"{'error': '/chat/completions: Invalid model name passed in model=claude-haiku-… |
| tool_choice request support | `unknown` |
| tool_choice implementation | tool_choice support not determined (probe failed before turn 1 completed) |
| Final assistant text | (empty) |

### Event timeline (non-transcript kinds)

`session_start` → `turn_request_tool_choice_auto_reasoning` → `http_error`

### Upstream / stream error

```text
upstream status 400: {"error":{"message":"{'error': '/chat/completions: Invalid model name passed in model=claude-haiku-4.5. Call `/v1/models` to view available models for your key.'}","type":"None","param":"None","code":"400"}}
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
export SAPALOQ_BLACKBOX_CHARACTERIZE_E2E=1
export BLACKBOX_API_KEY=...
export BLACKBOX_MODELS='anthropic/claude-haiku-4.5|openai|bearer|'
make blackbox-characterize
```
