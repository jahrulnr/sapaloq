# Gemini → gemini-flash-latest (stream)

> Last updated: 2026-07-02 (characterize suite)

Live characterization via `test/gemini` — raw `net/http` POST to Google **generateContent** / **streamGenerateContent** (no SapaLOQ orchestrator). Mode: **`stream`** (`stream: true`). Weather scenario: `get_weather` fake tool round-trip. Raw capture: `/apps/workspace/sapaloq/tmp/gemini/gemini-flash-latest-stream.jsonl` (14 records). Transcript: `/apps/workspace/sapaloq/tmp/gemini/gemini-flash-latest-stream.md`.

## Route

| Field | Value |
|-------|-------|
| Gateway | Gemini (`https://generativelanguage.googleapis.com/v1beta/models/<model>:generateContent`) |
| Model slug | `gemini-flash-latest` |
| Wire mode | `stream` (`stream: true`) |
| SapaLOQ parser hint (configured) | `gemini` |
| Sniffed parser (model name) | `gemini` |
| Auth | `x-goog-api-key` |
| Reasoning effort | `low` |
| Duration | 4514 ms |

## Recommended entry

```json
{
  "key": "gemini-gemini-flash-latest",
  "driver": "provider-bridge",
  "endpoint": "https://generativelanguage.googleapis.com/v1beta/models/gemini-flash-latest:generateContent",
  "model": "gemini-flash-latest",
  "credentialsEnv": "GEMINI_API_KEY",
  "parser": "gemini",
  "authScheme": "x-goog-api-key",
  "reasoningEffort": "low",
  "requestTimeoutSec": 600
}
```

Gemini uses the Google Generative Language API (`generateContent`). Auth header **`X-goog-api-key`**. Thinking is probed via `generationConfig.thinkingConfig` (`thinkingLevel` + `includeThoughts`); wire may expose `thoughtSignature` / `thoughtsTokenCount` without visible thought text (see `thinking_wire_exposed`). Tools use `functionDeclarations` + optional `toolConfig.functionCallingConfig.mode: AUTO`.

## Observed behavior

| Capability | Result |
|------------|--------|
| Thinking wire exposed | `no` (0 chars; reasoning_tokens=84) |
| reasoning_effort request support (`low`) | `yes` |
| thinking request support | `yes` |
| thinking wire note | thinking/reasoning not exposed on wire (reasoning_content/reasoning empty; reasoning_tokens=84) |
| Tool round-trip (`get_weather`) | ok |
| tool_choice request support | `yes` |
| Final assistant text | The current temperature in Jakarta is 32°C with humid, partly cloudy conditions. |
| Tool calls (order) | `get_weather` |
| Content before first tool | yes |

### Event timeline (non-transcript kinds)

`session_start` → `turn_request_tool_config_auto_thinking` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_data` → `sse_done` → `turn_request_tools_only_thinking` → `sse_data` → `sse_data` → `sse_data` → `sse_done`

### Notes

- no reasoning_content/reasoning observed on the wire for this run
- thinking/reasoning not exposed on wire (reasoning_content/reasoning empty; reasoning_tokens=84)

## Verdict

**Tool loop works** on Gemini (get_weather → fake result → assistant reply). `thinkingLevel: low` accepted on this route. `generationConfig.thinkingConfig` probe accepted. `toolConfig.functionCallingConfig.mode: AUTO` accepted on this route. Thinking/reasoning was not visible on the wire for this run.

## Reproduce

```bash
export SAPALOQ_GEMINI_CHARACTERIZE_E2E=1
export GEMINI_API_KEY=...
export GEMINI_MODELS='gemini-flash-latest|openai|bearer|'
make gemini-characterize
```
