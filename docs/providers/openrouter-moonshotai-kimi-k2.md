# OpenRouter → moonshotai/kimi-k2

> Last updated: 2026-07-02 (characterize suite)

Live characterization via `test/openrouter` — orchestrator `SendChat`, weather scenario forcing `read_file` on `jakarta-weather.txt`. Raw stream: `tmp/openrouter/moonshotai-kimi-k2.jsonl` (22 events).

## Route

| Field | Value |
|-------|-------|
| Gateway | OpenRouter (`https://openrouter.ai/api/v1/chat/completions`) |
| Model slug | `moonshotai/kimi-k2` |
| SapaLOQ parser (configured) | `kimi` (recommended) |
| Auto-detected parser | `kimi` |
| Auth | `bearer` |
| Duration | ~6 s (full tool round-trip) |

## Recommended entry

```json
{
  "key": "openrouter-moonshotai-kimi-k2",
  "driver": "provider-bridge",
  "endpoint": "https://openrouter.ai/api/v1/chat/completions",
  "model": "moonshotai/kimi-k2",
  "credentialsEnv": "OPENROUTER_API_KEY",
  "parser": "kimi",
  "authScheme": "bearer",
  "requestTimeoutSec": 600
}
```

Set `reasoningEffort` when you need Moonshot-style extended thinking (`thinking.type=enabled` on the wire).

## Observed behavior

| Capability | Result |
|------------|--------|
| Thinking exposed | no (no `thinking_delta` / transcript thinking row in this run) |
| Tool round-trip (`read_file`) | ok |
| Final assistant text | Jakarta cuacanya 32°C, lembab, dan berawan sebagian. |
| Tool calls (order) | `read_file` → `sapaloq_stop` |

### Turn shape (transcript)

```text
user
  → tool read_file (native id: functions.read_file:0, args: {"path":"jakarta-weather.txt"})
  → tool result (completed): 32°C, humid, partly cloudy
  → assistant text (delta patches, then snapshot)
  → tool sapaloq_stop (model-initiated stop)
```

### Wire / UI notes

- Tool calls arrive as **native OpenAI-compatible** `tool_calls` (not inline JSON leak).
- `tool_status` on transcript rows: `completed` (not `done`).
- Assistant reply streams via **transcript delta** ops (`append_text` on `1-pending-text`), then consolidates to snapshot row `1-text-2`.
- Orchestrator emits multiple identical transcript snapshots before boundaries; raw jsonl is authoritative (see capture fix in `test/openrouter/report_test.go`).

## Verdict

**Usable for SapaLOQ tool loops** on OpenRouter (native tool call + tool result + assistant reply). Thinking/reasoning was not visible on the wire for this run — enable `reasoningEffort` and re-characterize if you need thinking semantics.

## Reproduce

```bash
export SAPALOQ_OPENROUTER_E2E=1
export OPENROUTER_API_KEY=sk-or-...
export OPENROUTER_MODELS='moonshotai/kimi-k2|kimi|bearer|'
make openrouter-characterize
```
