# SapaLOQ — Runtime: Single Binary

> **Satu binary Go** — goroutine + channel + persistence lokal. Zero external daemons.
> Last updated: 2026-06-19

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
| SQLite | `~/.config/sapaloq/memory/companion.db` | Facts, FTS, skills index, dedupe |
| jsonl | `events.jsonl`, `progress/*.jsonl` | WAL, audit, replay on boot |
| Files | `config.json`, `skills/`, `prompt/` | Agent-editable, git-friendly |
| In-memory | goroutine LRU | Session hot cache — **lost on restart OK** |

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

Sub-agent **process** (optional): still talks via `sapaloq.sock` to **same** binary's socket server — not a second broker.

---

## Failure modes (simple)

| Failure | Behavior |
|---------|----------|
| sapaloq-core crash | systemd restart; replay jsonl |
| LLM API down | Orchestrator chat degrades; queue tasks |
| SQLite locked | WAL mode + short retry + single-writer queue (see [CONTEXT-SOP.md](./CONTEXT-SOP.md#sqlite-write-concurrency-implementation-note)) |
| Slow watcher | Drop + log; never block publisher |

No cascade: "Redis failed so events broken" — **cannot happen**.

---

## systemd user unit

```ini
[Unit]
Description=SapaLOQ desktop companion
After=graphical-session.target

[Service]
ExecStart=%h/.local/bin/sapaloq-core
Restart=on-failure
Environment=SAPALOQ_HOME=%h/.config/sapaloq

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

`runtime.singleBinary: true` (always — informational lock in schema).

Memory: `engine: sqlite` only. Event wake: `events.bus` not external broker.

---

## Non-goals (runtime)

- Redis, RabbitMQ, MQTT, NATS as required deps
- Multi-node event federation
- Separate `sapaloq-broker` service
- Hot cache that must survive only in Redis

Kalau scale ke Pi ↔ desktop later: **sync jsonl/export file**, bukan install Redis di desktop.

Hard limits without full fix: [LIMITATIONS.md](./LIMITATIONS.md).

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
| `config.json` | Validates against `config.schema.json` |
| `os.json` | Present + adapter caps load |
| `companion.db` | Open, migrations current, FTS triggers exist |
| `nodes` | `local-default` row; remote rows `share_memory=0` |
| Socket | `sapaloq.sock` path writable |
| LLM bridge | `llmBridge.driver` registered; probe optional `--skip-llm` |

Exit `0` = OK; `1` = fixable warnings; `2` = blocking errors. Full UX spec deferred to M3 — enough for M1 bootstrap gate.
