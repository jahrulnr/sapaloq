# SapaLOQ

Portable desktop companion — isolated from `cursor-agent`. **Go modular drivers** (platform + **LLM bridge**) + **sub-agent nodes** (local or remote).

**Start here:** [docs/BLUEPRINT.md](./docs/BLUEPRINT.md) — unified development proposal.

**Runtime:** one binary `sapaloq-core` — `os.json` cache → platform driver → `llmBridge` brain → spawn nodes via SQLite registry.

**UI (M5):** Wails v2 FAB+popup — [docs/UI-DECISION.md](./docs/UI-DECISION.md) · widget [cmd/sapaloq-widget/](./cmd/sapaloq-widget/)

## Quick start (M5a widget spike)

```bash
# Terminal 1 — mock IPC server
make mock

# Terminal 2 — widget (Ubuntu 24.04)
sudo apt install libwebkit2gtk-4.1-dev build-essential
go install github.com/wailsapp/wails/v2/cmd/wails@latest
make widget-install
make widget-build
./cmd/sapaloq-widget/build/bin/sapaloq-widget
```

Details: [docs/development/m5a-spike.md](./docs/development/m5a-spike.md)

## Repository layout

```text
sapaloq/
├── cmd/
│   ├── sapaloq-core/     # orchestrator + bus + IPC (M1+)
│   ├── sapaloq-widget/   # Wails FAB+popup (M5a ✅)
│   └── sapaloq-mock/     # dev mock unix socket
├── internal/             # shared packages (see internal/README.md)
├── docs/                 # architecture & SOPs
├── schema/               # config + os.json JSON Schema
├── config/               # example config.json
├── examples/nodes/       # node comm-spec templates
├── migrations/           # SQLite migrations (M1+)
└── embed/                # embedded assets (schemas, etc.)
```

Runtime data (not in repo): `~/.config/sapaloq/`

## Docs

| File | Purpose |
|------|---------|
| **[docs/BLUEPRINT.md](./docs/BLUEPRINT.md)** | Unified development book — proposal + roadmap |
| [docs/NODES.md](./docs/NODES.md) | Sub-agent nodes — SQLite + comm spec |
| [docs/DRIVER.md](./docs/DRIVER.md) | Platform driver registry, os.json |
| [docs/BRIDGE.md](./docs/BRIDGE.md) | LLM bridge drivers — cursor-bridge, parsers |
| [docs/ORCHESTRATOR.md](./docs/ORCHESTRATOR.md) | Spawn, control, progress |
| [docs/VISION.md](./docs/VISION.md) | Vision & mission |
| [docs/UI-DECISION.md](./docs/UI-DECISION.md) | Widget: Wails FAB+popup (M5a validated) |
| [schema/config.schema.json](./schema/config.schema.json) | `config.json` contract |

## Status

M0 ✅ docs · **M5a ✅** widget spike · M1 next: `companion.db` + `sapaloq-core` doctor

## License

[MIT](./LICENSE)
