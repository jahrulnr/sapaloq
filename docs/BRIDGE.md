# SapaLOQ — LLM Bridge Drivers

> **Brain bridge drivers** — connect companion/sub-agent LLM calls to external APIs & IDEs.
> **cursor-bridge** = driver pertama; Claude/OpenAI-compatible built-in later (9router-*pattern*, bukan adopt 9router sebagai third-party).
> Last updated: 2026-06-22 (runtime bridge/vault paths moved to ~/SapaLOQ)

Related: [DRIVER.md](./DRIVER.md) · [ORCHESTRATOR.md](./ORCHESTRATOR.md) · [LIMITATIONS.md](./LIMITATIONS.md) · [RE-CURSOR-THINKING-TOOLS.md](./RE-CURSOR-THINKING-TOOLS.md)

> **Thinking/tools wire truth:** [RE-CURSOR-THINKING-TOOLS.md](./RE-CURSOR-THINKING-TOOLS.md) (L0 only). Jangan derive thinking behavior dari 9router — adapter itu skip/collapse channel thinking Cursor.

---

## Dua keluarga driver

SapaLOQ punya **dua registry driver** terpisah — jangan dicampur:


| Family         | Package             | Pilih via                          | Contoh                                                           |
| -------------- | ------------------- | ---------------------------------- | ---------------------------------------------------------------- |
| **Platform**   | `internal/drivers/` | `os.json` + detect                 | `gnome`, `kde`, `windows`                                        |
| **LLM bridge** | `internal/bridges/` | `config.json` → `llmBridge.driver` | `cursor-bridge`, `openai-compat`, `claude-compat`, `local-llama` |

### Per-request timeout

Each provider entry bounds a single inference request via
`llmBridge.providers[].requestTimeoutSec` (default **600s**, resolved by
`config.LLMBridge.RequestTimeout()`). It applies to **both** bridge families:
the provider-bridge (`WireOptions.Timeout` → `buildHTTPRequest`
`context.WithTimeout`) used by tokenrouter/OpenAI/Claude/Kimi, and the
cursor-bridge (`StreamOptions`/`AgentStreamOptions`). The old hardcoded 120s
wire default truncated long sub-agent steps (large file generation) into a bare
"context deadline exceeded"; **both** bridges now rewrite that error to name the
timeout and the knob to raise it (`explainStreamError`).


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

**cursor-bridge** bukan dependency runtime ke repo `jahrulnr/cursor-bridge` atau 9router — SapaLOQ **mengadopsi kontrak** (schema, aliases, coercion) sebagai driver built-in.


|                    | SapaLOQ                          | cursor-bridge monorepo                   | 9router                                    |
| ------------------ | -------------------------------- | ---------------------------------------- | ------------------------------------------ |
| Role               | Runtime driver di `sapaloq-core` | Source of truth schema + test vectors    | Legacy proxy pattern (referensi transport) |
| Dependency         | Embed/sync schema at build       | Dev reference                            | **Tidak** third-party dep                  |
| Unknown tool calls | **Vault** JSONL review log       | Schema aliases + test vectors            | Partial (transport only)                   |
| Thinking text      | Stream as-is; no leak filter     | `leakMarkers` in schema (reference only) | Collapses pre-tag thinking                 |


Reference artifacts (dev / regen):

- `cursor-bridge.schema.json` — aliases, nativeTools, kimiTokens, coercion maps
- `cursor-agent-toolcall-spec.json` — 49 ToolCall variants (mirror UI only)

Orchestrator & sub-agents call unified interface `bridge.Complete()` — driver handles wire format.

---

## Vault (undeclared tool calls)

When Cursor or another provider emits a **structured tool call** (protobuf `TOOL_CALL` or Kimi inline) that is not on the companion **declared surface**, SapaLOQ appends a JSONL row — **no blocking**, stream continues.

**Path:** `~/SapaLOQ/vault/tool-calls.jsonl`

**CLI:**

```bash
sapaloq-core vault list --limit 20
sapaloq-core vault stats
```


| `reason`           | Meaning                                                           |
| ------------------ | ----------------------------------------------------------------- |
| `undeclared`       | Resolved name known upstream but not in `llmBridge.declaredTools` |
| `unknown_upstream` | Name not in schema `nativeTools` + aliases (truly foreign)        |


**Not vaulted:** tool names mentioned in thinking or chat text — aliases already group upstream names internally; filtering prose is unnecessary.

**Workflow:** run companion → review vault → add alias or extend `declaredTools` → regen schema from [cursor-bridge](https://github.com/jahrulnr/cursor-bridge) if needed.

```json
{
  "llmBridge": {
    "declaredTools": ["read_file", "grep", "glob_file_search"]
  }
}
```

Empty `declaredTools` → vault only `unknown_upstream` calls.

---

## Roadmap compat (built-in, bukan fork 9router)

`provider-bridge` is the multi-model built-in. It speaks OpenAI Chat Completions (covers OpenAI, OpenRouter, TokenRouter, LM Studio, vLLM, etc.), Anthropic Messages, and Kimi (Moonshot). See [PROVIDER-BRIDGE.md](./PROVIDER-BRIDGE.md) for the full config reference and recipes.


| Driver ID                  | Wire                                  | Tool poisoning             | Default parsers                   |
| -------------------------- | ------------------------------------- | -------------------------- | --------------------------------- |
| `cursor-bridge`            | Cursor `api2.cursor.sh` / agent proto | **High** — fake tool names | `tools:cursor`, `thinking:cursor` |
| `provider-bridge` (openai) | OpenAI `/v1/chat/completions`         | **Low** — usually clean    | `tools:openai`, `thinking:openai` |
| `provider-bridge` (claude) | Anthropic `/v1/messages`              | **Low**                    | `tools:claude`, `thinking:claude` |
| `provider-bridge` (kimi)   | OpenAI-compatible + `thinking` flag   | **Low**                    | `tools:openai`, `thinking:openai` |
| `local-llama`              | llama.cpp / sidecar                   | N/A (local schema)         | configurable                      |


**Community bridges:** compile-time registry (sama seperti platform drivers). Contrib driver baru untuk IDE/CLI dengan behavior mirip Cursor (Gemini plugin, Copilot VSCode, custom MCP gateway) tanpa merge ke core kecuali maintained.

---

## Tool poisoning matrix (expectation)


| Backend                             | Tool poisoning? | Notes                                                    |
| ----------------------------------- | --------------- | -------------------------------------------------------- |
| OpenRouter / raw OpenAI API         | Usually **no**  | Standard `tool_calls`                                    |
| Claude API (direct)                 | Usually **no**  | `tool_use` blocks                                        |
| **Cursor API**                      | **Yes**         | Fake names (`dir_list`, `file_write`, …) → need coercion |
| **Kimi** (via Cursor path)          | **Yes**         | Inline tokens + different inline format                  |
| Gemini (some paths)                 | **Possible**    | Treat as cursor-like until probed                        |
| Copilot VSCode / similar IDE agents | **Possible**    | Vault unknown calls; coerce via cursor-like aliases      |


**boundary-guard** + **context-scaler** tetap jalan di atas parsed tool calls — wire-format aliasing + vault review, bukan prose filtering di thinking channel.

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


| Parser     | Wire shape                                               | Known quirks                                                                                              |
| ---------- | -------------------------------------------------------- | --------------------------------------------------------------------------------------------------------- |
| **cursor** | Protobuf `THINKING_TEXT` + `RESPONSE_TEXT` + `TOOL_CALL` | Dual channel; blob split at `</think>` — see [RE-CURSOR-THINKING-TOOLS.md](./RE-CURSOR-THINKING-TOOLS.md) |
| **openai** | `choices[].delta.tool_calls[]`                           | Parallel calls, `index` field                                                                             |
| **claude** | `content[]` type `tool_use`                              | Block IDs, stop_reason `tool_use`                                                                         |
| **kimi**   | Inline markers in thinking/content tail                  | Often with Cursor Auto; not standalone API                                                                |


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


| Parser     | Format                                                          | Notes                                          |
| ---------- | --------------------------------------------------------------- | ---------------------------------------------- |
| **cursor** | `THINKING_TEXT` blob: pre/post `</think>`, optional `<|final|>` | **Do not** collapse like 9router — see RE doc  |
| **claude** | Extended thinking `thinking` blocks                             | API beta headers                               |
| **kimi**   | Inline section after thinking split                             | Sub-parser of cursor Auto path, not standalone |
| **openai** | `reasoning` / o-series deltas                                   | Model-gated                                    |


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

`**NativeTools` semantics:** names Cursor/Kimi may **hallucinate** in thinking/content. Used by:

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
    "declaredTools": ["read_file", "grep", "glob_file_search"],
    "coercion": {
      "enabled": true,
      "schemaPath": "~/SapaLOQ/bridge/cursor-bridge.schema.json"
    },
    "fallback": {
      "driver": "local-llama",
      "on": ["auth_error", "offline"]
    }
  }
}
```


| Key                                  | Purpose                                                  |
| ------------------------------------ | -------------------------------------------------------- |
| `driver`                             | Active LLM bridge driver ID                              |
| `declaredTools`                      | Companion tool surface; calls outside → vault log        |
| `parsers.tools` / `parsers.thinking` | Override auto parser                                     |
| `coercion.enabled`                   | Alias fake tool names to canonical (cursor-like drivers) |
| `fallback`                           | Offline / auth fail → local brain                        |


Credentials **never** in config.json — env, `.env`, or IDE `state.vscdb` only.

### Credentials

Autoload priority (ported from `@cursor-bridge/credential-loader`):

1. `SAPALOQ_CURSOR_TOKEN` or `CURSOR_ACCESS_TOKEN` + optional `CURSOR_MACHINE_ID` in process env
2. **Shell rc** — at boot `sapaloq-core` sources `~/.bashrc` then `~/.zshrc` (Linux only) and folds the relevant, not-already-set vars (`SAPALOQ_*`, `CURSOR_*`, `BLACKBOX_*`, `OPENAI_*`, `ANTHROPIC_*`, `KIMI_*`, `MOONSHOT_*`, `OPENROUTER_*`) into the process env. This matters under systemd `--user`/XDG autostart, where there is no login shell so rc exports would otherwise be invisible. Best-effort, silent on any failure, never overrides an already-set var, short timeout so a hanging rc can't freeze startup (`internal/shellenv`).
3. `.env` in cwd, then `~/.config/sapaloq/.env`
4. `~/.config/Cursor/User/globalStorage/state.vscdb` (`cursorAuth/accessToken`, `storage.serviceMachineId`)

Override vscdb path: `CURSOR_STATE_VSCDB`. Ghost mode default on unless `CURSOR_GHOST_MODE=false`.

`sapaloq-core doctor` prints credential source. Mock stream when autoload finds no token.

Agent may `/settings set llmBridge.driver openai-compat` — no settings UI.

### Wire driver selection

The cursor bridge ships **two HTTP/2 wire drivers**. Both produce the same request
shape (headers + Connect+proto body); they differ in transport. Pick via
`SAPALOQ_WIRE_DRIVER`:


| Driver          | Implementation                                                                             | Status                                                                                                                                     |
| --------------- | ------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------ |
| `raw` (default) | `wire.StreamChatRaw` — raw frames via `http2.Framer`, mirrors cursor-proto-lab Node client | Experimental; some api2 responses require further frame-format alignment (current symptom: `FRAME_SIZE_ERROR` goaway or no response).      |
| `http2`         | `wire.StreamChat` — Go `net/http` + `http2.Transport`                                      | Stable; surfaces `unauthenticated` cleanly when token rejected, but api2 currently rejects with the same error against valid vscdb tokens. |


Live E2E (`make e2e-live`) accepts either path as long as the bridge emits
`EventError` instead of a silent `[done]`.

### Agent API path (vision + composer models)

The Agent API (`agent.v1.AgentService/Run` on
`agentn.global.api5.cursor.sh`) is the endpoint `cursor-agent` uses for chat,
composer, and vision requests. It accepts a different protobuf envelope than
the legacy `StreamUnifiedChatWithTools` path, and is the only cursor API that
supports inline image input.

SapaLOQ automatically routes a request through the Agent API path when:

1. `**SAPALOQ_AGENT_PATH=1`** — explicit operator override (used by tests).
2. **Vision content** — any message contains `data:image/...` (inline base64)
  or an `http(s)://....png|jpg|jpeg|gif|webp` URL.

The encoder/decoder live in `internal/bridges/cursor/wire/proto_agent.go`
(field numbers pinned from cursor-agent's `agent.proto` descriptor bundle
2026.06.02-8c11d9f; cross-checked against the Node reference at
`9router/open-sse/utils/cursorAgentProtobuf.js`). The HTTP/2 driver is the
same raw-framer transport used for the chat path, with a separate request
encoder and response decoder:


| Driver (Agent API) | Implementation                                                                    | Default                                                                               |
| ------------------ | --------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------- |
| `raw`              | `wire.StreamAgentRawWithRaw` — raw frames; mirrors `cursorAgent.js` byte-for-byte | Production                                                                            |
| `http2`            | `wire.StreamAgentHTTP2` — stdlib `http.Client` + `http2.Transport`                | Set via `SAPALOQ_AGENT_WIRE_DRIVER=http2` (used by unit tests against httptest mocks) |


Override the host with `CURSOR_AGENT_HOST` and the path with `CURSOR_AGENT_PATH`
(defaults: `agentn.global.api5.cursor.sh` + `/agent.v1.AgentService/Run`). Use
`SAPALOQ_WIRE_INSECURE_TLS=1` to skip certificate verification when targeting
self-signed test servers.

Live E2E: `make e2e-live SAPALOQ_AGENT_PATH=1` exercises the Agent API path
end-to-end against `api5.cursor.sh`. Currently surfaces `rst_stream code=1`
from the real server — same byte-level frame alignment work as the chat
path; vision encoder/decoder contract is independently verified by the
unit tests.

#### Privacy vs non-privacy Agent host

Cursor maintains two Agent API hosts: the **non-privacy** host
(`agentn.global.api5.cursor.sh`) is the default and accepts usage
telemetry; the **privacy** host (`agent.global.api5.cursor.sh`) is
selected when the user has ghostMode enabled in their Cursor config
(`CURSOR_GHOST_MODE` is not equal to `false`).

`wire.AgentHost(creds.GhostMode)` returns the correct hostname and
`streamLiveAgent` consults it before dialing. Operators can still force
either with `CURSOR_AGENT_HOST=...`. This mirrors
`9router/src/lib/oauth/constants/oauth.js` which defines both
`agentEndpoint` and `agentNonPrivacyEndpoint` and picks between them by
the user's privacy setting.

### Other cursor endpoints


| RPC                 | Path                                                                     | Status                                         | Notes                                                                                                                                                                                            |
| ------------------- | ------------------------------------------------------------------------ | ---------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Chat                | `aiserver.v1.ChatService/StreamUnifiedChatWithTools`                     | Encoder stable; live returns `unauthenticated` | Same `unauthenticated` issue as the chat HTTP/2 transport — byte-level frame alignment still pending against `api2.cursor.sh`.                                                                   |
| Agent (privacy)     | `agent.v1.AgentService/Run` (host `agent.global.api5.cursor.sh`)         | Routed via `wire.AgentHost(true)`              | No telemetry sent to cursor.                                                                                                                                                                     |
| Agent (non-privacy) | `agent.v1.AgentService/Run` (host `agentn.global.api5.cursor.sh`)        | Routed via `wire.AgentHost(false)` (default)   | Default host.                                                                                                                                                                                    |
| Default model nudge | `aiserver.v1.AiService/GetDefaultModelNudgeData` (host `api2.cursor.sh`) | Stub via `wire.BuildNudgeRequestBody`          | 5-byte Connect-RPC unary envelope (1-byte flag + 4-byte length=0). Server picks the default model id; not currently decoded — bridge falls back to the explicit model id supplied by the caller. |


There is no separate "plan" RPC in cursor's API. Plan mode is an
in-band behaviour triggered by the model id or the `requested_model`
parameters (e.g. `[{id:"fast", value:"true"}]`); see
`9router/open-sse/utils/cursorAgentProtobuf.js` →
`resolveRequestedModel`. SapaLOQ already passes through the model id
and parameters untouched, so plan-style routing works once the Agent
API frame alignment is resolved.

---

## Sub-agent & orchestrator usage


| Role                  | Typical bridge                                            |
| --------------------- | --------------------------------------------------------- |
| Orchestrator (widget) | `llmBridge.driver` default                                |
| task-runner sub-agent | Same or `nodes` row override                              |
| research (web)        | Often `openai-compat` / `claude-compat` — lower poisoning |
| scribe                | Local or cheap compat API                                 |


Remote node **does not** share bridge credentials unless comm spec declares — token stays on orchestrator machine, remote gets **delegated spawn** with pre-built messages only.

### Worker (`cursor-agent`) relationship — not intercept

SapaLOQ widget = **parallel independent session** via `cursor-bridge` (or compat driver). It does **not** tap or proxy live `cursor-agent` CLI traffic.


| Path                   | When                                                                                 |
| ---------------------- | ------------------------------------------------------------------------------------ |
| **Parallel** (default) | Orchestrator + sub-agents use SapaLOQ's own bridge session; memory in `companion.db` |
| **Handoff** (explicit) | User or orchestrator writes `bridge/handoff/<uuid>.json` → worker consumes once      |


Implication for milestones: M1–M3 need bridge session + SQLite only; deep cursor-agent mirror UI is M8–M9 polish, not M1 blocker.

---

## Explicit non-goals


| Idea                                   | Why                                                        |
| -------------------------------------- | ---------------------------------------------------------- |
| Bundle 9router as required dep         | User chose less-deps single binary                         |
| One parser for all providers           | Formats genuinely differ — wrong parser = silent tool loss |
| Sync cursor-bridge memory to companion | Isolation — handoff packet only                            |
| Runtime `.so` bridge plugins           | Compile-time registry for MVP; revisit later               |


---

## Implementation order


| Step | Deliverable                                              |
| ---- | -------------------------------------------------------- |
| 1    | `bridge.Registry` + `Bridge` interface                   |
| 2    | `parse/tools/openai` + `parse/tools/claude` (unit tests) |
| 3    | `bridges/openai-compat` MVP (companion brain TBD path)   |
| 4    | `parse/tools/cursor` + `parse/thinking/cursor`           |
| 5    | `bridges/cursor-bridge` + embedded schema sync           |
| 6    | `parse/tools/kimi`, `parse/thinking/kimi`                |
| 7    | `bridges/claude-compat`                                  |
| 8    | Community bridge template + docs                         |


---

## Related repos (reference only)


| Repo                                                                | Use for SapaLOQ                                   |
| ------------------------------------------------------------------- | ------------------------------------------------- |
| [jahrulnr/cursor-bridge](https://github.com/jahrulnr/cursor-bridge) | Schema, coercion test vectors, proto-lab          |
| 9router                                                             | Transport pattern reference — **not** runtime dep |
| `cursor-agent-toolcall-spec.json`                                   | ToolCall variant map for mirror UI                |
