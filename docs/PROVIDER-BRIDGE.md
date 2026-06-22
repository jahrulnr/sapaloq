# SapaLOQ — Provider Bridge (OpenAI / Claude / Kimi)

> **Multi-model LLM bridge** — speaks OpenAI Chat Completions, Anthropic Messages, and Kimi (Moonshot) through one binary. Each provider is a self-contained entry in `llmBridge.providers`; selection via `llmBridge.providerKey`. Cursor is a first-class provider (RE proxy). No third-party proxy (9router-style) required.
> Last updated: 2026-06-22 (inline tool-call reassembly across content deltas; labeled `[Tool: name]` / bare `name {args}` recovery)

Related: [BRIDGE.md](./BRIDGE.md) · [ORCHESTRATOR.md](./ORCHESTRATOR.md) · [RE-CURSOR-THINKING-TOOLS.md](./RE-CURSOR-THINKING-TOOLS.md)

---

## Why this exists

OpenRouter, TokenRouter, OpenAI, Claude, Kimi, and many other inference providers expose roughly the same surface — HTTP POST + Server-Sent Events — but the request body and per-line event shape differ:

| Provider | Wire | Auth header | Tool calls | Thinking |
|----------|------|-------------|------------|----------|
| OpenAI Chat Completions | `/v1/chat/completions` | `Authorization: Bearer <key>` | `delta.tool_calls[]` | `delta.reasoning_content` |
| OpenRouter / TokenRouter | Same as OpenAI | `Authorization: Bearer <key>` | Same as OpenAI | Same as OpenAI |
| Anthropic Messages | `/v1/messages` | `x-api-key: <key>` + `anthropic-version` | `content_block_*` events | `thinking_delta` events |
| Kimi (Moonshot) | Same as OpenAI | `Authorization: Bearer <key>` | Same as OpenAI | `reasoning_content` (with `thinking.type=enabled` body flag) |

`cursor-bridge` is a single-provider driver; the **provider-bridge** is the multi-provider counterpart. Both implement `bridge.Bridge`.

The catch with OpenRouter/TokenRouter: their endpoint is OpenAI-shaped, but the model can be Claude-shaped. The wire format depends on the model, not the endpoint URL.

---

## Recommended: providers array

Every available provider is one self-contained entry in `llmBridge.providers`. The active entry is selected via `llmBridge.providerKey`. **Cursor is a first-class provider** in the same array — it just uses a different `driver` (RE proxy) instead of direct API access.

```json
{
  "llmBridge": {
    "providerKey": "cursor",
    "providers": [
      {
        "key": "cursor",
        "driver": "cursor-bridge",
        "endpoint": "https://api2.cursor.sh",
        "model": "default",
        "credentialsEnv": "SAPALOQ_CURSOR_TOKEN"
      },
      {
        "key": "openai",
        "driver": "provider-bridge",
        "endpoint": "https://api.openai.com/v1/chat/completions",
        "model": "gpt-4o-mini",
        "credentialsEnv": "OPENAI_API_KEY",
        "parser": "openai",
        "authScheme": "bearer",
        "contextWindow": 1000000
      },
      {
        "key": "openrouter-claude",
        "driver": "provider-bridge",
        "endpoint": "https://openrouter.ai/api/v1/chat/completions",
        "model": "anthropic/claude-opus-4.8",
        "credentialsEnv": "OPENROUTER_API_KEY",
        "parser": "claude",
        "authScheme": "x-api-key"
      },
      {
        "key": "kimi",
        "driver": "provider-bridge",
        "endpoint": "https://api.moonshot.ai/v1/chat/completions",
        "model": "kimi-k2.6",
        "credentialsEnv": "MOONSHOT_API_KEY",
        "parser": "kimi"
      }
    ]
  }
}
```

Same `providers` array holds cursor (RE proxy) and the direct OpenAI / Claude / Kimi entries. Switching providers is just changing `providerKey` — no driver indirection.

To switch at runtime via `/settings`, just patch `providerKey` and the next chat picks up the new entry.

---

## Cursor = RE proxy, others = direct

| Driver | What it does | Models available |
|--------|--------------|------------------|
| `cursor-bridge` | Reverse-Engineered Cursor client → upstream provider | GPT, Claude, Kimi, etc. (Cursor routes internally) |
| `provider-bridge` | Direct HTTP to OpenAI / Anthropic / Moonshot API | Whatever the endpoint supports |
| `local-llama` | Local llama.cpp sidecar | Whatever you serve |

Each cursor entry talks to `api2.cursor.sh` and lets Cursor pick the upstream. Each provider-bridge entry talks directly to the upstream API. The choice is yours — many users have both for fallback.

---

## Detection order (per-entry)

`internal/bridges/provider/detect.go` resolves the parser, auth scheme, and API version from one entry. Order:

1. **Explicit `entry.Parser`** — `"openai"`, `"claude"` / `"anthropic"`, `"kimi"` / `"moonshot"`.
2. **Model name sniff** — Anthropic/Moonshot family markers (`claude`/`opus`/`sonnet`/`haiku`/`kimi`/`moonshot`).
3. **Endpoint URL substring** — `anthropic`/`claude` → Claude, `moonshot`/`kimi` → Kimi, else OpenAI.
4. **Default** — OpenAI parser, `bearer` auth, 1,000,000 context window.

`authScheme` follows: explicit `entry.AuthScheme` → parser-derived (claude → x-api-key, others → bearer).

`apiVersion`: explicit `entry.APIVersion` → caller applies default `"2023-06-01"` (used by the Claude wire layer only).
| — | — | `claude-opus-4.8` | `openrouter.ai` | `claude` (model wins) |
| — | — | `gpt-5` | `openrouter.ai` | `openai` (model) |
| — | — | — | `api.openai.com` | `openai` (endpoint) |
| — | — | — | (any other) | `openai` (default) |

---

## Provider recipes (entry snippets)

These snippets show one entry each. Add them to the `llmBridge.providers` array and set `providerKey` to the entry's `key`.

### OpenAI

```json
{
  "key": "openai",
  "driver": "provider-bridge",
  "endpoint": "https://api.openai.com/v1/chat/completions",
  "model": "gpt-4o-mini",
  "credentialsEnv": "OPENAI_API_KEY",
  "parser": "openai",
  "authScheme": "bearer",
  "reasoningEffort": "medium",
  "maxTokens": 4096
}
```

`reasoningEffort` maps to OpenAI's `reasoning_effort` parameter (`low` | `medium` | `high`).

### OpenRouter — Claude model

```json
{
  "key": "openrouter-claude",
  "driver": "provider-bridge",
  "endpoint": "https://openrouter.ai/api/v1/chat/completions",
  "model": "anthropic/claude-opus-4.8",
  "credentialsEnv": "OPENROUTER_API_KEY",
  "parser": "claude",
  "authScheme": "x-api-key"
}
```

The `parser: "claude"` is required because OpenRouter returns Claude wire format for Claude models, even though its endpoint is OpenAI-shaped.

### OpenRouter — GPT-5

```json
{
  "key": "openrouter-gpt5",
  "driver": "provider-bridge",
  "endpoint": "https://openrouter.ai/api/v1/chat/completions",
  "model": "openai/gpt-5",
  "credentialsEnv": "OPENROUTER_API_KEY",
  "parser": "openai",
  "authScheme": "bearer"
}
```

### TokenRouter

```json
{
  "key": "tokenrouter",
  "driver": "provider-bridge",
  "endpoint": "https://api.tokenrouter.com/v1/chat/completions",
  "model": "MiniMax-M3",
  "credentialsEnv": "TOKENROUTER_API_KEY",
  "parser": "openai"
}
```

### Anthropic Claude (direct)

```json
{
  "key": "claude",
  "driver": "provider-bridge",
  "endpoint": "https://api.anthropic.com/v1/messages",
  "model": "claude-sonnet-4-5",
  "credentialsEnv": "ANTHROPIC_API_KEY",
  "parser": "claude",
  "authScheme": "x-api-key",
  "apiVersion": "2023-06-01",
  "reasoningEffort": "high"
}
```

`reasoningEffort` maps to Anthropic's `thinking.budget_tokens` (`low=1024`, `medium=5000`, `high=16000`). Set to a literal token count to override.

### Kimi (Moonshot)

```json
{
  "key": "kimi",
  "driver": "provider-bridge",
  "endpoint": "https://api.moonshot.ai/v1/chat/completions",
  "model": "kimi-k2.6",
  "credentialsEnv": "MOONSHOT_API_KEY",
  "parser": "kimi"
}
```

Kimi is OpenAI-compatible at the wire level. When `reasoningEffort` is non-empty the bridge injects `thinking: { type: "enabled" }` into the request body.

### Cursor (RE proxy)

```json
{
  "key": "cursor",
  "driver": "cursor-bridge",
  "endpoint": "https://api2.cursor.sh",
  "model": "default",
  "credentialsEnv": "SAPALOQ_CURSOR_TOKEN"
}
```

Cursor itself can pick which upstream model to use (GPT, Claude, Kimi) — the model name in the entry is the default. The RE client handles wire format translation.

---

## Context window

The bridge truncates the conversation before sending it to the model, keeping the most recent turns and always preserving the leading system message.

```json
{
  "providers": [
    {
      "key": "claude",
      "driver": "provider-bridge",
      "endpoint": "https://api.anthropic.com/v1/messages",
      "credentialsEnv": "ANTHROPIC_API_KEY",
      "parser": "claude",
      "authScheme": "x-api-key",
      "contextWindow": 200000
    }
  ]
}
```

**Default:** `DefaultContextWindow = 1,000,000`. Per-entry override via the entry's `contextWindow` field.

**Truncation rule:**

- Estimate each message's tokens as `len(content) / 4`.
- Drop the oldest non-system messages until the total fits inside the window.
- System message (role = `"system"` at index 0) is always preserved.

Set `contextWindow: 0` to disable truncation (rare; the bridge will forward the full conversation regardless of size).

---

## Validation

`LLMBridgeRoot.Validate()` is called during config load. It enforces:

- `providerKey` is non-empty.
- `providers` array is non-empty.
- Every entry has a non-empty `key`.
- No duplicate `keys` in the array.
- `providerKey` matches one of the entry keys.

Failures surface as config-load errors before the orchestrator starts.

---

## Architecture

```
internal/bridges/provider/
├── bridge.go      → bridge.Bridge impl (Complete → WireOptions → goroutine)
├── detect.go      → DetectParser/AuthScheme/APIVersion/ContextWindow/...
│                    entry-level resolver with model-name sniff + endpoint fallback
├── context.go     → FitMessagesToContext — drops oldest non-system messages
│                    when the conversation exceeds the configured window
├── wire.go        → runSSE loop, HTTP request builder, body marshalling
├── handlers.go    → per-line SSE dispatch (openai / kimi / claude)
├── types.go       → request/response struct shapes, message+tool builders
└── register.go    → registry hook (called from cmd/sapaloq-core)

internal/parse/tools/provider/
├── openai.go      → AccumulatorOpenAI for streaming tool_calls
├── kimi.go        → AccumulatorKimi (delegates to openai)
├── claude.go      → AccumulatorClaude for content_block_* events
├── payload.go     → name+arguments ⇄ string round-trip
└── leak.go        → inline JSON tool-call detection

internal/parse/thinking/provider/
├── provider.go    → Parsed struct + StripForMemory
├── openai.go      → channel-tag + legacy ¹think⁄ tags
├── claude.go      → <thinking></thinking> + <final></final>
└── kimi.go        → falls back to openai parser, then legacy tags
```

The wire layer is parser-agnostic. Each line is decoded by a per-parser handler that pushes normalised `WireEvent{Thinking, Text, Tool}` into a `WireHandler`. The bridge translates those into `bridge.StreamEvent` and surfaces them to the orchestrator.

---

## Stream event flow

```text
Upstream SSE line
  └─ extractDataPayload (handlers.go)
       └─ Parser-specific JSON unmarshal
            └─ Accumulator.Apply (tool calls)
                 └─ WireEvent
                      └─ Bridge.handleWireEvent
                           ├─ EventThinkingDelta
                           ├─ EventResponseDelta
                           │    └─ leakScanner.feed → EventToolCall (reassembled)
                           └─ EventToolCall
                                └─ EventDone
```

**Inline tool-call reassembly (`leakScanner`).** Some models (notably MiniMax)
emit a tool call inline in the *content* channel — `{"name":"...","arguments":{...}}`
— instead of the native `tool_calls` field. When the argument is large (e.g. a
whole HTML/CSS/JS file body), the JSON is streamed split across **many** content
deltas, so scanning one delta at a time never sees a balanced `{...}` and the
call is silently lost (this caused multi-turn task failures where only small
calls like `mkdir` got through). The per-stream `leakScanner` (`bridge.go`) fixes
this: it **accumulates** the visible content across deltas and scans the buffer
from a moving frontier for complete objects, emitting each reassembled call as a
real `EventToolCall`. Two safeguards:

- **String-aware brace matching** (`scanOneJSONObject` in `leak.go`): braces and
  escaped quotes *inside* a JSON string value are ignored, so file content with
  unbalanced `{`/`}` doesn't close the object early.
- **Declared-tool gating**: a reassembled object is only accepted if its `name`
  is in the request's `DeclaredTools`, so a JSON blob inside file content that
  merely has `name`+`arguments` fields is not misread as a call. An empty
  declared list disables the scanner entirely.

**Labeled / bare inline forms.** Besides the `{"name":...,"arguments":{...}}`
envelope, the scanner also recovers the two *labeled* shapes a model may emit
inline (the role prompts instruct calls as `read_file {"path":"..."}`,
`exec {"command":"..."}`), where the trailing `{...}` is the **arguments** body,
not an envelope:

- **Bracketed** — `[Tool: <name>]\n{args}`. Accepted even without a declared
  list (the label is unambiguous), but still gated by `DeclaredTools` when one
  is set. This was the orch-chat leak: `[Tool: exec]\n{"command":"ls ..."}`
  surfaced as a `response_delta` because no parser recognised it.
- **Bare** — `<name> {args}` where `<name>` is a snake_case token. Accepted
  **only** when `<name>` is in `DeclaredTools`, so prose like `the object {...}`
  is never misread; a name that is only a suffix of a longer word (`prefixexec`)
  is rejected too.

Both reuse the string-aware brace matcher and the moving-frontier streaming
logic, so a labeled call whose large args object (or the label itself) is split
across deltas is still reassembled into a single `EventToolCall`.

(The old `EventToolLeak` event type remains defined but is no longer emitted by
this path; the orchestrator only ever consumed `EventToolCall`.)

---

## Capabilities

```go
type BridgeCaps struct {
    Thinking  bool // true for all three parsers
    Tools     bool // always true
    LiveAPI   bool // true when credentialsEnv resolves to a non-empty string
}
```

`Caps()` is consulted by the orchestrator before opening a stream. A bridge without `LiveAPI` falls back to a mock stream (the same path `cursor-bridge` uses when no token is loaded).

---

## Adding a new provider

To add a new provider that uses one of the existing three wire formats, you only need to:

1. Add a `DetectParser` case (if the endpoint URL is distinctive).
2. Add a docs recipe (this file).
3. Add a `detect_test.go` case.

To add a brand-new wire format:

1. Add a `ParserKind` constant in `detect.go`.
2. Add `streamNewProvider` in `wire.go` and a `newNewProviderLineHandler` in `handlers.go`.
3. Add request/response structs in `types.go`.
4. Add an `AccumulatorNewProvider` under `internal/parse/tools/provider/`.
5. Cover the new format with `httptest`-based tests in `wire_test.go`.

The bridge file itself does not need to change — it dispatches on `opts.Parser`.

---

## Role-scoped tools for Ask / Plan / Agent

Provider-bridge backends should not receive one static global tool list forever. To match Cursor/Copilot behavior, SapaLOQ resolves tools from the current execution role:

| Role | Mode | Tool source | Notes |
|------|------|-------------|-------|
| `orchestrator` | Ask | `tools.profiles.ask` | spawn/status/context/clarify + light `desktop_*`; no shell or file mutation |
| `planner` | Plan | `tools.profiles.plan` | read-only workspace/memory + `sapaloq_write_plan_markdown`; writes `memory/tasks/<taskId>/plan.md` |
| `task-runner` | Agent | `tools.profiles.agent` | workspace edits, terminal/build/test, progress/complete |

`llmBridge.providers[].declaredTools` remains a compatibility fallback for old configs and vault review. The target runtime contract is request-scoped tools:

```go
bridge.Request{
    Role: "planner",
    ExecutionMode: "plan",
    DeclaredTools: resolvedRoleTools,
}
```

The provider bridge serializes those canonical names into OpenAI/Claude/Kimi tool schemas. The orchestrator, not the provider bridge, validates and executes emitted tool calls.

---

## Limitations

- **Single inference backend per stream.** Each Complete call goes to one configured endpoint. Multi-model orchestration (e.g. Claude for thinking, GPT for tools) is the orchestrator's job.
- **No provider-specific tool schemas.** `declaredTools` is a name list; the bridge sends `additionalProperties: true` schemas. If you need strict parameter validation, build a custom `*ToolParser` adapter.
- **Tool calls in a single message only.** The bridge does not yet route tool results back to the model in a multi-turn loop — that's the orchestrator's responsibility.
- **No fallback / retry.** If the upstream returns 5xx the bridge surfaces an `EventError` and stops. The orchestrator decides whether to retry.
