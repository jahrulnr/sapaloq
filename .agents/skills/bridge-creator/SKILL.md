---
name: bridge-creator
description: Creates SapaLOQ LLM bridge drivers and characterize test suites (test/{provider}/). Use when adding a new gateway (gemini-bridge, openrouter, blackbox, 9router), wiring internal/bridges/*, config schema, WireMeta replay, or test/*/characterize probes.
---

# SapaLOQ bridge creator

End-to-end workflow for a **new LLM gateway** in SapaLOQ: characterize first (wire truth), then production bridge, then config/docs.

## Decision: dedicated driver vs provider-bridge

| Use **provider-bridge** (+ parser hint) | Use **new `internal/bridges/{name}/`** |
|----------------------------------------|----------------------------------------|
| OpenAI Chat Completions-shaped wire | Native API (Gemini `generateContent`, Codex app-server, Cursor protobuf) |
| Bearer or Anthropic `x-api-key` on same HTTP patterns | Custom auth header (`X-goog-api-key`, etc.) |
| Messages = roles + string content | `contents`/`parts`, opaque replay blobs (`thoughtSignature`) |

When unsure: run a **characterize probe** first; if request/response cannot map cleanly to provider `wire.go`, use a dedicated driver (mirror `codex/`, `gemini/`).

## Workflow checklist

```
- [ ] 1. Characterize suite (test/{provider}/)
- [ ] 2. Live probe + tmp/*.jsonl + docs/providers/*
- [ ] 3. Production bridge (internal/bridges/{name}/)
- [ ] 4. WireMeta / replay if multi-turn tools need opaque wire
- [ ] 5. Config schema + example + main.go Register
- [ ] 6. Docs sync (AGENTS.md table) + STATUS.md
- [ ] 7. go test ./... green
```

---

## Step 1 — Characterize suite

Copy pattern from [`test/openrouter/`](../../test/openrouter/) or [`test/gemini/`](../../test/gemini/).

**Required files** (package `{name}_test`; Go package name cannot start with a digit — use e.g. `nrouter_test` for `test/9router/`):

| File | Purpose |
|------|---------|
| `config_test.go` | Env gate, credentials, endpoint URL builder, tmp/doc paths |
| `harness_test.go` | Thin wrappers: `{name}APIKey()`, `require{Name}Models()` |
| `raw_client_test.go` | Raw `net/http` probe (no orchestrator imports) |
| `report_test.go` | `CharacterReport`, probe contract (`yes`/`no`/`unknown`) |
| `transcript_test.go` | Human `.md` transcript |
| `docgen_test.go` | Refresh `docs/providers/{prefix}-*.md` |
| `characterize_test.go` | Stream + nostream subtests per model |

**Config struct** (`config_test.go`):

```go
var provider = Config{
    Name: "gemini", DisplayName: "Gemini",
    GateEnv: "SAPALOQ_GEMINI_CHARACTERIZE_E2E",
    CredEnv: "GEMINI_API_KEY", ModelsEnv: "GEMINI_MODELS",
    DefaultEndpoint: "https://...",
    TmpSubdir: "gemini", DocKeyPrefix: "gemini",
    MakeTarget: "gemini-characterize",
    TestDir: "test/gemini",
}
```

**Probe contract** (every provider): two-turn weather `get_weather` round-trip; record separately:

- request support vs wire exposure (`thinking_request_support` vs `thinking_wire_exposed`)
- `tool_choice` / gateway-specific tool config fallback
- provider-specific replay markers (e.g. Gemini `thought_signature_replay`)

**Makefile target:**

```makefile
gemini-characterize:
	SAPALOQ_GEMINI_CHARACTERIZE_E2E=1 \
	GEMINI_MODELS='model|parser|auth|effort' \
		go test ./test/gemini/... -v -count=1 -timeout 15m -run Characterize
```

Document env in [`test/README.md`](../../test/README.md).

---

## Step 2 — Production bridge package

Location: [`internal/bridges/{name}/`](../../internal/bridges/).

**Must implement** [`bridge.Bridge`](../../internal/bridge/bridge.go):

```go
type Bridge interface {
    ID() string                    // must match config driver string
    Caps() BridgeCaps
    Complete(ctx, Request) (<-chan StreamEvent, error)
}
```

**Typical files:**

| File | Role |
|------|------|
| `bridge.go` | `Complete()` → goroutine, map wire → `StreamEvent` |
| `wire.go` | HTTP, SSE, retries, idle timeout |
| `types.go` | Upstream request/response structs |
| `messages.go` | `bridge.Message[]` ↔ upstream format |
| `handlers.go` | Parse chunks → thinking/text/tool |
| `detect.go` | Endpoint normalization, stream URL |
| `register.go` | `Register(reg *bridge.Registry, entry config.LLMBridge)` |
| `*_test.go` | Unit tests from characterize JSONL fixtures |

**Register** in [`cmd/sapaloq-core/main.go`](../../cmd/sapaloq-core/main.go):

```go
if entry.Driver == "gemini-bridge" {
    gemini.Register(reg, entry)
}
```

**StreamEvent mapping:**

| Wire | `StreamEvent` |
|------|---------------|
| thinking | `EventThinkingDelta` |
| assistant text | `EventResponseDelta` |
| tool call | `EventToolCall` |
| failure | `EventError` |
| end | `EventDone` |

Reuse patterns from [`internal/bridges/provider/bridge.go`](../../internal/bridges/provider/bridge.go) and [`internal/bridges/codex/bridge.go`](../../internal/bridges/codex/bridge.go).

---

## Step 3 — Multi-turn tools + WireMeta (when needed)

If upstream requires **opaque replay** (Gemini `thoughtSignature`, native `functionCall.id`):

1. Bridge emits `StreamEvent.WireMeta` on tool-call events (JSON blob of upstream model parts).
2. Persist on assistant turn: extend [`chat.Turn`](../../internal/store/chat/store.go) with `WireMeta`; allow empty `Content` when `WireMeta` set.
3. Replay: [`actorTurnsToMessages`](../../internal/core/orchestrator/prompt.go) passes `WireMeta` on `bridge.Message`.
4. Bridge `messages.go` prefers `WireMeta` over reconstructed text.

**Do not** strip opaque fields on replay — fix at bridge or replay mapper, not UI sanitizers (see [`AGENTS.md`](../../AGENTS.md) KISS / boundaries).

---

## Step 4 — Config + schema

Update in **same change**:

| File | Change |
|------|--------|
| [`schema/config.schema.json`](../../schema/config.schema.json) | `driver` enum, `parser`, `authScheme` |
| [`config/config.example.json`](../../config/config.example.json) | Example provider entry |
| [`internal/config/load.go`](../../internal/config/load.go) | Document fields |

Example entry shape:

```json
{
  "key": "gemini",
  "driver": "gemini-bridge",
  "endpoint": "https://generativelanguage.googleapis.com/v1beta",
  "model": "gemini-flash-latest",
  "credentialsEnv": "GEMINI_API_KEY",
  "reasoningEffort": "low",
  "requestTimeoutSec": 600,
  "stream": true
}
```

---

## Step 5 — Docs (required)

Follow AGENTS.md doc-sync table. Minimum for a new bridge:

| Change area | Docs |
|-------------|------|
| New bridge package | `docs/{NAME}-BRIDGE.md` (or section in `docs/BRIDGE.md`) |
| Provider wire notes | `docs/providers/README.md` + generated pages |
| Always | `docs/STATUS.md` (status row + session bullet) |

Characterize docs are **generated** from live runs — do not hand-edit behavior claims without re-running probe.

---

## Step 6 — Tests

**Required before merge:**

```bash
go build ./...
go vet ./...
go test ./...
go test ./test/{provider}/... -count=1    # unit (skips without gate)
# optional live:
make {provider}-characterize
```

**Contract tests must cover:** happy tool loop, probe fallbacks, replay edge (missing opaque fields), stream vs nostream, cancellation/timeouts where feasible.

---

## Naming conventions

| Item | Pattern | Example |
|------|---------|---------|
| Driver ID / config `driver` | `{name}-bridge` | `gemini-bridge` |
| Package dir | `internal/bridges/gemini` | |
| Test dir | `test/gemini` or `test/9router` | |
| Test package | valid Go identifier | `gemini_test`, `nrouter_test` |
| Config file | `test/{provider}/config_test.go` | not `config.go` (package `*_test` rule) |
| Make target | `{provider}-characterize` | |
| Docs slug | `docs/providers/{prefix}-{model}-{stream\|nostream}.md` | |

---

## Anti-patterns

- Adding a fourth parser to `provider/wire.go` when wire shape diverges (creates permanent branches).
- Characterize suite importing orchestrator — keep raw `net/http`.
- Persist reordering transcript at write time — append wire order; map on replay only.
- Skipping `docs/STATUS.md` or schema updates.
- Happy-path-only characterize tests.

---

## Reference

Detailed file map and existing drivers: [reference.md](reference.md)
