# test/

Integration and end-to-end tests will live here (future).

## OpenRouter characterize suite (non-native models via OpenAI-compatible gateway)

`test/openrouter/` probes real OpenRouter models listed in `OPENROUTER_MODELS`
using raw `net/http` POST to `/chat/completions` (no SapaLOQ orchestrator).
Each run forces a weather scenario (`get_weather` fake tool → tool result →
short answer) in **both wire modes**: `stream: true` (SSE) and `stream: false`
(JSON body). If upstream rejects `tool_choice`, the probe retries with
`tools` only (no `tool_choice` field). **Raw output:** each mode writes
`tmp/openrouter/<model-slug>-stream.jsonl` or `...-nostream.jsonl` (repo root).
A derived summary is logged to test output only.

Tests `t.Skip` unless opted in, so default `go test ./...` stays offline-green.

```bash
export OPENROUTER_API_KEY=sk-or-...
export OPENROUTER_MODELS='anthropic/claude-3.5-haiku|openai|bearer|high,moonshotai/kimi-k2|kimi|bearer|medium'
make openrouter-characterize
```

Or directly:

```bash
SAPALOQ_OPENROUTER_E2E=1 \
  OPENROUTER_API_KEY=sk-or-... \
  OPENROUTER_MODELS='anthropic/claude-3.5-haiku|openai|bearer|high' \
  go test ./test/openrouter/... -v -count=1 -timeout 15m -run Characterize
```

**Env contract**

| Variable | Required | Purpose |
|----------|----------|---------|
| `SAPALOQ_OPENROUTER_E2E=1` | yes | opt-in gate |
| `OPENROUTER_API_KEY` | yes | bearer token for OpenRouter |
| `OPENROUTER_MODELS` | yes | comma-separated entries; no hardcoded defaults |
| `OPENROUTER_ENDPOINT` | no | default `https://openrouter.ai/api/v1/chat/completions` |

**Output:** `<repo>/tmp/openrouter/<model-slug>-stream.jsonl` and
`<model-slug>-nostream.jsonl` — one JSON object per line
(`phase`: request, SSE chunk / JSON response, fallback marker, HTTP error, etc.).

**Transcript:** `<model-slug>-{stream,nostream}.md` beside each JSONL — all turn fields always shown (`thinking: (not on wire)` when empty). **Probe contract** lists request support vs wire exposure separately (`thinking_request_support` vs `thinking_wire_exposed`, `reasoning_tokens_observed`).

**Docs:** each live run also refreshes `docs/providers/openrouter-<model-slug>.md`
(field notes for tool/thinking behavior). Index: [docs/providers/README.md](../docs/providers/README.md).

**`OPENROUTER_MODELS` format** (pipe-delimited per entry):

```text
model|parser|authScheme|reasoningEffort
```

If `reasoningEffort` is omitted, the probe defaults to **`low`** and sends `thinking: {type: enabled}`. When upstream rejects either field, turn 1 retries with both unset and records **`reasoning_effort_support`** / **`thinking_support`** (`yes` / `no` / `unknown`) beside **`tool_choice_support`**.

OpenRouter's endpoint is OpenAI-shaped; set `parser` and `authScheme` explicitly
for Claude/Kimi models (auto-detect from model name can pick the wrong wire).

## Live simulate suite (real LLM, mocked sub-agents)

`internal/core/orchestrator/simulate_live_test.go` drives the orchestrator /
planner / agent loop against a real OpenAI-compatible provider (Blackbox) in one
role at a time, mocking the other roles and tooling. The tests `t.Skip` unless
opted in, so the default `go test ./...` stays offline-green.

Run them live via the Makefile (recommended):

```bash
export BLACKBOX_API_KEY=sk-...   # token comes from the environment, never the Makefile
make simulate-live               # all three modes

# one mode:
make simulate-live SIMULATE_RUN=TestSimulateOrchestratorPlannerAgentRoundTrip

# override the model/endpoint:
make simulate-live BLACKBOX_MODEL=blackboxai/minimax/minimax-m2.7
```

Or directly with `go test`:

```bash
SAPALOQ_BLACKBOX_E2E=1 \
  BLACKBOX_API_KEY=sk-... \
  BLACKBOX_MODEL=blackboxai/anthropic/claude-sonnet-4.5 \
  BLACKBOX_ENDPOINT=https://api.blackbox.ai/v1 \
  go test ./internal/core/orchestrator -run TestSimulate -v
```

- `BLACKBOX_ENDPOINT` may be a bare base URL (`…/v1`); it is auto-completed to
  `/chat/completions`.
- Override the token env var name with `BLACKBOX_CREDENTIALS_ENV` (default
  `BLACKBOX_API_KEY`).
- Mode 1 is the live regression for the orchestrator.md spawn-before-acknowledge fix.
