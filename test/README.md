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

Provider settings live in `test/openrouter/config.go` (env gate, paths, defaults).

## Blackbox characterize suite (same probe, Blackbox gateway)

`test/blackbox/` mirrors the OpenRouter characterize suite against Blackbox's
OpenAI-compatible `/chat/completions` endpoint. Same two-turn weather probe,
stream + nostream, tool_choice fallback, and probe contract markers.

```bash
export BLACKBOX_API_KEY=sk-...
export BLACKBOX_MODELS='blackboxai/anthropic/claude-sonnet-4.5|openai|bearer|'
make blackbox-characterize
```

Or directly:

```bash
SAPALOQ_BLACKBOX_CHARACTERIZE_E2E=1 \
  BLACKBOX_API_KEY=sk-... \
  BLACKBOX_MODELS='blackboxai/anthropic/claude-sonnet-4.5|openai|bearer|' \
  go test ./test/blackbox/... -v -count=1 -timeout 15m -run Characterize
```

**Env contract**

| Variable | Required | Purpose |
|----------|----------|---------|
| `SAPALOQ_BLACKBOX_CHARACTERIZE_E2E=1` | yes | opt-in gate |
| `BLACKBOX_API_KEY` | yes | bearer token (override env name via `BLACKBOX_CREDENTIALS_ENV`) |
| `BLACKBOX_MODELS` | yes | comma-separated entries; same pipe format as OpenRouter |
| `BLACKBOX_ENDPOINT` | no | default `https://api.blackbox.ai/v1/chat/completions` |

**Output:** `<repo>/tmp/blackbox/<model-slug>-{stream,nostream}.jsonl` + `.md` transcript.
**Docs:** `docs/providers/blackbox-<model-slug>-{stream,nostream}.md`.

Provider settings live in `test/blackbox/config.go`.

## 9router characterize suite (local Cursor proxy gateway)

`test/9router/` mirrors the same probe against a running **9router** instance
(OpenAI-compatible `/v1/chat/completions`, default `http://127.0.0.1:20128`).
Uses the same env contract as SapaLOQ `cursor-9router` provider entries
(`NROUTER_API_KEY`, model aliases like `cu/default`).

```bash
export NROUTER_API_KEY=...
export NROUTER_MODELS='cu/default|openai|bearer|,codex-cursor|openai|bearer|'
make 9router-characterize
```

Or directly:

```bash
SAPALOQ_9ROUTER_CHARACTERIZE_E2E=1 \
  NROUTER_API_KEY=... \
  NROUTER_MODELS='cu/default|openai|bearer|' \
  go test ./test/9router/... -v -count=1 -timeout 15m -run Characterize
```

**Env contract**

| Variable | Required | Purpose |
|----------|----------|---------|
| `SAPALOQ_9ROUTER_CHARACTERIZE_E2E=1` | yes | opt-in gate |
| `NROUTER_API_KEY` | yes | bearer token (override env name via `NROUTER_CREDENTIALS_ENV`) |
| `NROUTER_MODELS` | yes | comma-separated entries; same pipe format as OpenRouter |
| `NROUTER_ENDPOINT` | no | default `http://127.0.0.1:20128/v1/chat/completions` |

**Output:** `<repo>/tmp/9router/<model-slug>-{stream,nostream}.jsonl` + `.md` transcript.
**Docs:** `docs/providers/9router-<model-slug>-{stream,nostream}.md`.

Provider settings live in `test/9router/config_test.go` (Go package `nrouter_test` — identifiers cannot start with a digit).

## Gemini characterize suite (Google generateContent API)

`test/gemini/` probes Gemini models via the **Generative Language API**
(`POST …/v1beta/models/{model}:generateContent` and `:streamGenerateContent?alt=sse`).
Auth: **`X-goog-api-key`** header. API key from **`GOOGLE_API_KEY`** or **`GEMINI_API_KEY`**
(Google SDK convention: `GOOGLE_API_KEY` wins when both are set — see
[Using Gemini API keys](https://ai.google.dev/gemini-api/docs/api-key)).

**Note:** Google ships an official Go client (`google.golang.org/genai`) for app
code; this characterize suite intentionally uses raw `net/http` (same as
OpenRouter/Blackbox/9router) so JSONL captures the exact wire request/response
for provider docs — not the SDK's typed view.

Same weather tool round-trip probe; thinking is requested via
`generationConfig.thinkingConfig` (`thinkingLevel` + `includeThoughts`). Wire
often exposes `thoughtsTokenCount` / `thoughtSignature` without visible thought
text — probe contract records that separately. **Turn 2 replays turn-1 model parts
verbatim** (`thoughtSignature`, `functionCall.id`, thought text) per
[Thought signatures](https://ai.google.dev/gemini-api/docs/thought-signatures).

```bash
export GEMINI_API_KEY=...   # or GOOGLE_API_KEY
export GEMINI_MODELS='gemini-flash-latest|gemini|x-goog-api-key|low'
make gemini-characterize
```

Or directly:

```bash
SAPALOQ_GEMINI_CHARACTERIZE_E2E=1 \
  GEMINI_API_KEY=... \
  GEMINI_MODELS='gemini-flash-latest|gemini|x-goog-api-key|low' \
  go test ./test/gemini/... -v -count=1 -timeout 15m -run Characterize
```

**Env contract**

| Variable | Required | Purpose |
|----------|----------|---------|
| `SAPALOQ_GEMINI_CHARACTERIZE_E2E=1` | yes | opt-in gate |
| `GOOGLE_API_KEY` or `GEMINI_API_KEY` | yes | Google AI API key (`GOOGLE_API_KEY` preferred; override name via `GEMINI_CREDENTIALS_ENV`) |
| `GEMINI_MODELS` | yes | comma-separated entries; same pipe format |
| `GEMINI_ENDPOINT` | no | API base (default `https://generativelanguage.googleapis.com/v1beta`) |

**Output:** `<repo>/tmp/gemini/<model-slug>-{stream,nostream}.jsonl` + `.md` transcript.
**Docs:** `docs/providers/gemini-<model-slug>-{stream,nostream}.md`.

Provider settings live in `test/gemini/config_test.go`.

The characterize suite is the regression oracle for the production
**`gemini-bridge`** driver (`internal/bridges/gemini/`). After bridge changes,
re-run `make gemini-characterize` with a live `GEMINI_API_KEY`.

## llama.cpp characterize suite (local llama-server)

`test/llamacpp/` probes **llama-server** OpenAI-compatible
`POST /v1/chat/completions` (and SSE stream). Default target is direct
**`http://127.0.0.1:8080`** (upstream llama.cpp default). Override with
`LLAMACPP_ENDPOINT` when your server uses another host/port (e.g. `:16285`).

Contract source: `/opt/llama.cpp/tools/server/README.md` (deployed locally via
`llama-auto` / systemd on `:16285`). Same weather `get_weather` two-turn probe
as other characterize suites; optional `LLAMACPP_API_KEY` / `LLAMA_API_KEY` when
`llama-server` runs with `--api-key`.

```bash
export LLAMACPP_MODELS='your-model-id|openai|bearer|'
make llamacpp-characterize
```

**Env contract**

| Variable | Required | Purpose |
|----------|----------|---------|
| `SAPALOQ_LLAMACPP_CHARACTERIZE_E2E=1` | yes | opt-in gate |
| `LLAMACPP_MODELS` | yes | comma-separated `model\|parser\|auth\|reasoningEffort` |
| `LLAMACPP_ENDPOINT` | no | default `http://127.0.0.1:8080/v1/chat/completions` |
| `LLAMA_SERVER_URL` | no | alias base URL (same normalization as endpoint) |
| `LLAMACPP_API_KEY` / `LLAMA_API_KEY` | no | Bearer when server uses `--api-key` |

**Output:** `<repo>/tmp/llamacpp/<model-slug>-{stream,nostream}.jsonl` + `.md`.
**Docs:** `docs/providers/llamacpp-<model-slug>-{stream,nostream}.md`.

Provider settings live in `test/llamacpp/config_test.go`.

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
