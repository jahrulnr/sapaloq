# SapaLOQ — LLM Bridge Drivers

> **Brain bridge drivers** — connect companion/sub-agent LLM calls to external APIs & IDEs.
> **cursor-bridge** = driver pertama; Claude/OpenAI-compatible built-in later (9router-*pattern*, bukan adopt 9router sebagai third-party).
> Last updated: 2026-06-19

Related: [DRIVER.md](./DRIVER.md) · [ORCHESTRATOR.md](./ORCHESTRATOR.md) · [LIMITATIONS.md](./LIMITATIONS.md) · [RE-CURSOR-THINKING-TOOLS.md](./RE-CURSOR-THINKING-TOOLS.md)

> **Thinking/tools wire truth:** [RE-CURSOR-THINKING-TOOLS.md](./RE-CURSOR-THINKING-TOOLS.md) (L0 only). Jangan derive thinking behavior dari 9router — adapter itu skip/collapse channel thinking Cursor.

---

## Dua keluarga driver

SapaLOQ punya **dua registry driver** terpisah — jangan dicampur:

| Family | Package | Pilih via | Contoh |
|--------|---------|-----------|--------|
| **Platform** | `internal/drivers/` | `os.json` + detect | `gnome`, `kde`, `windows` |
| **LLM bridge** | `internal/bridges/` | `config.json` → `llmBridge.driver` | `cursor-bridge`, `openai-compat`, `claude-compat`, `local-llama` |

```
sapaloq-core
├── driver/          platform registry (os.json)
├── drivers/         gnome, kde, …
├── bridge/          LLM bridge registry
├── bridges/         cursor-bridge, openai-compat, claude-compat, local-llama, …
└── parse/
    ├── tools/       format-specific tool call parsers
    └── thinking/    format-specific reasoning/thinking parsers
```

Platform driver = desktop automation. LLM bridge driver = **companion brain** + optional sub-agent remote inference.

---

## cursor-bridge sebagai driver resmi

**cursor-bridge** bukan dependency runtime ke repo `jahrulnr/cursor-bridge` atau 9router — SapaLOQ **mengadopsi kontrak** (schema, coercion, leak markers) sebagai driver built-in.

| | SapaLOQ | cursor-bridge monorepo | 9router |
|--|---------|------------------------|---------|
| Role | Runtime driver di `sapaloq-core` | Source of truth schema + test vectors | Legacy proxy pattern (referensi transport) |
| Dependency | Embed/sync schema at build | Dev reference | **Tidak** third-party dep |
| Tool poisoning | Parser + sanitizer layer | `leakMarkers`, nativeTools | Partial (transport only) |

Reference artifacts (dev / regen):

- `cursor-bridge.schema.json` — aliases, leak markers, coercion maps
- `cursor-agent-toolcall-spec.json` — 49 ToolCall variants (mirror UI only)

Orchestrator & sub-agents call unified interface `bridge.Completion()` — driver handles wire format.

---

## Roadmap compat (built-in, bukan fork 9router)

Next development **may** ship first-party bridges dengan pola mirip 9router (OpenAI `/v1/chat/completions`, Claude Messages API) — **tanpa** require user install 9router.

| Driver ID | Wire | Tool poisoning | Default parsers |
|-----------|------|----------------|-----------------|
| `cursor-bridge` | Cursor `api2.cursor.sh` / agent proto | **High** — fake tool names | `tools:cursor`, `thinking:cursor` |
| `openai-compat` | OpenAI-compatible HTTP | **Low** — usually clean | `tools:openai`, `thinking:openai` |
| `claude-compat` | Anthropic Messages API | **Low–medium** | `tools:claude`, `thinking:claude` |
| `local-llama` | llama.cpp / sidecar | N/A (local schema) | configurable |

**Community bridges:** compile-time registry (sama seperti platform drivers). Contrib driver baru untuk IDE/CLI dengan behavior mirip Cursor (Gemini plugin, Copilot VSCode, custom MCP gateway) tanpa merge ke core kecuali maintained.

---

## Tool poisoning matrix (expectation)

| Backend | Tool poisoning? | Notes |
|---------|-----------------|-------|
| OpenRouter / raw OpenAI API | Usually **no** | Standard `tool_calls` |
| Claude API (direct) | Usually **no** | `tool_use` blocks |
| **Cursor API** | **Yes** | Fake names (`dir_list`, `file_write`, …) → need coercion |
| **Kimi** (via Cursor path) | **Yes** | Inline tokens + different inline format |
| Gemini (some paths) | **Possible** | Treat as cursor-like until probed |
| Copilot VSCode / similar IDE agents | **Possible** | May leak non-native tool names — use cursor-like sanitizer |

**boundary-guard** + **context-scaler** tetap jalan di atas parsed tool calls — poisoning = wire-format problem, bukan orchestrator problem.

---

## Parser layer (tools)

Satu **canonical internal model** (`parse.ToolCall`) — setiap driver pilih parser:

```go
type ToolParser interface {
    ID() string // "openai" | "claude" | "kimi" | "cursor"
    ParseStream(chunk []byte) ([]ToolCall, error)
    ParseMessage(msg Message) ([]ToolCall, error)
    Serialize(calls []ToolCall) (any, error) // round-trip for driver
}
```

| Parser | Wire shape | Known quirks |
|--------|------------|--------------|
| **cursor** | Protobuf `THINKING_TEXT` + `RESPONSE_TEXT` + `TOOL_CALL` | Dual channel; blob split at `</think>` — see [RE-CURSOR-THINKING-TOOLS.md](./RE-CURSOR-THINKING-TOOLS.md) |
| **openai** | `choices[].delta.tool_calls[]` | Parallel calls, `index` field |
| **claude** | `content[]` type `tool_use` | Block IDs, stop_reason `tool_use` |
| **kimi** | Inline markers in thinking/content tail | Often with Cursor Auto; not standalone API |

Driver config may override:

```json
"llmBridge": {
  "driver": "openai-compat",
  "parsers": { "tools": "openai" }
}
```

Auto-default dari `driver` ID; override untuk exotic proxy (OpenAI wire + Cursor backend).

---

## Parser layer (thinking / reasoning)

Thinking blocks **tidak** disimpan ke companion memory verbatim by default — stream to widget ring state `thinking`, strip before scribe index.

```go
type ThinkingParser interface {
    ID() string // "cursor" | "claude" | "kimi" | "openai"
    ExtractThinking(stream []byte) (public string, thinking string, err error)
    StripForMemory(text string) string
}
```

| Parser | Format | Notes |
|--------|--------|-------|
| **cursor** | `THINKING_TEXT` blob: pre/post `</think>`, optional `<\|final\|>` | **Do not** collapse like 9router — see RE doc |
| **claude** | Extended thinking `thinking` blocks | API beta headers |
| **kimi** | Inline section after thinking split | Sub-parser of cursor Auto path, not standalone |
| **openai** | `reasoning` / o-series deltas | Model-gated |

Widget **thinking** state driven by parser events — not raw provider bytes.

---

## Unified bridge interface

```go
// internal/bridge/bridge.go

type Bridge interface {
    ID() string
    Capabilities() BridgeCaps // toolPoisoning, streaming, vision, …
    Complete(ctx context.Context, req CompletionRequest) (<-chan StreamEvent, error)
}

type BridgeCaps struct {
    ToolPoisoning   bool
    NativeTools     []string  // upstream leak catalog — for coercion/sanitizer ONLY, not sub-agent tool list
    Streaming       bool
    Vision          bool
    ThinkingMode    bool
}
```

**`NativeTools` semantics:** names Cursor/Kimi may **hallucinate** in thinking/content. Used by:

1. **Leak detection** (`analyzeLeak`) — content + thinking scan
2. **Coercion mapping** — fake name → declared bridge tool via `cursor-bridge.schema.json` aliases
3. **Not** exposed to sub-agent as callable tools — sub-agent tools come from `subAgents.roles[].allowedTools` only

Wrong interpretation (exposing NativeTools to task-runner) = double tool namespace bug.

### Schema sync at build

```
cursor-bridge monorepo schema/cursor-bridge.schema.json
    → cp to sapaloq-core/embed/cursor-bridge.schema.json
    → //go:embed in internal/bridges/cursor/schema.go
```

CI step: `make sync-cursor-schema` (diff fails build if drift). **Not** git submodule at runtime.

### `local-llama` (fallback only — spec deferred)

Primary brain: **`cursor-bridge`** (or `openai-compat`). `local-llama` = offline fallback when `llmBridge.fallback.on` matches.

MVP stub: HTTP OpenAI-compatible to `127.0.0.1:8080/v1` (llama-server sidecar). Full spec (GGUF path, `n_gpu_layers`) → post-M8 when fallback is needed.

### Registration (mirror platform drivers)

```go
// internal/bridges/cursor/register.go
func init() { bridge.Register(&CursorBridgeFactory{}) }
```

---

## `config.json` — `llmBridge`

```json
{
  "llmBridge": {
    "driver": "cursor-bridge",
    "endpoint": "https://api2.cursor.sh",
    "model": "default",
    "parsers": {
      "tools": "cursor",
      "thinking": "cursor"
    },
    "credentialsEnv": "SAPALOQ_CURSOR_TOKEN",
    "coercion": {
      "enabled": true,
      "schemaPath": "~/.config/sapaloq/bridge/cursor-bridge.schema.json"
    },
    "fallback": {
      "driver": "local-llama",
      "on": ["auth_error", "offline"]
    }
  }
}
```

| Key | Purpose |
|-----|---------|
| `driver` | Active LLM bridge driver ID |
| `parsers.tools` / `parsers.thinking` | Override auto parser |
| `coercion.enabled` | Fake-tool sanitizer (cursor-like drivers) |
| `fallback` | Offline / auth fail → local brain |

Credentials **never** in config.json — env or secret file path only.

Agent may `/settings set llmBridge.driver openai-compat` — no settings UI.

---

## Sub-agent & orchestrator usage

| Role | Typical bridge |
|------|----------------|
| Orchestrator (widget) | `llmBridge.driver` default |
| task-runner sub-agent | Same or `nodes` row override |
| research (web) | Often `openai-compat` / `claude-compat` — lower poisoning |
| scribe | Local or cheap compat API |

Remote node **does not** share bridge credentials unless comm spec declares — token stays on orchestrator machine, remote gets **delegated spawn** with pre-built messages only.

### Worker (`cursor-agent`) relationship — not intercept

SapaLOQ widget = **parallel independent session** via `cursor-bridge` (or compat driver). It does **not** tap or proxy live `cursor-agent` CLI traffic.

| Path | When |
|------|------|
| **Parallel** (default) | Orchestrator + sub-agents use SapaLOQ's own bridge session; memory in `companion.db` |
| **Handoff** (explicit) | User or orchestrator writes `bridge/handoff/<uuid>.json` → worker consumes once |

Implication for milestones: M1–M3 need bridge session + SQLite only; deep cursor-agent mirror UI is M8–M9 polish, not M1 blocker.

---

## Explicit non-goals

| Idea | Why |
|------|-----|
| Bundle 9router as required dep | User chose less-deps single binary |
| One parser for all providers | Formats genuinely differ — wrong parser = silent tool loss |
| Sync cursor-bridge memory to companion | Isolation — handoff packet only |
| Runtime `.so` bridge plugins | Compile-time registry for MVP; revisit later |

---

## Implementation order

| Step | Deliverable |
|------|-------------|
| 1 | `bridge.Registry` + `Bridge` interface |
| 2 | `parse/tools/openai` + `parse/tools/claude` (unit tests) |
| 3 | `bridges/openai-compat` MVP (companion brain TBD path) |
| 4 | `parse/tools/cursor` + `parse/thinking/cursor` |
| 5 | `bridges/cursor-bridge` + embedded schema sync |
| 6 | `parse/tools/kimi`, `parse/thinking/kimi` |
| 7 | `bridges/claude-compat` |
| 8 | Community bridge template + docs |

---

## Related repos (reference only)

| Repo | Use for SapaLOQ |
|------|-----------------|
| [jahrulnr/cursor-bridge](https://github.com/jahrulnr/cursor-bridge) | Schema, coercion test vectors, proto-lab |
| 9router | Transport pattern reference — **not** runtime dep |
| `cursor-agent-toolcall-spec.json` | ToolCall variant map for mirror UI |
