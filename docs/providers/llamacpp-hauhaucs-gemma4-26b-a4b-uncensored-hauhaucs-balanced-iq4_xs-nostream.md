# llama.cpp (llama-server) → HauhauCS/Gemma4-26B-A4B-Uncensored-HauhauCS-Balanced:IQ4_XS (nostream)

> Last updated: 2026-07-02 (characterize suite)

Live characterization via `test/llamacpp` — raw `net/http` POST to **llama-server** OpenAI-compatible `/v1/chat/completions` (no SapaLOQ orchestrator). Prefer direct **`:16285`** over agent-sidecar **`:16283`** so prompts are not injected. Mode: **`nostream`** (`stream: false`). Weather scenario: `get_weather` fake tool round-trip. Raw capture: `/home/jahrulnr/SapaLOQ/workspace/sapaloq-codebase/tmp/llamacpp/hauhaucs-gemma4-26b-a4b-uncensored-hauhaucs-balanced-iq4_xs-nostream.jsonl` (3 records). Transcript: `/home/jahrulnr/SapaLOQ/workspace/sapaloq-codebase/tmp/llamacpp/hauhaucs-gemma4-26b-a4b-uncensored-hauhaucs-balanced-iq4_xs-nostream.md`.

## Route

| Field | Value |
|-------|-------|
| Gateway | llama.cpp (llama-server) (`http://127.0.0.1:16285/v1/chat/completions`) |
| Health | `http://127.0.0.1:16285/health` |
| Model slug | `HauhauCS/Gemma4-26B-A4B-Uncensored-HauhauCS-Balanced:IQ4_XS` |
| Wire mode | `nostream` (`stream: false`) |
| SapaLOQ parser hint (configured) | `openai` |
| Sniffed parser (model name) | `openai` |
| Auth | `bearer` |
| Reasoning effort | `low (probe default)` |
| Duration | 120089 ms |

## Recommended entry

```json
{
  "key": "llamacpp-hauhaucs-gemma4-26b-a4b-uncensored-hauhaucs-balanced-iq4_xs",
  "driver": "provider-bridge",
  "endpoint": "http://127.0.0.1:16285/v1/chat/completions",
  "model": "HauhauCS/Gemma4-26B-A4B-Uncensored-HauhauCS-Balanced:IQ4_XS",
  "credentialsEnv": "LLAMACPP_API_KEY",
  "parser": "openai",
  "authScheme": "bearer",
  "requestTimeoutSec": 600
}
```

llama.cpp (llama-server) is OpenAI-shaped (`/v1/chat/completions`). Use `provider-bridge` with `parser: openai` against direct llama-server. For agent-sidecar WebUI/Ollama paths, point at `http://127.0.0.1:16283` separately — chat completions there may inject system layers.

## Observed behavior

| Capability | Result |
|------------|--------|
| Thinking wire exposed | `no` (0 chars; reasoning_tokens=0) |
| reasoning_effort request support (`low`) | `unknown` |
| reasoning_effort implementation | reasoning_effort support not determined (probe failed before turn 1 completed) |
| thinking request support | `unknown` |
| thinking wire note | thinking/reasoning not exposed on wire (reasoning_content/reasoning empty; reasoning_tokens=0) |
| thinking request note | thinking support not determined (probe failed before turn 1 completed) |
| Tool round-trip (`get_weather`) | failed — Post "http://127.0.0.1:16285/v1/chat/completions": context deadline exceeded (Client.Timeout exceeded while awaiting hea… |
| tool_choice request support | `unknown` |
| tool_choice implementation | tool_choice support not determined (probe failed before turn 1 completed) |
| Final assistant text | (empty) |

### Event timeline (non-transcript kinds)

`session_start` → `turn_request_tool_choice_auto_reasoning`

### Upstream / stream error

```text
Post "http://127.0.0.1:16285/v1/chat/completions": context deadline exceeded (Client.Timeout exceeded while awaiting headers)
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
export SAPALOQ_LLAMACPP_CHARACTERIZE_E2E=1
export LLAMACPP_MODELS='HauhauCS/Gemma4-26B-A4B-Uncensored-HauhauCS-Balanced:IQ4_XS|openai|bearer|'
make llamacpp-characterize
```
