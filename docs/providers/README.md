# Provider characterization index

> Last updated: 2026-07-02

Field notes from the live **OpenRouter characterize suite** (`test/openrouter/`). Each page documents one model + wire mode (`stream` or `nostream`) on OpenRouter's OpenAI-compatible gateway via a raw HTTP probe (fake `get_weather` tool loop; no SapaLOQ orchestrator).

| Doc | Model | Mode | Tools | Thinking | Verdict |
|-----|-------|------|-------|----------|---------|
| [openrouter-anthropic-claude-3-haiku-stream.md](./openrouter-anthropic-claude-3-haiku-stream.md) | `anthropic/claude-3-haiku` | stream | (re-run) | — | — |
| [openrouter-anthropic-claude-3-haiku-nostream.md](./openrouter-anthropic-claude-3-haiku-nostream.md) | `anthropic/claude-3-haiku` | nostream | (re-run) | — | — |
| [openrouter-moonshotai-kimi-k2-stream.md](./openrouter-moonshotai-kimi-k2-stream.md) | `moonshotai/kimi-k2` | stream | (re-run) | — | — |
| [openrouter-moonshotai-kimi-k2-nostream.md](./openrouter-moonshotai-kimi-k2-nostream.md) | `moonshotai/kimi-k2` | nostream | (re-run) | — | — |

Legacy single-mode pages (`openrouter-<slug>.md` without suffix) are superseded by the `-stream`/`-nostream` pair.

## Regenerate

```bash
export SAPALOQ_OPENROUTER_E2E=1
export OPENROUTER_API_KEY=sk-or-...
export OPENROUTER_MODELS='anthropic/claude-3-haiku|openai|bearer|,moonshotai/kimi-k2|kimi|bearer|'
make openrouter-characterize
```

Each run refreshes:

- Raw capture: `tmp/openrouter/<model-slug>-stream.jsonl` and `...-nostream.jsonl`
- Human transcript: `tmp/openrouter/<model-slug>-{stream,nostream}.md`
- Provider docs: `docs/providers/openrouter-<model-slug>-{stream,nostream}.md`

See also: [test/README.md](../test/README.md), [PROVIDER-BRIDGE.md](../PROVIDER-BRIDGE.md).
