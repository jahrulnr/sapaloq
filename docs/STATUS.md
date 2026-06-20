# SapaLOQ — Implementation Status

> Single source of truth for **what is actually implemented in code** vs what is
> still doc-only. Verify claims against the cited Go files, not against other docs.
> Last updated: 2026-06-20

Legend: ✅ implemented · 🟡 partial · ❌ not implemented (doc/config-only)

---

## Subsystem status

| # | Subsystem | Status | Evidence / notes |
|---|-----------|--------|------------------|
| 1 | Execution modes Ask / Plan / Agent | ✅ | `internal/core/orchestrator/conversation.go` (`runConversation`), `tasks.go` (`handleAskTool`), roles `planner` / `task-runner` |
| 2 | Sub-agent tool loop + per-role profiles | ✅ | `subagent.go` (`runSubAgentLoop`, `handleSubAgentTool`), `tools.go` (`toolsForRole`); `maxTurns` read from config (`roleMaxTurns`) |
| 3 | Assessment tools (read/search/list_dir, web_search/fetch) | ✅ | `tools_workspace.go`, `tools_web.go`, dispatch in `tools_dispatch.go` |
| 4 | Write/exec tools (write_file/create_file, terminal_run) | ✅ | `tools_workspace.go`; gated to `task-runner` in `subagent.go` |
| 5 | In-place edit / delete / glob tools | ✅ | `tools_workspace.go` (`toolEditFile`, `toolDeleteFile`, `toolGlob`) — added 2026-06-20 |
| 6 | `read_file` binary guard + line-range read | ✅ | `tools_workspace.go` (`toolReadFile`: NUL/non-printable sniff + `offset`/`limit` line range) — added 2026-06-20 |
| 7 | Plan artifact + handoff | ✅ | `subagent.go` (`sapaloq_write_plan_markdown`, `readPlanMarkdown`, `buildSubAgentMessages`); `latestPlanTaskID` requires real `plan.md` |
| 8 | Plan iteration (revise before finishing) | 🟡 | `write_plan_markdown` is non-terminal; planner can rewrite + read its own plan. No approval-gate UI; no post-handoff agent amend |
| 9 | Clarification loop | ✅ | Two-way: `sapaloq_request_clarification` pauses, `sapaloq_answer_clarification` resumes the paused sub-agent loop (transcript replayed, answer nudge injected). `tasks.go`, `subagent.go`, `tools.go`, `session.go` |
| 10 | Vault audit log | ✅ | `internal/vault`, wired via `Orchestrator.auditTool` (`chat.go`) at Ask + sub-agent chokepoints; cursor-bridge logs undeclared calls |
| 11 | Compaction (session + mid-run) | ✅ | `chat.go` (`compactActiveSession`), `conversation.go` |
| 12 | Provider bridge (openai/claude/kimi + tool schema) | ✅ | `internal/bridges/provider`; per-tool JSON schema via `toolschema.go` |
| 13 | Cursor bridge (live stream, alias coercion, vault) | ✅ | `internal/bridges/cursor` |
| 14 | Widget UI (chat, streaming, markdown, thinking, slash) | ✅ | `cmd/sapaloq-widget`; markdown via `marked`+DOMPurify; wait countdown |
| 15 | Slash commands (/model, /thinking, /settings, /compaction, /reset) | ✅ | `internal/core/orchestrator/slash.go`, `settings.go`, `config_reload.go` |
| 16 | SQLite chat store (sessions/turns/events/snapshots/compaction) | ✅ | `internal/store/chat/store.go` (inline migrate) |
| 17 | Event bus (in-proc pub/sub) | ✅ | `internal/bus/bus.go`: topic-pattern routing (`*`/`**` via `matchTopic`/`SubscribeTopics`), JSON-lines WAL (`NewWithWAL`, non-blocking append goroutine, seq monotonic across boots), `Replay(since, fn)`, boot replay wired in `cmd/sapaloq-core/main.go` (`newEventBus`). `Subscribe` stays receive-all for the widget `watch`. Socket bus-ops (wire publish/watch) remain a follow-up |
| 18 | Context-SOP: FTS index / prefetch / anti-deep-check / intent-router | 🟡 | `facts` + `facts_fts` (FTS5-probed, LIKE fallback) now live in the chat store (`store.go` migrate, `facts.go`): `AddFact`/`SearchFacts`/`RecentFacts`/`DeleteFact`. No prefetch/anti-deep-check/intent-router yet |
| 19 | Feedback / penalty (👍👎, slices, do_not_repeat, learning_queue, bandit) | 🟡 | `feedback_events` table + `AddFeedback`/`RecentDoNotRepeat` (`feedback.go`); 👎+correction → `do_not_repeat` fact; bounded negative-guidance slice injected into Ask prompt (`session.go`); widget 👍/👎 + correction box wired (`app.go`, `ipc.go`, `main.ts`). No learning_queue / bandit yet |
| 20 | Named sub-agent roles (scribe, memory-janitor, intent-router, boundary-guard, event-watcher, learning-agent, research) | 🟡 | `scribe` is now spawnable (`sapaloq_spawn_scribe`); the sub-agent tool gate is config-driven (`roleAllows` honors `subAgents.roles[].allowedTools` with `*`-wildcards, default-deny mutation when unconfigured); `toolsForRole` offers only allowed+registered tools. memory-janitor/intent-router/boundary-guard/event-watcher/learning-agent/research still not spawnable |
| 21 | Mode-aware scribe storage mapping (personal/work/hobby) | ✅ | `scribe_write_note` resolves a destination via `storage.intents`/explicit id/mode(+kind) and appends a timestamped note, boundary-enforced to declared `storage.paths` only. `internal/config/load.go` (`StorageConfig`/`StoragePath`/`Resolve`), `scribe.go` |
| 22 | Skills system | ❌ | No skill scanning / trigger matching / injection |
| 23 | Nodes (remote sub-agents) | ❌ | No nodes table / picker / transport |
| 24 | Driver / Platform (GNOME / D-Bus notifications, `desktop_*`) | ❌ | No `internal/driver` / `internal/platform`; no desktop tools |

---

## Implemented this session (2026-06-20)

- **Markdown via library:** replaced the hand-rolled parser in the widget with `marked` + `DOMPurify` (GFM tables/headings now render). `cmd/sapaloq-widget/frontend/src/main.ts`, `style.css`.
- **Wait countdown UX:** `waiting` status now carries `wait_seconds`; the widget shows a live countdown (`waiting · 10s, 9s, …`). `internal/bridge/events.go`, `tasks.go`, `main.ts`.
- **Atomic task writes:** `writeFileAtomic` (temp + rename) fixes the `status.json` read/write race that made `sapaloq_wait` fail with "unexpected end of JSON input". `tasks.go`. Defensive retry in `readTask`.
- **No fake plan.md:** planner no longer auto-writes `plan.md` from free-form text; only `sapaloq_write_plan_markdown` does. `latestPlanTaskID` requires a real `plan.md`. `tasks.go`.
- **Tool audit:** every orchestrator-executed tool is appended to `vault/tool-calls.jsonl` (`reason: executed`). `chat.go`, `subagent.go`.
- **Config consumed:** `subAgents.roles[].maxTurns` is now read (`roleMaxTurns`); `config.example.json` `allowedTools` aligned to real tool names. `internal/config/load.go`, `subagent.go`.
- **Tool upgrade (cursor-style):** `read_file` gains binary detection + line-range (`offset`/`limit`); new `edit_file` (precise string replace), `delete_file`, `glob_file_search`. Plan made iterable. `tools_workspace.go`, `tools.go`, `subagent.go`.
- **SQLite facts + FTS (roadmap #1):** activated the dead `001_initial.sql` design inline — `facts` table plus an FTS5-probed `facts_fts` virtual table + sync triggers, with a safe LIKE fallback when FTS5 is unavailable. New `facts.go` (`AddFact`/`SearchFacts`/`RecentFacts`/`DeleteFact`, FTS-query sanitizer). `store/chat/store.go`, `facts.go`, `facts_test.go`.
- **Clarification resume (roadmap #4):** sub-agents can now be answered and resumed. `taskRecord` keeps a capped `Transcript` + `Answer`; new `sapaloq_answer_clarification` tool finds the awaiting task, sets the answer, flips status back to `in_progress`, and re-spawns the loop (transcript replayed, answer nudge appended). `tasks.go`, `subagent.go`, `tools.go`, `session.go`, `clarification_test.go`.
- **Feedback loop 👍/👎 (roadmap #2):** new `feedback_events` table + `AddFeedback`/`RecentDoNotRepeat`; a 👎 with a correction also stores a `do_not_repeat` fact. The Ask prompt now injects a short, config-bounded negative-guidance block (`feedback.maxNegativeSlicesPerTurn`). New IPC `submit_feedback` op + orchestrator `SubmitFeedback` (no-op when disabled) + widget 👍/👎 controls with an inline correction box. `store/chat/feedback.go`, `config/load.go` (`FeedbackConfig`), `session.go`, `ipc/{protocol,server}.go`, `cmd/sapaloq-widget/{app,ipc}.go`, `frontend/src/{main.ts,style.css}`, `feedback_test.go`.
- **Event bus: WAL + replay + topic routing (roadmap #5):** the in-proc bus now supports dot-delimited topic patterns (`*` one segment, `**` the rest) via `SubscribeTopics`/`matchTopic`, a non-blocking JSON-lines WAL (`NewWithWAL`) with seq monotonic across restarts, and `Replay(since, fn)`. Boot wiring in `newEventBus` enables the WAL from `events.bus.walPath` and logs replayable counts when `replayOnBoot` is set. `Subscribe` stays receive-all so the widget `watch` is unaffected. `internal/bus/bus.go`, `bus_test.go`, `config/load.go` (`BusConfig` WAL fields), `cmd/sapaloq-core/main.go`.
- **Named sub-agents + allowedTools enforcement (roadmap #3):** the per-tool hard-coded `role != "task-runner"` gates are replaced by a generic, config-driven `roleAllows(role, tool)` — `subAgents.roles[].allowedTools` (with `*`-suffix wildcards) is authoritative when present, otherwise the original default-deny-for-mutation policy applies. `toolsForRole` is now a method that offers only allowed+registered tools. New spawnable `scribe` role (`sapaloq_spawn_scribe`) with a boundary-safe `scribe_write_note` that appends timestamped notes only to declared `storage.paths` (resolved by intent/id/mode). `internal/config/load.go` (`StorageConfig`), `subagent.go`, `tools.go`, `tasks.go`, `scribe.go`, `config.example.json` (scribe + orchestrator allowedTools aligned to real tool names), `scribe_test.go`.

---

## Roadmap (deliberately deferred — each is a large feature)

1. **Context-SOP intelligence:** run `migrations/001_initial.sql`, build `facts`/`facts_fts`, prefetch + anti-deep-check, intent-router.
2. **Feedback/RL layer:** `feedback_events` table, widget 👍/👎, positive/negative prompt slices, `do_not_repeat`, `learning_queue`, contextual bandit on prefetch rules.
3. **Named sub-agents:** make scribe / memory-janitor / intent-router / boundary-guard / event-watcher / research actually spawnable; enforce `allowedTools`/`toolPolicy` from config.
4. **Clarification resume:** two-way — answer a paused sub-agent and continue its loop.
5. **Event bus completion:** topic-pattern matcher, jsonl WAL, replay-on-boot, socket bus-ops.
6. **Platform/Driver:** GNOME/D-Bus notifications, `desktop_*` tools, `os.json` detect/cache.
7. **Nodes:** remote sub-agent registry + transport.
8. **Skills:** scan `~/.config/sapaloq/skills/`, trigger matching, bounded injection.
9. **Scribe storage mapping:** mode-aware note writing to `storage.paths`.
