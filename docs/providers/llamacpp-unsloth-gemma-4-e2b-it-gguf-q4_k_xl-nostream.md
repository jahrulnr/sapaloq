# llama.cpp (llama-server) → unsloth/gemma-4-E2B-it-GGUF:Q4_K_XL (nostream)

> Last updated: 2026-07-02 (characterize suite)

Live characterization via `test/llamacpp` — raw `net/http` POST to **llama-server** OpenAI-compatible `/v1/chat/completions` (no SapaLOQ orchestrator). Prefer direct **`:16285`** over agent-sidecar **`:16283`** so prompts are not injected. Mode: **`nostream`** (`stream: false`). Weather scenario: `get_weather` fake tool round-trip. Raw capture: `/apps/workspace/sapaloq/tmp/llamacpp/unsloth-gemma-4-e2b-it-gguf-q4_k_xl-nostream.jsonl` (7 records). Transcript: `/apps/workspace/sapaloq/tmp/llamacpp/unsloth-gemma-4-e2b-it-gguf-q4_k_xl-nostream.md`.

## Route

| Field | Value |
|-------|-------|
| Gateway | llama.cpp (llama-server) (`http://127.0.0.1:16285/v1/chat/completions`) |
| Health | `http://127.0.0.1:16285/health` |
| Model slug | `unsloth/gemma-4-E2B-it-GGUF:Q4_K_XL` |
| Wire mode | `nostream` (`stream: false`) |
| SapaLOQ parser hint (configured) | `openai` |
| Sniffed parser (model name) | `openai` |
| Auth | `bearer` |
| Reasoning effort | `low (probe default)` |
| Duration | 6342 ms |

## Recommended entry

```json
{
  "key": "llamacpp-unsloth-gemma-4-e2b-it-gguf-q4_k_xl",
  "driver": "provider-bridge",
  "endpoint": "http://127.0.0.1:16285/v1/chat/completions",
  "model": "unsloth/gemma-4-E2B-it-GGUF:Q4_K_XL",
  "credentialsEnv": "LLAMACPP_API_KEY",
  "parser": "openai",
  "authScheme": "bearer",
  "reasoningEffort": "low",
  "requestTimeoutSec": 600
}
```

llama.cpp (llama-server) is OpenAI-shaped (`/v1/chat/completions`). Use `provider-bridge` with `parser: openai` against direct llama-server. For agent-sidecar WebUI/Ollama paths, point at `http://127.0.0.1:16283` separately — chat completions there may inject system layers.

## Observed behavior

| Capability | Result |
|------------|--------|
| Thinking wire exposed | `yes` (1377 chars; reasoning_tokens=0) |
| reasoning_effort request support (`low`) | `yes` |
| thinking request support | `yes` |
| Tool round-trip (`get_weather`) | ok |
| tool_choice request support | `yes` |
| Final assistant text | The temperature in Jakarta is 32°C. |
| Tool calls (order) | `get_weather` |
| Thinking before first tool | yes |

### Event timeline (non-transcript kinds)

`session_start` → `model_preload_warning` → `turn_request_tool_choice_auto_reasoning` → `json_response` → `turn_request_tools_only_reasoning` → `json_response`

## Verdict

**Tool loop works** on llama.cpp (llama-server) (get_weather → fake result → assistant reply). `reasoningEffort: low` accepted on this route. `thinking` probe accepted. `tool_choice: auto` accepted on this route.

## Reproduce

```bash
export SAPALOQ_LLAMACPP_CHARACTERIZE_E2E=1
export LLAMACPP_MODELS='unsloth/gemma-4-E2B-it-GGUF:Q4_K_XL|openai|bearer|'
make llamacpp-characterize
```
