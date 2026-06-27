# SapaLOQ

Portable desktop companion. **Go modular drivers** (platform + **LLM bridge**) + **sub-agent nodes** (local or remote).

**Start here:** [docs/BLUEPRINT.md](./docs/BLUEPRINT.md) - unified development proposal.

**Runtime:** one binary `sapaloq-core` - platform driver → **provider-bridge** brain → IPC socket → Wails widget.

**UI (M5):** Wails v2 FAB+popup - [docs/UI-DECISION.md](./docs/UI-DECISION.md) · [cmd/sapaloq-widget/](./cmd/sapaloq-widget/)

## Install

There are two ways to install, depending on whether you want a prebuilt release
or a build from source.

### Users - prebuilt release (no Go/build needed)

```bash
curl -fsSL https://raw.githubusercontent.com/jahrulnr/sapaloq/main/install.sh | bash
```

`install.sh` **downloads the prebuilt release artifact** from GitHub (Linux
x86_64), verifies its sha256 checksum, installs `sapaloq-core` and
`sapaloq-widget` into `~/.local/bin`, seeds a default config at
`~/.config/sapaloq/config.json` (an existing config is never overwritten),
creates the runtime dirs (`memory/ state/ run/ vault/`), and runs
`sapaloq-core service install` to enable + start the background service. Only
`curl`, `tar` and (for the service) systemd `--user` are required - no clone, no
Go toolchain, no `wails`.

```bash
./install.sh --version v0.1.0      # install a specific release (default: latest)
./install.sh --no-service          # install binaries only, no systemd
./install.sh --no-autostart        # don't launch the widget on login
./install.sh --bin-dir ~/bin       # install binaries elsewhere
./install.sh --no-verify           # skip checksum verification (not recommended)
./install.sh --uninstall           # remove service + autostart + binaries (config KEPT)
```

### Developers - build from source

From a checkout, build and install the binaries locally (requires Go, and
`wails` + `libwebkit2gtk` for the widget):

```bash
make install                       # build core+widget, seed config, register service
make install INSTALL_SERVICE=0     # install binaries only, no systemd
make install INSTALL_AUTOSTART=0   # don't launch the widget on login
make install BIN_DIR=~/bin         # install binaries elsewhere
make uninstall                     # remove service + binaries (config KEPT)
```

Releases are produced by the `release` GitHub Actions workflow on a `v*` tag: it
builds the binaries, packages `sapaloq_<version>_linux_amd64.tar.gz` +
`checksums.txt`, and publishes them to the GitHub Release that `install.sh`
downloads from.

Make sure `~/.local/bin` is on your `PATH`. To keep the core service running
without an active login session: `loginctl enable-linger $USER`.

**Widget on login:** `service install` also writes an XDG autostart entry
(`~/.config/autostart/sapaloq-widget.desktop`), so the widget appears on your
desktop automatically after you log in to GNOME (or any XDG-compliant session) -
no manual launching. It shows up on the next login; to start it immediately the
first time, run `sapaloq-widget &`. The widget is a graphical app, so it uses the
desktop session's autostart rather than the headless systemd service.

### Service (systemd `--user`)

The service supervises `sapaloq-core run` (the orchestrator + IPC socket).

| Command | Action |
|---------|--------|
| `sapaloq-core service install` | Write the unit, `daemon-reload`, enable + start, **and add the widget login autostart** (idempotent) |
| `sapaloq-core service uninstall` | Stop, disable, remove the unit **and the widget autostart** - **config/data kept** |
| `sapaloq-core service start` | Start the service (manual) |
| `sapaloq-core service stop` | Stop the service (manual) |
| `sapaloq-core service status` | `systemctl --user status` passthrough |

The unit is written to `~/.config/systemd/user/sapaloq.service` with an absolute
`ExecStart` pointing at the installed binary.

### Uninstall / delete config

`./install.sh --uninstall` (or `sapaloq-core service uninstall`) removes the service
and binaries but **keeps** your config and data. To erase everything - config,
facts, chat history and the tool vault - delete both default roots. If
`runtime.dataDir` was customized, replace `~/SapaLOQ` with that configured path:

```bash
rm -rf ~/.config/sapaloq ~/SapaLOQ
```

## Quick start (dev)

```bash
# Build & test
make test

# One terminal - orchestrator + widget dev; Ctrl+C stops both
sudo apt install libwebkit2gtk-4.1-dev build-essential
go install github.com/wailsapp/wails/v2/cmd/wails@latest
make widget-install
make run                          # autoload token from .env
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
| `run` | Start IPC server on `~/SapaLOQ/run/sapaloq.sock` |
| `chat [message]` | Stream one chat turn to stdout (`[thinking]`, `[response]`, `[tool]`) |
| `--debug`, `-d` | Audit logs on stderr (credentials, bridge, wire summary) |
| `--verbose`, `-v` | Debug + per-frame wire detail |
| `doctor` | Validate config paths, writable socket dir |
| `vault list [--limit N] [--json]` | Recent undeclared/unknown tool calls |
| `vault stats [--json]` | Vault summary by reason and top tools |
| `vault path` | Print vault log path |
| `service install\|uninstall\|start\|stop\|status` | Manage the systemd `--user` background service (see [Service](#service-systemd---user)) |
| `help` | Usage |

Slash commands in chat: **`/settings` only** (MVP). Example:

```text
/settings patch {"notifications":{"read":false}}
/settings show
```

## Vault (tool surface review)

When a provider emits a **structured tool call** outside `llmBridge.declaredTools`, SapaLOQ appends to:

`~/SapaLOQ/vault/tool-calls.jsonl`

Thinking/chat text that **mentions** tool names is not filtered - aliases handle grouping. Vault is for **actionable review** when fixing declared tool surface and schema aliases.

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
└── migrations/           # SQLite migrations
```

Config: `~/.config/sapaloq/config.json`. Runtime data (not in repo): `~/SapaLOQ/`.

## Docs

| File | Purpose |
|------|---------|
| **[docs/BLUEPRINT.md](./docs/BLUEPRINT.md)** | Unified development book - proposal + roadmap |
| [docs/RUNTIME.md](./docs/RUNTIME.md) | Single binary, CLI, doctor, vault paths |
| [docs/BRIDGE.md](./docs/BRIDGE.md) | LLM bridge |
| [docs/ORCHESTRATOR.md](./docs/ORCHESTRATOR.md) | Spawn, control, `/settings` |
| [docs/DRIVER.md](./docs/DRIVER.md) | Platform driver registry, os.json |
| [docs/VISION.md](./docs/VISION.md) | Vision & mission |
| [schema/config.schema.json](./schema/config.schema.json) | `config.json` contract |

## License

[MIT](./LICENSE)
