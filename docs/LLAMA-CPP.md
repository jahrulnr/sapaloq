# SapaLOQ - llama-cpp driver (llama-server)

> Thin preset over **provider OpenAI wire** for local [llama.cpp](https://github.com/ggml-org/llama.cpp) **llama-server** (`/v1/chat/completions`). Not a separate HTTP stack — reuses `internal/bridges/provider` parsers and SSE handlers.
>
> Last updated: 2026-07-02

Related: [BRIDGE.md](./BRIDGE.md) · [PROVIDER-BRIDGE.md](./PROVIDER-BRIDGE.md) · [providers/llamacpp-*](./providers/) (characterize notes) · `test/llamacpp/`

---

## When to use

| Driver | Use for |
|--------|---------|
| **`llama-cpp`** | Local **llama-server** OpenAI-compatible chat API (default port **8080**) |
| **`provider-bridge`** | Remote OpenAI-shaped gateways, or local servers with explicit `parser` / `authScheme` |
| Dedicated driver | Native non-OpenAI wire (e.g. `gemini-bridge`) |

`llama-cpp` does **not** claim every local model stack — only llama-server (or compatible) exposing OpenAI Chat Completions. Other local wires need characterize + `provider-bridge` or a new driver.

---

## Endpoint & port

| Setting | Default | Override |
|---------|---------|----------|
| `endpoint` | `http://127.0.0.1:8080` → normalized to `…/v1/chat/completions` | Full URL or `host:port` (e.g. `http://127.0.0.1:16285`) |
| Characterize env | `http://127.0.0.1:8080/v1/chat/completions` | `LLAMACPP_ENDPOINT` |

Upstream llama.cpp serves on **port 8080** by default. Custom systemd/deploy ports are fine — set `endpoint` only.

**Avoid** agent-sidecar `:16283` for SapaLOQ chat: that path may inject system layers. Point at direct llama-server instead.

---

## Server deployment modes

llama-server is commonly started in one of two ways:

### Single-model startup

```bash
llama-server -m /path/to/model.gguf --port 8080
```

- One model loaded at boot.
- The `model` field in chat requests may be ignored or must match the loaded weights (build-dependent).
- Set `model` in config for consistency with logs/UI.
- `POST /models/load` may return 404/400 — doctor reports a **warning**, not a failure.

### Multi-model / router

- Server runs without a fixed `-m` model; models load on demand.
- **`model` in config must match the server registry** (e.g. `unsloth/gemma-4-E2B-it-GGUF:Q4_K_XL`).
- Cold load can be slow — use `requestTimeoutSec: 600` or higher.
- Upstream may return **503** while loading; preload manually or let doctor best-effort `POST /models/load`.

**SapaLOQ contract:** the driver always sends `entry.model` on every request. It does **not** auto-detect single vs multi-model mode.

---

## Auth

| Deploy | Config |
|--------|--------|
| No `--api-key` (typical local) | Omit `credentialsEnv`; `authScheme` defaults to **none** |
| Server with API key | `credentialsEnv`: `LLAMACPP_API_KEY` (or `LLAMA_API_KEY`); `authScheme`: `bearer` |

---

## Example config

```json
{
  "key": "local-gemma",
  "driver": "llama-cpp",
  "endpoint": "http://127.0.0.1:8080",
  "model": "unsloth/gemma-4-E2B-it-GGUF:Q4_K_XL",
  "reasoningEffort": "low",
  "requestTimeoutSec": 600,
  "stream": true
}
```

Switch active provider: `llmBridge.providerKey`: `"local-gemma"`.

---

## Wire capabilities (characterized)

From `test/llamacpp` live probe (`tmp/llamacpp/`):

- `tool_calls` + `role=tool` multi-turn replay (no WireMeta)
- `reasoning_content` → thinking channel (`reasoning_effort: low` sufficient)
- `stream: true` and `stream: false` both supported

Re-run characterize after infra changes:

```bash
export SAPALOQ_LLAMACPP_CHARACTERIZE_E2E=1
export LLAMACPP_ENDPOINT='http://127.0.0.1:16285/v1/chat/completions'  # if non-default port
export LLAMACPP_MODELS='unsloth/gemma-4-E2B-it-GGUF:Q4_K_XL|openai|bearer|'
make llamacpp-characterize
```

---

## Doctor

`sapaloq-core doctor` when `llama-cpp` is active:

1. `GET {base}/health`
2. Best-effort `POST {base}/models/load` with configured `model` (multi-model preload hint)

Credentials are optional; missing token does not fail doctor.

---

## Implementation map

| Piece | Location |
|-------|----------|
| Driver | `internal/bridges/llamacpp/` |
| HTTP/SSE wire | `internal/bridges/provider` (`AuthNone`, OpenAI parser) |
| Characterize oracle | `test/llamacpp/` |
| Register | `cmd/sapaloq-core/main.go` |

---

## Limitations

See [LIMITATIONS.md](./LIMITATIONS.md) — non-OpenAI local stacks, cold multi-model load, `llmBridge.fallback` auto-failover not wired yet.
