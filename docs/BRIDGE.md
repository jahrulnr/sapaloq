# SapaLOQ - LLM Bridge Drivers

> **Brain bridge drivers** - connect companion/sub-agent LLM calls to external APIs & IDEs.
> **cursor-bridge** = driver pertama; Claude/OpenAI-compatible built-in later (9router-*pattern*, bukan adopt 9router sebagai third-party).
> Last updated: 2026-07-02 (llama-cpp driver rename)
>
> Prior: 2026-06-28 (**tool mapping** — `ResolveToolCall` upstream→declared; see `TOOL-MAPPING.md`)

Related: [DRIVER.md](./DRIVER.md) · [ORCHESTRATOR.md](./ORCHESTRATOR.md) · [TOOL-MAPPING.md](./TOOL-MAPPING.md) · [LIMITATIONS.md](./LIMITATIONS.md) · [RE-CURSOR-THINKING-TOOLS.md](./RE-CURSOR-THINKING-TOOLS.md) · [BOUNDARIES.md](./BOUNDARIES.md)

> **Thinking/tools wire truth:** [RE-CURSOR-THINKING-TOOLS.md](./RE-CURSOR-THINKING-TOOLS.md) (L0 only). Jangan derive thinking behavior dari 9router - adapter itu skip/collapse channel thinking Cursor.

---

## Dua keluarga driver

SapaLOQ punya **dua registry driver** terpisah - jangan dicampur:


| Family         | Package             | Pilih via                          | Contoh                                                           |
| -------------- | ------------------- | ---------------------------------- | ---------------------------------------------------------------- |
| **Platform**   | `internal/drivers/` | `os.json` + detect                 | `gnome`, `kde`, `windows`                                        |
| **LLM bridge** | `internal/bridges/` | `config.json` → `llmBridge.driver` | `cursor-bridge`, `openai-compat`, `claude-compat`, `llama-cpp` |

### Per-request timeout

Each provider entry bounds a single inference request via
`llmBridge.providers[].requestTimeoutSec` (default **600s**, resolved by
`config.LLMBridge.RequestTimeout()`). It applies to the **provider-bridge**
(`WireOptions.Timeout` → `buildHTTPRequest` `context.WithTimeout`) and the
cursor-bridge **api2** chat stream (`StreamOptions`). The **api5 agent path**
(api5 / `useAgentPath` / vision) does **not** use that wall clock — one agent
turn can run long MCP/exec loops. Stream lifetime uses a **refilling idle**
cap (`streamIdleTimeoutSec`, default **60s** — reset on each api5 frame or
uplink write; paused during local MCP/exec) plus the orchestrator run idle
cancel (`orchestrator.continuation.maxWallTimeMinutes`, default 30m silence).
The old hardcoded 120s wire default truncated long sub-agent steps into a bare
"context deadline exceeded"; bridges rewrite deadline errors to name the timeout
and the knob (`explainStreamError`).

### Pre-stream retry

The provider-bridge retries a **transient pre-stream failure** (connection
error, or a retryable status: `408`, `429`, `5xx`) with exponential backoff +
jitter, bounded by `llmBridge.providers[].maxRetries`
(`config.LLMBridge.ResolveMaxRetries()`, default **5**, `-1` disables, clamped
to 10). This matches the official Blackbox CLI's resilience (OpenAI SDK
`maxRetries`) and absorbs flaky-gateway `500`s (e.g. the Vercel AI Gateway
`Connection error` routing Anthropic/opus models behind `api.blackbox.ai`).
Retries only fire **before** the first SSE byte, so streamed deltas are never
duplicated. See [PROVIDER-BRIDGE.md](./PROVIDER-BRIDGE.md#limitations).


```
sapaloq-core
├── driver/          platform registry (os.json)
├── drivers/         gnome, kde, …
├── bridge/          LLM bridge registry
├── bridges/         cursor-bridge, openai-compat, claude-compat, llama-cpp, …
└── parse/
    ├── tools/       format-specific tool call parsers
    └── thinking/    format-specific reasoning/thinking parsers
```

Platform driver = desktop automation. LLM bridge driver = **companion brain** + optional sub-agent remote inference.

---

## cursor-bridge sebagai driver resmi

**cursor-bridge** bukan dependency runtime ke repo `jahrulnr/cursor-bridge` atau 9router - SapaLOQ **mengadopsi kontrak** (schema, aliases, coercion) sebagai driver built-in.


|                    | SapaLOQ                          | cursor-bridge monorepo                   | 9router                                    |
| ------------------ | -------------------------------- | ---------------------------------------- | ------------------------------------------ |
| Role               | Runtime driver di `sapaloq-core` | Source of truth schema + test vectors    | Legacy proxy pattern (referensi transport) |
| Dependency         | Embed/sync schema at build       | Dev reference                            | **Tidak** third-party dep                  |
| Unknown tool calls | **Vault** JSONL review log       | Schema aliases + test vectors            | Partial (transport only)                   |
| Thinking text      | Kimi suppress + leak sanitizer (`guard.go`) | `leakMarkers` in schema (reference only) | Collapses pre-tag thinking                 |


### api2 message framing (live chat)

Before `wire.StreamChat*` / Node `cursor-proto-lab`, `normalizeCursorWireMessages`
(`internal/bridges/cursor/messages.go`) maps roles like 9router
`openai-to-cursor.js`:

- `system` → `user` with prefix `[System Instructions]\n` (orchestrator.md, runtime context, skills, etc.)
- `tool` → `user` with a `<tool_result>...</tool_result>` XML block (avoids protobuf `tool_results` loop bugs)
- `user` / `assistant` unchanged

**9router wire parity** (same session, `guard.go` + `wire/proto.go`):

- **INSTRUCTION guard** — protobuf field populated for `default`/`auto` models (`OpenAI bridge: callable tools are …` or no-tools variant); skipped when declared tools **or prompt-embedded** native Agent-session triggers (`run_terminal_cmd`, etc.) are detected.
- **forceAgentMode** — `default`/`auto` → Agent wire (`IS_AGENTIC=1`, `UNIFIED_MODE=Agent`, tools enabled).
- **MCP tools[]** — declared SapaLOQ tools encoded on the wire (schemas from `provider.RegisteredToolSchema`).
- **Response hygiene** — accumulate one turn in `stream_buffer.go`, then finalize (9router `transformProtobufToSSE` parity): **api2** ingests `wire.ExtractedPart` frames; **api5** ingests `wire.AgentDecoded` via `ingestAgentDecoded` (same buffer). Post-MCP `text`/`thinking` on the agent wire is discarded (`noteMCPTool`) — it is continuation noise, not assistant output. Finalize: defer Kimi inline tool extraction until stream end; `cleanKimiAssistantContent` + `finalizeAssistantContentWithToolCalls`; end-of-turn sanitizer (skip when tools present); drop undeclared structured tool calls (vault only); **drop all tools + visible text when thinking is unanchored confabulation** vs the user prompt; normalize `web_search.search_term` → executor `query`.
- **Thinking promote** — `default`/`auto`/`composer*`/` *-thinking` models promote post-`</think>` tail to visible assistant text (9router `visibleContentFromThinking`).
- **Prompt agent detect** — scan messages for embedded native tool schemas (`"name": "…"`, markdown headers) to skip guard on real Agent sessions.

Disable guard: `SAPALOQ_CURSOR_TOOL_GUARD=0` (also honors `NINEROUTER_CURSOR_TOOL_GUARD`).

**Why message roles still matter:**
Passing orchestrator `system` turns through unchanged made multi-kilobyte orchestrator.md
look like prior assistant speech, which triggered agent-task confabulation on
short greetings (`hey hey` → invented website/21st.dev tasks). OpenClaw +
9router never had this bug because the translator runs before encode.

### Provider message roles (orchestrator notes)

SapaLOQ maps wire events to storage/API roles per provider — do not pretend unsupported roles exist on the wire.

| Provider / path | Native wire roles | SapaLOQ storage / replay notes |
|-----------------|-------------------|--------------------------------|
| OpenAI-compatible (`provider-bridge`) | `system`, `user`, `assistant`, `tool`; some gateways add `developer` | Direct replay via `actorTurnsToMessages`; assistant-before-tool fix in mapper |
| Cursor api2/api5 (`cursor-bridge`) | Composed user blob; thinking/thinking+text deltas; MCP tools in-bridge | `thinking` turn UI-only; tool via `EventToolUpdate`; see `CURSOR_AGENT_CONTRACT.md` |
| Codex app-server | Thread items; native tool namespace `sapaloq` | Tool `outputDelta` → progress; turns append on orchestrator ToolUpdate path |

When a provider cannot carry a role (e.g. native `thinking` on OpenAI chat completions), persist as show-only `thinking` in `turns.json` and skip in API replay — explicit mapping, not silent reorder.

Reference artifacts (dev / regen):

- `cursor-bridge.schema.json` - aliases, nativeTools, kimiTokens, coercion maps
- `cursor-agent-toolcall-spec.json` - 49 ToolCall variants (mirror UI only)

Orchestrator & sub-agents call unified interface `bridge.Complete()` - driver handles wire format.

---

## codex-bridge (app-server socket only)

`internal/bridges/codex` connects to `codex app-server`; there is no
`codex exec` fallback. Both UDS and TCP endpoints carry WebSocket JSON-RPC.
The default `auto` lifecycle probes `~/SapaLOQ/run/codex-app-server.sock`,
spawns a shared app-server child when absent, and reaps only that owned child on
shutdown or provider reload. `external` and `managed` modes connect without
taking process ownership.

One `Complete` call maps to one `turn/start` … `turn/completed`. The bridge
starts or resumes a Codex thread and persists the SapaLOQ session mapping under
`vault/codex-threads.jsonl`. Legacy CLI records are deliberately incompatible,
so the first turn after upgrade starts cleanly instead of resuming a thread
without dynamic tool definitions.

Request-scoped `DeclaredTools` become the `sapaloq` dynamic-tools namespace on
`thread/start`, using the same descriptions and JSON schemas as provider-bridge.
Inbound `item/tool/call` requests execute through `bridge.Request.ToolExecutor`
inside the Codex turn and return a `DynamicToolCallResponse`. Codex-native tool
items and dynamic callbacks emit `Source:"codex"` telemetry; the orchestrator
forwards that telemetry to the UI but never enqueues it for a second dispatch.

Cancellation sends `turn/interrupt`. Terminal success/failure comes from the
matching `turn/completed`; a closed socket without a terminal is an error.
Unknown notification/item kinds are skipped, and streamed item IDs suppress
duplicate completed-item text.

Runtime settings are environment-only:

| Variable | Default |
|---|---|
| `SAPALOQ_CODEX_APP_SERVER_MODE` | `auto` (`external`, `managed`) |
| `SAPALOQ_CODEX_APP_SERVER_LISTEN` | `unix://~/SapaLOQ/run/codex-app-server.sock` |
| `SAPALOQ_CODEX_BINARY` | `codex` from `PATH` |
| `SAPALOQ_CODEX_SANDBOX` | `workspace-write` |
| `SAPALOQ_CODEX_CWD` | SapaLOQ workspace |
| `CODEX_HOME` | `~/.codex` |

`sapaloq-core doctor` checks binary resolution, lifecycle/probe, `initialize`,
and `getAuthStatus`. Exact framing, methods, notification mapping, approvals,
and tests are documented in
[CODEX_APP_SERVER_CONTRACT.md](./CODEX_APP_SERVER_CONTRACT.md); ownership and
tool-flow rationale are in [BRIDGE_DESIGN.md](./BRIDGE_DESIGN.md).

---

## Vault (undeclared tool calls)

When Cursor or another provider emits a **structured tool call** (protobuf `TOOL_CALL` or Kimi inline) that is not on the companion **declared surface**, SapaLOQ appends a JSONL row - **no blocking**, stream continues.

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


**Not vaulted:** tool names mentioned in thinking or chat text - aliases already group upstream names internally; filtering prose is unnecessary.

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
| `cursor-bridge`            | Cursor `api2.cursor.sh` / agent proto | **High** - fake tool names | `tools:cursor`, `thinking:cursor` |
| `provider-bridge` (openai) | OpenAI `/v1/chat/completions`         | **Low** - usually clean    | `tools:openai`, `thinking:openai` |
| `provider-bridge` (claude) | Anthropic `/v1/messages`              | **Low**                    | `tools:claude`, `thinking:claude` |
| `provider-bridge` (kimi)   | OpenAI-compatible + `thinking` flag   | **Low**                    | `tools:openai`, `thinking:openai` |
| `codex-bridge`             | App-server WebSocket JSON-RPC (UDS/WS) | Native telemetry only; dynamic callback for SapaLOQ tools | App-server notification mapper |
| `gemini-bridge`            | Google `generateContent` / SSE         | **Low** — native `functionCall`                           | `tools:gemini`, WireMeta replay — see [`GEMINI-BRIDGE.md`](GEMINI-BRIDGE.md) |
| `llama-cpp`              | llama-server OpenAI `/v1/chat/completions` | N/A (local)                | `openai` default; optional auth |


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


**boundary-guard** + **context-scaler** tetap jalan di atas parsed tool calls - wire-format aliasing + vault review, bukan prose filtering di thinking channel.

---

## Parser layer (tools)

Satu **canonical internal model** (`parse.ToolCall`) - setiap driver pilih parser:

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
| **cursor** | Protobuf `THINKING_TEXT` + `RESPONSE_TEXT` + `TOOL_CALL` | Dual channel; blob split at `</think>` - see [RE-CURSOR-THINKING-TOOLS.md](./RE-CURSOR-THINKING-TOOLS.md) |
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

Thinking blocks **tidak** disimpan ke companion memory verbatim by default - stream to widget ring state `thinking`, strip before scribe index.

```go
type ThinkingParser interface {
    ID() string // "cursor" | "claude" | "kimi" | "openai"
    ExtractThinking(stream []byte) (public string, thinking string, err error)
    StripForMemory(text string) string
}
```


| Parser     | Format                                                          | Notes                                          |
| ---------- | --------------------------------------------------------------- | ---------------------------------------------- |
| **cursor** | `THINKING_TEXT` blob: pre/post `</think>`, optional `<|final|>` | **Do not** collapse like 9router - see RE doc  |
| **claude** | Extended thinking `thinking` blocks                             | API beta headers                               |
| **kimi**   | Inline section after thinking split                             | Sub-parser of cursor Auto path, not standalone |
| **openai** | `reasoning` / o-series deltas                                   | Model-gated                                    |


Widget **thinking** state driven by parser events - not raw provider bytes.

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
    NativeTools     []string  // upstream leak catalog - for coercion/sanitizer ONLY, not sub-agent tool list
    Streaming       bool
    Vision          bool
    ThinkingMode    bool
}
```

`**NativeTools` semantics:** names Cursor/Kimi may **hallucinate** in thinking/content. Used by:

1. **Leak detection** (`analyzeLeak`) - content + thinking scan
2. **Coercion mapping** - fake/upstream name → SapaLOQ declared tool via `ResolveToolCall` (`declared_map.go`); catalog in `docs/TOOL-MAPPING.md`
3. **Not** exposed to sub-agent as callable tools - sub-agent tools come from `subAgents.roles[].allowedTools` only

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

## `config.json` - `llmBridge`

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
      "driver": "llama-cpp",
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


Credentials **never** in config.json - env, `.env`, or IDE `state.vscdb` only.

### Credentials

Autoload priority (ported from `@cursor-bridge/credential-loader`):

1. `SAPALOQ_CURSOR_TOKEN` or `CURSOR_ACCESS_TOKEN` + optional `CURSOR_MACHINE_ID` in process env
2. **Shell rc** - at boot `sapaloq-core` sources `~/.bashrc` then `~/.zshrc` (Linux only) and folds **all** not-already-set vars from the sourced environment into the process env (tokens, `PATH`, custom `credentialsEnv` names, etc.). This matters under systemd `--user`/XDG autostart, where there is no login shell so rc exports would otherwise be invisible. The rc is sourced with an **interactive** shell (`bash -ic`/`zsh -ic`) on purpose: the stock Debian/Ubuntu `~/.bashrc` begins with `case $- in *i*) ;; *) return;; esac`, which would `return` before any exports under a non-interactive shell. Best-effort, silent on any failure, stdin detached and stderr discarded so the interactive shell can't prompt/block, never overrides an already-set var, short timeout so a hanging rc can't freeze startup (`internal/shellenv`).
3. `.env` in cwd, then `~/.config/sapaloq/.env`
4. `~/.config/Cursor/User/globalStorage/state.vscdb` (`cursorAuth/accessToken`, `storage.serviceMachineId`)

Override vscdb path: `CURSOR_STATE_VSCDB` (when set, **only** that path is consulted — default IDE locations are skipped). Ghost mode default on unless `CURSOR_GHOST_MODE=false`.

`sapaloq-core doctor` prints credential source. Mock stream when autoload finds no token; offline mock emits `sapaloq_stop` on `<sapaloq:autopilot>` continuations so the orchestrator does not burn the inference-turn budget.

Agent may `/settings set llmBridge.driver openai-compat` - no settings UI.

### Wire driver selection

The cursor bridge ships **two HTTP/2 wire drivers**. Both produce the same request
shape (headers + Connect+proto body); they differ in transport. Pick via
`SAPALOQ_WIRE_DRIVER`:


| Driver          | Implementation                                                                             | Status                                                                                                                                     |
| --------------- | ------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------ |
| `raw` (default) | `wire.StreamChatRaw` - raw frames via `http2.Framer`, mirrors cursor-proto-lab Node client | Experimental; some api2 responses require further frame-format alignment (current symptom: `FRAME_SIZE_ERROR` goaway or no response).      |
| `http2`         | `wire.StreamChat` - Go `net/http` + `http2.Transport`                                      | Stable; surfaces `unauthenticated` cleanly when token rejected, but api2 currently rejects with the same error against valid vscdb tokens. |


Live E2E (`make e2e-live`) accepts either path as long as the bridge emits
`EventError` instead of a silent `[done]`.

### Agent API path (vision + composer models)

The Agent API (`agent.v1.AgentService/Run` on
`agentn.global.api5.cursor.sh`) is the endpoint `cursor-agent` uses for chat,
composer, and vision requests. It accepts a different protobuf envelope than
the legacy `StreamUnifiedChatWithTools` path, and is the only cursor API that
supports inline image input.

SapaLOQ automatically routes a request through the Agent API path when:

1. `**SAPALOQ_AGENT_PATH=1`** - explicit operator override (used by tests).
2. **Vision content** - any message contains `data:image/...` (inline base64)
  or an `http(s)://....png|jpg|jpeg|gif|webp` URL.

The encoder/decoder live in `internal/bridges/cursor/wire/proto_agent.go`
(field numbers pinned from cursor-agent's `agent.proto` descriptor bundle
2026.06.02-8c11d9f; cross-checked against the Node reference at
`9router/open-sse/utils/cursorAgentProtobuf.js`). The HTTP/2 driver is the
same raw-framer transport used for the chat path, with a separate request
encoder and response decoder:


| Driver (Agent API) | Implementation                                                                    | Default                                                                               |
| ------------------ | --------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------- |
| `node`             | `wire.StreamAgentNode` — thin Node H2 gateway (`scripts/cursor-agent-h2-gateway.mjs`); Go owns headers, protobuf, exec/MCP | **Production** when `node` + script available |
| `raw`              | `wire.StreamAgentRawWithRaw` — raw HTTP/2 framer; full exec loop + MCP in Go    | `SAPALOQ_AGENT_WIRE_DRIVER=raw` (api5 still auth-fingerprints; use for wire parity) |
| `http2`            | `wire.StreamAgentHTTP2` — `net/http` http2 + `agentUploadBody`; same exec loop  | `SAPALOQ_AGENT_WIRE_DRIVER=http2`                                                     |


Enable for all text turns: `"useAgentPath": true` on the cursor provider entry, or
`SAPALOQ_AGENT_PATH=1`. Vision requests always route through api5.
(defaults: `agentn.global.api5.cursor.sh` + `/agent.v1.AgentService/Run`). Use
`SAPALOQ_WIRE_INSECURE_TLS=1` to skip certificate verification when targeting
self-signed test servers.

Live E2E: `make e2e-live SAPALOQ_AGENT_PATH=1` exercises the Agent API path
end-to-end against `api5.cursor.sh`. See [CURSOR_AGENT_CONTRACT.md](./CURSOR_AGENT_CONTRACT.md)
for the exec/MCP ownership model (mirrors codex-bridge `ToolExecutor`, not CLI subprocess).

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
| Chat                | `aiserver.v1.ChatService/StreamUnifiedChatWithTools`                     | Encoder stable; live returns `unauthenticated` | Same `unauthenticated` issue as the chat HTTP/2 transport - byte-level frame alignment still pending against `api2.cursor.sh`.                                                                   |
| Agent (privacy)     | `agent.v1.AgentService/Run` (host `agent.global.api5.cursor.sh`)         | Routed via `wire.AgentHost(true)`              | No telemetry sent to cursor.                                                                                                                                                                     |
| Agent (non-privacy) | `agent.v1.AgentService/Run` (host `agentn.global.api5.cursor.sh`)        | Routed via `wire.AgentHost(false)` (default)   | Default host.                                                                                                                                                                                    |
| Default model nudge | `aiserver.v1.AiService/GetDefaultModelNudgeData` (host `api2.cursor.sh`) | Stub via `wire.BuildNudgeRequestBody`          | 5-byte Connect-RPC unary envelope (1-byte flag + 4-byte length=0). Server picks the default model id; not currently decoded - bridge falls back to the explicit model id supplied by the caller. |


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
| research (web)        | Often `openai-compat` / `claude-compat` - lower poisoning |
| scribe                | Local or cheap compat API                                 |


Remote node **does not** share bridge credentials unless comm spec declares - token stays on orchestrator machine, remote gets **delegated spawn** with pre-built messages only.

### Worker (`cursor-agent`) relationship - not intercept

SapaLOQ widget = **parallel independent session** via `cursor-bridge` (or compat driver). It does **not** tap or proxy live `cursor-agent` CLI traffic.


| Path                   | When                                                                                 |
| ---------------------- | ------------------------------------------------------------------------------------ |
| **Parallel** (default) | Orchestrator + sub-agents use SapaLOQ's own bridge session; memory in JSON index (`facts.json`) |
| **Handoff** (explicit) | User or orchestrator writes `bridge/handoff/<uuid>.json` → worker consumes once      |


Implication for milestones: M1–M3 need bridge session + JSON store only; deep cursor-agent mirror UI is M8–M9 polish, not M1 blocker.

---

## Explicit non-goals


| Idea                                   | Why                                                        |
| -------------------------------------- | ---------------------------------------------------------- |
| Bundle 9router as required dep         | User chose less-deps single binary                         |
| One parser for all providers           | Formats genuinely differ - wrong parser = silent tool loss |
| Sync cursor-bridge memory to companion | Isolation - handoff packet only                            |
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
| 9router                                                             | Transport pattern reference - **not** runtime dep |
| `cursor-agent-toolcall-spec.json`                                   | ToolCall variant map for mirror UI                |
