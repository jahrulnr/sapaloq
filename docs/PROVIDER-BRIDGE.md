# SapaLOQ - Provider Bridge (OpenAI / Claude / Kimi)

> **Multi-model LLM bridge** - speaks OpenAI Chat Completions, Anthropic Messages, and Kimi (Moonshot) through one binary. Each provider is a self-contained entry in `llmBridge.providers`; selection via `llmBridge.providerKey`. Cursor is a first-class provider (RE proxy). No third-party proxy (9router-style) required.
> Last updated: 2026-06-25 (added pre-stream retry/backoff with `maxRetries` knob; documented `contextWindow` (input) vs `maxTokens` (output) knobs + 1M/128k example; clarified `[Called tools: …]` is a suppressed orchestrator note, not a recoverable inline call)

Related: [BRIDGE.md](./BRIDGE.md) · [ORCHESTRATOR.md](./ORCHESTRATOR.md) · [RE-CURSOR-THINKING-TOOLS.md](./RE-CURSOR-THINKING-TOOLS.md)

---

## Why this exists

OpenRouter, TokenRouter, OpenAI, Claude, Kimi, and many other inference providers expose roughly the same surface - HTTP POST + Server-Sent Events - but the request body and per-line event shape differ:

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

Every available provider is one self-contained entry in `llmBridge.providers`. The active entry is selected via `llmBridge.providerKey`. **Cursor is a first-class provider** in the same array - it just uses a different `driver` (RE proxy) instead of direct API access.

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

Same `providers` array holds cursor (RE proxy) and the direct OpenAI / Claude / Kimi entries. Switching providers is just changing `providerKey` - no driver indirection.

To switch at runtime via `/settings`, just patch `providerKey` and the next chat picks up the new entry.

---

## Cursor = RE proxy, others = direct

| Driver | What it does | Models available |
|--------|--------------|------------------|
| `cursor-bridge` | Reverse-Engineered Cursor client → upstream provider | GPT, Claude, Kimi, etc. (Cursor routes internally) |
| `provider-bridge` | Direct HTTP to OpenAI / Anthropic / Moonshot API | Whatever the endpoint supports |
| `local-llama` | Local llama.cpp sidecar | Whatever you serve |

Each cursor entry talks to `api2.cursor.sh` and lets Cursor pick the upstream. Each provider-bridge entry talks directly to the upstream API. The choice is yours - many users have both for fallback.

---

## Detection order (per-entry)

`internal/bridges/provider/detect.go` resolves the parser, auth scheme, and API version from one entry. Order:

1. **Explicit `entry.Parser`** - `"openai"`, `"claude"` / `"anthropic"`, `"kimi"` / `"moonshot"`.
2. **Model name sniff** - Anthropic/Moonshot family markers (`claude`/`opus`/`sonnet`/`haiku`/`kimi`/`moonshot`).
3. **Endpoint URL substring** - `anthropic`/`claude` → Claude, `moonshot`/`kimi` → Kimi, else OpenAI.
4. **Default** - OpenAI parser, `bearer` auth, 1,000,000 context window.

`authScheme` follows: explicit `entry.AuthScheme` → parser-derived (claude → x-api-key, others → bearer).

`apiVersion`: explicit `entry.APIVersion` → caller applies default `"2023-06-01"` (used by the Claude wire layer only).
| - | - | `claude-opus-4.8` | `openrouter.ai` | `claude` (model wins) |
| - | - | `gpt-5` | `openrouter.ai` | `openai` (model) |
| - | - | - | `api.openai.com` | `openai` (endpoint) |
| - | - | - | (any other) | `openai` (default) |

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

### OpenRouter - Claude model

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

### OpenRouter - GPT-5

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

Cursor itself can pick which upstream model to use (GPT, Claude, Kimi) - the model name in the entry is the default. The RE client handles wire format translation.

---

## Context window & output cap

There are **two separate token knobs** per provider entry. They are easy to
confuse, so be precise:

| Field | Bounds | What it limits | Default | Wire mapping |
|-------|--------|----------------|---------|--------------|
| `contextWindow` | **input** | The conversation the bridge *sends* to the model in one turn (prompt + history). | `1,000,000` | none - used locally to truncate before the request |
| `maxTokens` | **output** | The most tokens the model may *generate* back in one turn. | `0` (unset) | `max_completion_tokens` (openai/kimi) · `max_tokens` (claude) |

`contextWindow` is the **input budget**; `maxTokens` is the **output budget**.
They are independent - `contextWindow` does **not** reserve room for the output.

### Example: 1M context, 128k output

```json
{
  "key": "openai",
  "driver": "provider-bridge",
  "endpoint": "https://api.openai.com/v1/chat/completions",
  "model": "gpt-5",
  "credentialsEnv": "OPENAI_API_KEY",
  "parser": "openai",
  "authScheme": "bearer",
  "contextWindow": 1000000,
  "maxTokens": 131072
}
```

> **128k = 131072** (128 × 1024). `1M = 1000000`.

### `contextWindow` (input truncation)

The bridge truncates the conversation before sending it, keeping the most recent
turns and always preserving the leading system message.

- **Default:** `DefaultContextWindow = 1,000,000`. Per-entry override via
  `contextWindow`.
- **Truncation rule:**
  - Estimate each message's tokens as `len(content) / 4`.
  - Drop the oldest non-system messages until the total fits inside the window.
  - The system message (role = `"system"` at index 0) is always preserved.
- Set `contextWindow: 0` to disable truncation (rare; the bridge forwards the
  full conversation regardless of size).

> **If the model's physical context is a *shared* input+output budget** (e.g. a
> "1M total" model), set `contextWindow` a bit *below* that total so there is
> room for the output. For a 1M-total model wanting 128k output, use
> `contextWindow: 900000`, `maxTokens: 131072`. SapaLOQ does not subtract
> `maxTokens` from `contextWindow` for you - pick the input budget with the
> output in mind.

### `maxTokens` (output cap)

Bounds what the model may generate in a single turn. Per-parser behavior:

- **openai / kimi:** sent as `max_completion_tokens`, **only when `maxTokens > 0`**.
  Left unset (`0`) → the field is omitted and the provider uses its own default.
- **claude:** the Anthropic API *requires* `max_tokens`, so the bridge always
  sends it - **defaulting to `8192`** when `maxTokens` is unset, and using your
  value when set.

### How to set these

- Edit the provider entry in `~/.config/sapaloq/config.json` (under
  `llmBridge.providers[]`) directly, **or**
- Use the `/settings` command in the widget to patch config fields live.

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
├── detect.go      → DetectParser/AuthScheme/APIVersion/ContextWindow/MaxTokens/...
│                    entry-level resolver with model-name sniff + endpoint fallback
├── context.go     → FitMessagesToContext - drops oldest non-system messages
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
emit a tool call inline in the *content* channel - `{"name":"...","arguments":{...}}`
- instead of the native `tool_calls` field. When the argument is large (e.g. a
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

- **Bracketed** - `[Tool: <name>]\n{args}`. Accepted even without a declared
  list (the label is unambiguous), but still gated by `DeclaredTools` when one
  is set. This was the orch-chat leak: `[Tool: exec]\n{"command":"ls ..."}`
  surfaced as a `response_delta` because no parser recognised it.
- **Bare** - `<name> {args}` where `<name>` is a snake_case token. Accepted
  **only** when `<name>` is in `DeclaredTools`, so prose like `the object {...}`
  is never misread; a name that is only a suffix of a longer word (`prefixexec`)
  is rejected too.

Both reuse the string-aware brace matcher and the moving-frontier streaming
logic, so a labeled call whose large args object (or the label itself) is split
across deltas is still reassembled into a single `EventToolCall`.

**Not the same as `[Called tools: …]`.** Do not confuse the recoverable forms
above with a `[Called tools: name, …]` line. The latter is **not** a tool call
the model is trying to make - it is an *internal* transcript note the
orchestrator itself appends (`calledToolsNote`, the anti double-spawn record),
which some models then echo back as prose. It carries no args object, so this
scanner correctly ignores it; instead the orchestrator's `calledToolsFilter`
(`internal/core/orchestrator/called_tools_filter.go`) strips that echo from the
visible `response_delta` stream so it never reaches the user or the persisted
assistant message.

**Raw control-char tolerance.** A model that writes a multi-line argument inline
(e.g. an `exec` heredoc whose body is a whole HTML file) embeds **real newline
bytes** inside the JSON string value. Per the JSON spec a literal control byte
(U+0000–U+001F) in a string is invalid, so `encoding/json` would reject the whole
call and it was silently lost - the tool then saw empty args, returned "command
is required", and the model wrongly concluded its content had been
"stripped/filtered" (and burned turns on base64/chunking workarounds). The
reassembler and `parseToolArgs` now run the candidate through
`parse.RepairControlCharsInJSON`, which escapes raw control bytes **inside string
literals** (`\n`, `\r`, `\t`, `\u00XX`) while leaving structure untouched - a
no-op for already-valid JSON. So a multi-line inline call is recognised and its
stored `Arguments` are valid JSON that downstream unmarshalling accepts.

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

The bridge file itself does not need to change - it dispatches on `opts.Parser`.

---

## Role-scoped tools for Ask / Plan / Agent

Provider-bridge backends should not receive one static global tool list forever. To match Cursor/Copilot behavior, SapaLOQ resolves tools from the current execution role:

| Role | Mode | Tool source | Notes |
|------|------|-------------|-------|
| `orchestrator` | Ask | `tools.profiles.ask` | spawn/status/context/clarify + light `desktop_*`; no shell or file mutation |
| `planner` | Plan | `tools.profiles.plan` | read-only workspace/memory + `write_plan`; writes `state/tasks/<taskId>/plan.md` |
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
- **Tool calls in a single message only.** The bridge does not yet route tool results back to the model in a multi-turn loop - that's the orchestrator's responsibility.
- **Pre-stream retry, no mid-stream retry.** A transient *pre-stream* failure (connection error, or a retryable status: `408`, `429`, `5xx`) is retried with exponential backoff + jitter up to `llmBridge.providers[].maxRetries` times (resolved by `config.LLMBridge.ResolveMaxRetries()`, default **5**; set `-1` to disable, clamped to 10). This mirrors the OpenAI SDK's default retry behaviour that keeps the official Blackbox CLI stable on the same endpoint, and fixes flaky-gateway `500`s such as the Vercel AI Gateway `InternalServerError: Connection error` routing Anthropic models behind `api.blackbox.ai` (model-other providers rarely hit that route, so the symptom was opus-only). Retries fire **only** before the first SSE byte is dispatched, so emitted deltas are never duplicated. Once the stream is open, any failure (or a non-retryable 4xx) surfaces an `EventError` and stops — the orchestrator decides whether to retry the whole turn.
- **No model-group fallback.** The bridge does not switch to a different provider entry on failure; that is the orchestrator's job.
