# OpenRouter → anthropic/claude-3-haiku

> Last updated: 2026-07-02 (characterize suite)

Live characterization via `test/openrouter` — orchestrator `SendChat`, weather scenario forcing `read_file` on `jakarta-weather.txt`. Raw stream: `tmp/openrouter/anthropic-claude-3-haiku.jsonl` (6 events).

## Route

| Field | Value |
|-------|-------|
| Gateway | OpenRouter (`https://openrouter.ai/api/v1/chat/completions`) |
| Model slug | `anthropic/claude-3-haiku` |
| Upstream (observed) | Amazon Bedrock (via OpenRouter metadata) |
| SapaLOQ parser (configured) | `openai` (recommended; do not use `claude` parser on OpenRouter) |
| Auto-detected parser | `claude` (from model name — override explicitly) |
| Auth | `bearer` |
| Duration | ~2.3 s (failed before tool loop) |

## Recommended entry

```json
{
  "key": "openrouter-anthropic-claude-3-haiku",
  "driver": "provider-bridge",
  "endpoint": "https://openrouter.ai/api/v1/chat/completions",
  "model": "anthropic/claude-3-haiku",
  "credentialsEnv": "OPENROUTER_API_KEY",
  "parser": "openai",
  "authScheme": "bearer",
  "requestTimeoutSec": 600
}
```

OpenRouter is OpenAI-shaped at the gateway. Use `parser: "openai"` + `authScheme: "bearer"` even for Claude slugs.

## Observed behavior

| Capability | Result |
|------------|--------|
| Thinking exposed | no |
| Tool round-trip (`read_file`) | failed — upstream 400 |
| Final assistant text | (empty — turn ended on error) |

### Stream sequence (transcript)

1. User prompt persisted
2. Status: `provider noise - retrying (1/3)`
3. Error row, `finished: true`

### Upstream / stream error

```text
provider-bridge: upstream status 400: {"error":{"message":"Provider returned error","code":400,"metadata":{"raw":"{\"message\":\"tool_choice may only be specified while providing tools.\"}","provider_name":"Amazon Bedrock","is_byok":false}}}
```

Bedrock rejects the request when SapaLOQ forwards the orchestrator tool surface. No `read_file` call reached the model.

## Verdict

**Tools not usable** on this OpenRouter route in the characterize scenario. Do not use `anthropic/claude-3-haiku` through OpenRouter for SapaLOQ agent/tool workflows until OpenRouter/Bedrock accepts the tool payload or SapaLOQ adds a route-specific workaround.

Text-only chat may work if the orchestrator tool list is not offered (not tested here).

## Reproduce

```bash
export SAPALOQ_OPENROUTER_E2E=1
export OPENROUTER_API_KEY=sk-or-...
export OPENROUTER_MODELS='anthropic/claude-3-haiku|openai|bearer|'
make openrouter-characterize
```
