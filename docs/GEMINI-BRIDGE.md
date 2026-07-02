# Gemini bridge (`gemini-bridge`)

> Last updated: 2026-07-02

Dedicated LLM driver for Google **Generative Language API** (`generateContent` /
`streamGenerateContent?alt=sse`). Not OpenAI-shaped — do not route through
`provider-bridge`.

Package: [`internal/bridges/gemini/`](../internal/bridges/gemini/)

## Wire contract

| Item | Value |
|------|-------|
| URL | `{endpoint}/models/{model}:generateContent` or `:streamGenerateContent?alt=sse` |
| Auth | `X-goog-api-key` (`GEMINI_API_KEY` or `GOOGLE_API_KEY`) |
| Request | `contents[]`, `parts[]`, `tools.functionDeclarations`, optional `toolConfig`, `generationConfig.thinkingConfig` |
| Thinking | `thinkingLevel` + `includeThoughts` from `reasoningEffort` |
| Tools | `functionCall` / `functionResponse` parts |

Notes:

- `tools.functionDeclarations[].parameters` must **not** contain `additionalProperties` (Gemini rejects it with 400). The bridge strips `additionalProperties` recursively from registered tool schemas before sending.

## Multi-turn tool replay (`thoughtSignature`)

Gemini 2.x requires **verbatim replay** of model `parts` on the next request,
including `thoughtSignature` and `functionCall.id` on tool rounds.

SapaLOQ persists opaque replay on the assistant turn:

```json
{
  "driver": "gemini-bridge",
  "model_parts": [
    {"text": "...", "thought": true},
    {"functionCall": {"id": "...", "name": "...", "args": {}}, "thoughtSignature": "..."}
  ]
}
```

Flow:

1. Bridge emits `StreamEvent.WireMeta` on `EventToolCall`.
2. Orchestrator persists `chat.Turn.WireMeta` (content may be empty).
3. `actorTurnsToMessages` passes `bridge.Message.WireMeta` on replay.
4. `messages.go` prefers WireMeta over reconstructed assistant text.

## Stream vs nostream

| Mode | Thinking on wire |
|------|------------------|
| nostream | Often `thought: true` text parts |
| stream | May only expose `thoughtsTokenCount` + signature on `functionCall` |

Replay uses persisted **model parts from wire**, not UI thinking stream.

## Probe fallbacks

On upstream 400:

- Reject `thinkingConfig` → retry without `generationConfig`.
- Reject `toolConfig` → retry with `tools` only.

Same contract as [`test/gemini/`](../test/gemini/) characterize suite.

## Config example

```json
{
  "key": "gemini",
  "driver": "gemini-bridge",
  "endpoint": "https://generativelanguage.googleapis.com/v1beta",
  "model": "gemini-flash-latest",
  "credentialsEnv": "GEMINI_API_KEY",
  "reasoningEffort": "low",
  "contextWindow": 1000000,
  "requestTimeoutSec": 600,
  "stream": true
}
```

## Limitations (v1)

- Images are attached as `inlineData` parts to the **final user message** in the request. Supported inputs:
  - `bridge.Image.DataURI` in `data:<mime>;base64,<payload>` form, or
  - raw `bridge.Image.Data` + `bridge.Image.MimeType` (encoded to base64).
- Images are **not** interleaved into earlier user messages; they are appended only to the last user turn built for the call.
- If the call has no user message parts, images are dropped (no `inlineData` is emitted).
- Raw HTTP (no `google.golang.org/genai` SDK) for wire fidelity.
- `generateContent` only (not Interactions API).
- WireMeta-heavy turns increase `turns.json` size — `/reset` clears.

## Validation

```bash
go test ./internal/bridges/gemini/... -count=1
make gemini-characterize   # live gate (GEMINI_API_KEY)
```

Characterize captures: `tmp/gemini/*.jsonl`, `docs/providers/gemini-*.md`.
