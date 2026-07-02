# Gemini → gemini-flash-latest (nostream)

> Last updated: 2026-07-02 (characterize suite)

Live characterization via `test/gemini` — raw `net/http` POST to Google **generateContent** / **streamGenerateContent** (no SapaLOQ orchestrator). Mode: **`nostream`** (`stream: false`). Weather scenario: `get_weather` fake tool round-trip. Raw capture: `/apps/workspace/sapaloq/tmp/gemini/gemini-flash-latest-nostream.jsonl` (6 records). Transcript: `/apps/workspace/sapaloq/tmp/gemini/gemini-flash-latest-nostream.md`.

## Route

| Field | Value |
|-------|-------|
| Gateway | Gemini (`https://generativelanguage.googleapis.com/v1beta/models/<model>:generateContent`) |
| Model slug | `gemini-flash-latest` |
| Wire mode | `nostream` (`stream: false`) |
| SapaLOQ parser hint (configured) | `gemini` |
| Sniffed parser (model name) | `gemini` |
| Auth | `x-goog-api-key` |
| Reasoning effort | `low` |
| Duration | 5217 ms |

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
| Thinking wire exposed | `yes` (1347 chars; reasoning_tokens=128) |
| reasoning_effort request support (`low`) | `yes` |
| thinking request support | `yes` |
| Tool round-trip (`get_weather`) | ok |
| tool_choice request support | `yes` |
| Final assistant text | It is currently 32°C and partly cloudy in Jakarta. |
| Tool calls (order) | `get_weather` |
| Content before first tool | yes |
| Thinking before first tool | yes |

### Event timeline (non-transcript kinds)

`session_start` → `turn_request_tool_config_auto_thinking` → `json_response` → `turn_request_tools_only_thinking` → `json_response`

## Verdict

**Tool loop works** on Gemini (get_weather → fake result → assistant reply). `thinkingLevel: low` accepted on this route. `generationConfig.thinkingConfig` probe accepted. `toolConfig.functionCallingConfig.mode: AUTO` accepted on this route.

## Reproduce

```bash
export SAPALOQ_GEMINI_CHARACTERIZE_E2E=1
export GEMINI_API_KEY=...
export GEMINI_MODELS='gemini-flash-latest|openai|bearer|'
make gemini-characterize
```
