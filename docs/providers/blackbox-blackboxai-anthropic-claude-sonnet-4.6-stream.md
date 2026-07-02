# Blackbox → blackboxai/anthropic/claude-sonnet-4.6 (stream)

> Last updated: 2026-07-02 (characterize suite)

Live characterization via `test/blackbox` — raw `net/http` POST to Blackbox chat/completions (no SapaLOQ orchestrator). Mode: **`stream`** (`stream: true`). Weather scenario: `get_weather` fake tool round-trip. Raw capture: `/apps/workspace/sapaloq/tmp/blackbox/blackboxai-anthropic-claude-sonnet-4.6-stream.jsonl` (26 records). Transcript: `/apps/workspace/sapaloq/tmp/blackbox/blackboxai-anthropic-claude-sonnet-4.6-stream.md`.

## Route

| Field | Value |
|-------|-------|
| Gateway | Blackbox (`https://api.blackbox.ai/v1/chat/completions`) |
| Model slug | `blackboxai/anthropic/claude-sonnet-4.6` |
| Wire mode | `stream` (`stream: true`) |
| SapaLOQ parser hint (configured) | `claude (auto; set explicitly for Blackbox)` |
| Sniffed parser (model name) | `claude` |
| Auth | `bearer (default)` |
| Reasoning effort | `low (probe default)` |
| Duration | 4788 ms |

## Recommended entry

```json
{
  "key": "blackbox-blackboxai-anthropic-claude-sonnet-4.6",
  "driver": "provider-bridge",
  "endpoint": "https://api.blackbox.ai/v1/chat/completions",
  "model": "blackboxai/anthropic/claude-sonnet-4.6",
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
| reasoning_effort request support (`low`) | `no` |
| reasoning_effort implementation | upstream rejects reasoning_effort — leave reasoningEffort unset in provider entry |
| reasoning_effort fallback | yes (retried unset) |
| thinking request support | `no` |
| thinking wire note | thinking/reasoning not exposed on wire (reasoning_content/reasoning empty; reasoning_tokens=0) |
| thinking request note | upstream rejects thinking field — omit thinking payload in provider-bridge |
| thinking fallback | yes (retried unset) |
| Tool round-trip (`get_weather`) | ok |
| tool_choice request support | `yes` |
| Final assistant text | Jakarta is currently a warm **32°C** with humid, partly cloudy conditions. |
| Tool calls (order) | `get_weather` |

### Event timeline (non-transcript kinds)

`session_start` → `turn_request_tool_choice_auto_reasoning` → `http_error` → `reasoning_fallback` → `turn_request_tool_choice_auto` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_done` → `turn_request_tools_only` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_done`

### Notes

- no reasoning_content/reasoning observed on the wire for this run
- upstream rejects reasoning_effort — leave reasoningEffort unset in provider entry (retried with reasoning_effort unset)
- thinking/reasoning not exposed on wire (reasoning_content/reasoning empty; reasoning_tokens=0)
- upstream rejects thinking field — omit thinking payload in provider-bridge (retried with thinking unset)

## Verdict

**Tool loop works** on Blackbox (get_weather → fake result → assistant reply). **Leave `reasoningEffort` unset** on this route. **Omit `thinking`** on this route. `tool_choice: auto` accepted on this route. Thinking/reasoning was not visible on the wire for this run.

## Reproduce

```bash
export SAPALOQ_BLACKBOX_CHARACTERIZE_E2E=1
export BLACKBOX_API_KEY=...
export BLACKBOX_MODELS='blackboxai/anthropic/claude-sonnet-4.6|openai|bearer|'
make blackbox-characterize
```
