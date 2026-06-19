# SapaLOQ

Portable desktop companion — isolated from `cursor-agent`. **Go modular drivers** (platform + **LLM bridge**) + **sub-agent nodes** (local or remote).

**Start here:** [docs/BLUEPRINT.md](./docs/BLUEPRINT.md) — unified development proposal.

**Runtime:** one binary `sapaloq-core` — platform driver → **cursor-bridge** brain → IPC socket → Wails widget.

**UI (M5):** Wails v2 FAB+popup — [docs/UI-DECISION.md](./docs/UI-DECISION.md) · [cmd/sapaloq-widget/](./cmd/sapaloq-widget/)

## Quick start

```bash
# Build & test
make test

# One terminal — orchestrator + widget dev; Ctrl+C stops both
sudo apt install libwebkit2gtk-4.1-dev build-essential
go install github.com/wailsapp/wails/v2/cmd/wails@latest
make widget-install
make run                          # autoload token from Cursor IDE state.vscdb or .env
```

One-shot chat from CLI:

```bash
go run ./cmd/sapaloq-core chat "halo"
go run ./cmd/sapaloq-core chat '/settings show'
```

Details: [docs/RUNTIME.md](./docs/RUNTIME.md) · widget spike: [docs/development/m5a-spike.md](./docs/development/m5a-spike.md)

## CLI (`sapaloq-core`)

| Command | Purpose |
|---------|---------|
| `run` | Start IPC server on `~/.config/sapaloq/run/sapaloq.sock` |
| `chat [message]` | Stream one chat turn to stdout (`[thinking]`, `[response]`, `[tool]`) |
| `--debug`, `-d` | Audit logs on stderr (credentials, bridge, wire summary) |
| `--verbose`, `-v` | Debug + per-frame wire detail |
| `doctor` | Validate config paths, writable socket dir, cursor autoload |
| `vault list [--limit N] [--json]` | Recent undeclared/unknown tool calls |
| `vault stats [--json]` | Vault summary by reason and top tools |
| `vault path` | Print vault log path |
| `help` | Usage |

Env: `SAPALOQ_CONFIG`, `SAPALOQ_CURSOR_TOKEN`, `CURSOR_ACCESS_TOKEN`, `CURSOR_MACHINE_ID`. Credentials autoload from `.env` or Cursor IDE `state.vscdb` (see [docs/BRIDGE.md](./docs/BRIDGE.md#credentials)).

Slash commands in chat: **`/settings` only** (MVP). Example:

```text
/settings patch {"notifications":{"read":false}}
/settings show
```

## Vault (tool surface review)

When a provider emits a **structured tool call** outside `llmBridge.declaredTools`, SapaLOQ appends to:

`~/.config/sapaloq/vault/tool-calls.jsonl`

Thinking/chat text that **mentions** tool names is not filtered — aliases handle grouping. Vault is for **actionable review** when fixing declared tool surface and schema aliases.

See [docs/BRIDGE.md](./docs/BRIDGE.md#vault-undeclared-tool-calls).

## Repository layout

```text
sapaloq/
├── cmd/
│   ├── sapaloq-core/     # orchestrator + bus + IPC + CLI
│   ├── sapaloq-widget/   # Wails FAB+popup (M5)
│   └── sapaloq-mock/     # dev mock unix socket
├── internal/             # shared packages (see internal/README.md)
├── docs/                 # architecture & SOPs
├── schema/               # config + os.json JSON Schema
├── config/               # example config.json
├── examples/nodes/       # node comm-spec templates
├── migrations/           # SQLite migrations
└── embed/                # embedded cursor-bridge schema
```

Runtime data (not in repo): `~/.config/sapaloq/`

## Docs

| File | Purpose |
|------|---------|
| **[docs/BLUEPRINT.md](./docs/BLUEPRINT.md)** | Unified development book — proposal + roadmap |
| [docs/RUNTIME.md](./docs/RUNTIME.md) | Single binary, CLI, doctor, vault paths |
| [docs/BRIDGE.md](./docs/BRIDGE.md) | LLM bridge — cursor-bridge, vault, parsers |
| [docs/ORCHESTRATOR.md](./docs/ORCHESTRATOR.md) | Spawn, control, `/settings` |
| [docs/RE-CURSOR-THINKING-TOOLS.md](./docs/RE-CURSOR-THINKING-TOOLS.md) | Cursor thinking/tools wire truth |
| [docs/DRIVER.md](./docs/DRIVER.md) | Platform driver registry, os.json |
| [docs/VISION.md](./docs/VISION.md) | Vision & mission |
| [schema/config.schema.json](./schema/config.schema.json) | `config.json` contract |

## Status

M0 ✅ docs · M5a ✅ widget spike · **M5b/M8/M9 🚧** cursor-bridge stream + vault + `/settings` · M1 next: `companion.db` boot indexer

## License

[MIT](./LICENSE)
