# SapaLOQ - Runtime: Single Binary

> **Satu binary Go** - goroutine + channel + persistence lokal. Zero external daemons.
> Last updated: 2026-06-28 (sapaloq.sock IPC timeout troubleshooting; sapaloq_stop scope docs for agents)

Related: [EVENT-BUS.md](./EVENT-BUS.md) · [VISION.md](./VISION.md)

---

## Prinsip

```
sapaloq-core (one binary)
├── UI / widget IPC
├── Orchestrator loop
├── Event bus (route watchers)     ← bukan Redis/Rabbit/MQTT
├── Sub-agent workers (goroutine or child proc → same socket)
├── SQLite (companion.db)
├── jsonl WAL (events, progress, learning queue)
└── Platform adapters (swappable per OS/DE)
```

**Tidak ada dependency runtime wajib** selain OS + platform adapter + LLM API optional.

| ❌ Avoid | ✅ SapaLOQ |
|----------|------------|
| Redis / Rabbit / MQTT | In-proc bus + SQLite |
| GNOME Shell extension required | D-Bus + portal; extension/MCP optional |
| Separate broker container | Same binary |
| Hardcode one DE | `platform.adapter` swap |

---

## Persistence (local only)

| Store | Path | Role |
|-------|------|------|
| Config | `~/.config/sapaloq/config.json` | User-editable runtime configuration |
| SQLite | `~/SapaLOQ/memory/companion.db` | Facts, FTS, skills index, dedupe |
| Workspace | `~/SapaLOQ/workspace/` | Default actor CWD; persisted per actor |
| Runtime state | `~/SapaLOQ/state/` | Tasks, workers, actor inboxes, tool jobs |
| jsonl | `events.jsonl`, `progress/*.jsonl` | WAL, audit, replay on boot |
| Worker health | `state/workers/<task-id>/health.json` | Live per-worker PID/phase/heartbeat snapshot (observability) |
| Worker errors | `state/workers/<task-id>/error.log` | Errors-only trail per sub-agent (debugging) |
| Files | `config.json`, `skills/`, `prompt/` | Agent-editable, git-friendly |
| In-memory | goroutine LRU | Session hot cache - **lost on restart OK** |

Restart = reload SQLite + optional jsonl tail. No external cache warm-up.

---

## Concurrency model

```go
// Everything inside sapaloq-core
go orchestratorLoop(watchers["orchestrator"])
go walAppender(eventsCh)
go subAgentWorker(id, ctx)
go platformAdapter.NotifyWatch()
```

Sub-agent **process** (optional): still talks via `sapaloq.sock` to **same** binary's socket server - not a second broker.

---

## Failure modes (simple)

| Failure | Behavior |
|---------|----------|
| sapaloq-core crash | systemd restart; replay jsonl |
| LLM API down | Orchestrator chat degrades; queue tasks |
| SQLite locked | WAL mode + short retry + single-writer queue (see [CONTEXT-SOP.md](./CONTEXT-SOP.md#sqlite-write-concurrency-implementation-note)) |
| Slow watcher | Drop + log; never block publisher |

No cascade: "Redis failed so events broken" - **cannot happen**.

---

## systemd user unit

```ini
[Unit]
Description=SapaLOQ desktop companion
After=graphical-session.target

[Service]
ExecStart=%h/.local/bin/sapaloq-core
Restart=on-failure
Environment=SAPALOQ_HOME=%h/SapaLOQ

[Install]
WantedBy=default.target
```

One service. One binary. One socket path.

---

## MVP stack (minimal deps)

| Layer | Tech |
|-------|------|
| Core | Go 1.22+ |
| UI | **Wails v2** + web frontend (`sapaloq-widget`); see [UI-DECISION.md](./UI-DECISION.md) |
| DB | modernc.org/sqlite or mattn/go-sqlite3 |
| IPC | net.Listen("unix", socketPath) |
| GNOME | godbus |
| LLM | HTTP client direct |

No Docker, no compose, no message queue for SapaLOQ itself.

---

## Config

`runtime.singleBinary: true` (always - informational lock in schema).

Memory: `engine: sqlite` only. Event wake: `events.bus` not external broker.

The first-boot public example now contains only configuration read by the
current runtime: runtime path, platform adapter, providers, command registry,
continuation/compaction/completion, active sub-agent roles, storage, skills,
prompts, feedback, vault, and event-bus socket/WAL. Roadmap-only knobs remain
documented in their subsystem docs but are not copied into a live config where
`/settings` could falsely report a successful no-op.

`orchestrator.continuation.maxParallelTools` defaults to `8`. Tool-job state is
persisted under `state/tool-jobs/*.json`; queued/running jobs found after a
restart are marked cancelled with an explicit restart reason. Cross-actor
steering is persisted under `state/actor-inbox/<actor-id>/*.json` and consumed
at inference safe points.

The widget queues foreground corrections through IPC `chat_steering`
(`session_id`, text `message`, optional matching `target_id`, priority
`normal`). `Orchestrator.UserSteering` accepts the request only while that
session has an active foreground generation, then writes
`steering.proposed` with `source_id=user` to
`state/actor-inbox/<session-id>/*.json`. The run drains it before the next
provider inference, after the current tool batch has completed. Steering does
not start a generation or append a chat turn. `priority: interrupt` and
background-actor targets are not implemented in this IPC path.

Config and runtime data are intentionally separate. `SAPALOQ_CONFIG` controls
only the config file path (default `~/.config/sapaloq/config.json`). The default
runtime root is `~/SapaLOQ`; schema migration 1.4 rewrites only legacy shipped
defaults and preserves explicit custom paths. Startup moves known non-config
artifacts from the legacy root without moving or overwriting `config.json` and
`.env`.

Each actor starts at `~/SapaLOQ/workspace`. Relative file tools and `exec`
resolve from its persisted CWD. A shell `cd` is captured after command execution
and stored under `state/workspaces/<actor>.json`; missing directories fall back
to the default workspace.

---

## Non-goals (runtime)

- Redis, RabbitMQ, MQTT, NATS as required deps
- Multi-node event federation
- Separate `sapaloq-broker` service
- Hot cache that must survive only in Redis

Kalau scale ke Pi ↔ desktop later: **sync jsonl/export file**, bukan install Redis di desktop.

Hard limits without full fix: [LIMITATIONS.md](./LIMITATIONS.md).

---

## `sapaloq-core` CLI

Headless entrypoint - orchestrator, IPC socket, cursor-bridge brain, vault review.

```bash
sapaloq-core help
sapaloq-core --debug run
sapaloq-core --verbose chat "halo"
sapaloq-core doctor
```

Debug output goes to **stderr**; chat events stay on stdout. Env: `SAPALOQ_DEBUG=1`, `SAPALOQ_VERBOSE=1`.

| Env | Default | Purpose |
|-----|---------|---------|
| `SAPALOQ_CONFIG` | `~/.config/sapaloq/config.json` | Live config only |
| `SAPALOQ_CURSOR_TOKEN` | - | Cursor bearer token (sapaloq name) |
| `CURSOR_ACCESS_TOKEN` | - | Same token (cursor-bridge convention) |
| `CURSOR_MACHINE_ID` | - | Machine id for checksum headers |
| `CURSOR_STATE_VSCDB` | auto | Override IDE `state.vscdb` path |

Without explicit env vars, `sapaloq-core` first sources the user's shell rc (`~/.bashrc` then `~/.zshrc`, Linux only - needed under systemd `--user`/autostart where no login shell runs), then autoloads from `.env`, then Cursor IDE `state.vscdb` - broadly the same priority as the [cursor-bridge credential-loader](https://github.com/jahrulnr/cursor-bridge/tree/master/packages/credential-loader) with the shell-rc step added in front of `.env` (`internal/shellenv`). The rc is sourced with an **interactive** shell (`bash -ic`/`zsh -ic`) so the stock Debian/Ubuntu `~/.bashrc` interactive guard (`case $- in *i*) ;; *) return;; esac`) doesn't short-circuit before the user's exports. Shell-rc import is best-effort, silent on failure, allowlisted by key prefix, and never overrides an already-set variable.

`chat` output prefixes: `[thinking]`, `[response]`, `[tool]`, `[error]`, `[done]`.

---

## Vault paths

| Path | Writer | Purpose |
|------|--------|---------|
| `vault/tool-calls.jsonl` | cursor-bridge + orchestrator | Structured tool-call audit log (see two reasons below) |

The vault log is JSON-lines and serves **two** purposes, distinguished by the `reason` field on each entry:

| `reason` | Source | Meaning |
|----------|--------|---------|
| `undeclared` | cursor-bridge (and future drivers) | A provider whose tool surface is hardcoded server-side called a tool **outside** `llmBridge.declaredTools` - the original anomaly/alias-review signal |
| `executed` | `Orchestrator.auditTool` (Ask + sub-agent chokepoints) | Audit trail of every tool the orchestrator actually ran |

Review via CLI:

```bash
sapaloq-core vault stats
sapaloq-core vault list --limit 50 --json
```

Vault **does not** filter thinking/chat text - only structured tool calls (`undeclared` anomalies and `executed` audit entries). See [BRIDGE.md](./BRIDGE.md#vault-undeclared-tool-calls).

### Rotation & retention

The log is append-only, so it is **size-rotated** to stay bounded (it would otherwise grow forever, and reads - which scan the whole file - would get slower over time). When the primary file would exceed `maxLogBytes`, it cascades to numbered siblings (`tool-calls.jsonl` → `.1` → `.2` …) and the oldest beyond `keepRotatedFiles` is dropped. Rotation is **best-effort**: if a rename fails, the writer falls back to a plain append so an audit write is never lost. `ReadRecent` reads across rotated siblings so stats/CLI still see recent history after a rotation.

| Config (`config.json` → `vault`) | Default | Meaning |
|-----------------------------------|---------|---------|
| `maxLogBytes` | `5242880` (5 MiB) | Size at/after which the primary log rotates |
| `keepRotatedFiles` | `3` | How many numbered siblings (`.1` … `.N`) to retain |

An absent `vault` block uses the defaults (the cursor-bridge writer inherits the same default rotation). Implementation: `internal/vault` (`vault.go`, `read.go`).

---

## Widget IPC (`sapaloq.sock`) — `i/o timeout`

Path default: `~/SapaLOQ/run/sapaloq.sock` (`events.bus.socketPath`). The widget is a thin client; every ping, history load, and chat op goes over this unix socket.

### Timeouts (widget client)

| Phase | Limit | Ops |
|-------|-------|-----|
| Dial | **500 ms** | All ops — fails fast if core is down or not accepting |
| Read/write on connection | **3 s** | ping, `chat_history`, `context_usage`, `actor_inspect`, `session_*`, … |
| Read on connection | **35 min** | `chat_send`, `chat_retry` (covers long LLM streams) |

Server write deadline per frame: **5 s** (`internal/ipc/server.go`).

### What the error usually means

| Message | Likely cause |
|---------|----------------|
| `dial …/sapaloq.sock: connect: connection refused` | `sapaloq-core` not running |
| `dial …/sapaloq.sock: i/o timeout` | Core not accepting within 500 ms (slow boot, stale socket file, overloaded host) |
| `read unix …/sapaloq.sock: i/o timeout` | Core did not finish a **non-chat** IPC handler within **3 s** |

Chat streaming itself uses the 35-minute deadline — a slow model reply does **not** hit the 3 s cap. The 3 s timeout shows up on **side calls** while core is busy: ping (every 4 s), context usage (every 15 s), opening sub-agent monitor (`actor_inspect`), loading a large `chat_history`, or SQLite work during compaction.

### What to do

1. Confirm core is up: `sapaloq-core run` (or your systemd unit).
2. `sapaloq-core doctor` — socket path writable, config loads.
3. Transient timeouts during heavy chat/sub-agent load often clear on the next ping; widget marks the conn dot reconnecting.
4. Persistent timeouts while idle → check core stderr for a wedged IPC handler or disk I/O on `~/SapaLOQ/state/`.

---

## `sapaloq-core doctor` (no-UI recovery)

Minimum CLI for config/infra validation when widget unavailable:

```bash
sapaloq-core doctor              # all checks
sapaloq-core doctor --fix        # safe auto-fixes only (mkdir, default node row)
sapaloq-core doctor --json       # machine-readable exit payload
```

| Check | Pass criteria |
|-------|---------------|
| `config.json` | Loads; commands registry valid |
| Runtime dirs | `run/`, `memory/`, `state/`, `vault/` writable |
| Socket | `sapaloq.sock` path writable |
| LLM bridge | Cursor credentials via autoload (`process.env` → `.env` → `state.vscdb`) |

Config **schema migration** is implemented: `Load` decodes to a raw map, runs
an ordered upgrade chain (`internal/config/migrate.go`,
`CurrentSchemaVersion = 1.5.0`; lower → upgrade + persist, equal → no-op,
higher → load as-is) before unmarshalling. The 1.2 migration aligns active
`skills.dir`, `prompts.dir`, and `events.bus.walPath` names; **1.5.0** appends
missing lifecycle tools (`sapaloq_stop`, etc.) to sub-agent `allowedTools`. Still planned:
`os.json` regeneration checks and a unified SQL migration runner.
`maxParallelTools` is additive and receives its default through
`OrchestratorConfig.WithDefaults`, so existing 1.2 configs do not require a
destructive rewrite.

```bash
sapaloq-core doctor              # current checks
```

Legacy spec (not all implemented yet):

```bash
sapaloq-core doctor --fix        # safe auto-fixes only (mkdir, default node row)
sapaloq-core doctor --json       # machine-readable exit payload
```
