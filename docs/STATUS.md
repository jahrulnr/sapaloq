# SapaLOQ — Implementation Status

> Single source of truth for **what is actually implemented in code** vs what is
> still doc-only. Verify claims against the cited Go files, not against other docs.
> Last updated: 2026-06-22

Legend: ✅ implemented · 🟡 partial · ❌ not implemented (doc/config-only)

---

## Subsystem status

| # | Subsystem | Status | Evidence / notes |
|---|-----------|--------|------------------|
| 1 | Execution modes Ask / Plan / Agent | ✅ | `internal/core/orchestrator/conversation.go` (`runConversation`), `tasks.go` (`handleAskTool`), roles `planner` / `task-runner` |
| 2 | Sub-agent tool loop + per-role profiles | ✅ | `subagent.go` (`runSubAgentLoop`, `handleSubAgentTool`), `tools.go` (`toolsForRole`); `maxTurns` read from config (`roleMaxTurns`). Role policy is checked before shared-tool dispatch, so undeclared/provider-poisoned calls cannot bypass the role allowlist |
| 3 | Assessment tools (read/search/list_dir, web_search/fetch) | ✅ | `tools_workspace.go`, `tools_web.go`, dispatch in `tools_dispatch.go` |
| 4 | File + exec tools (`read_file`/`write_file`/`create_file`/`edit_file`/`delete_file`/`search`/`list_dir`/`glob`, `exec`) | ✅ | `tools_workspace.go`; flat unrestricted surface (any path; no workspace sandbox). Mutating file tools gated to `task-runner`; `exec` available in every mode |
| 5 | In-place edit / delete / glob tools | ✅ | `tools_workspace.go` (`toolEditFile`, `toolDeleteFile`, `toolGlob`) — added 2026-06-20 |
| 6 | `read_file` binary guard + line-range read | ✅ | `tools_workspace.go` (`toolReadFile`: NUL/non-printable sniff + `offset`/`limit` line range) — added 2026-06-20 |
| 7 | Plan artifact + handoff | ✅ | `subagent.go` (`sapaloq_write_plan_markdown`, `readPlanMarkdown`, `buildSubAgentMessages`); `sapaloq_spawn_agent.plan_task_id` is explicit and validated as same-session, completed Planner work with a real `plan.md`. No implicit latest-plan attachment |
| 8 | Plan iteration (revise before finishing) | 🟡 | `write_plan_markdown` is non-terminal; planner can rewrite + read its own plan. Ask prompt requires user review before passing `plan_task_id`, but no approval-gate UI/state machine yet; no post-handoff agent amend |
| 9 | Clarification loop | ✅ | Two-way: `sapaloq_request_clarification` pauses, `sapaloq_answer_clarification` resumes the paused sub-agent loop (transcript replayed, answer nudge injected). `tasks.go`, `subagent.go`, `tools.go`, `session.go` |
| 10 | Vault audit log | ✅ | `internal/vault`, wired via `Orchestrator.auditTool` (`chat.go`) at Ask + sub-agent chokepoints; cursor-bridge logs undeclared calls |
| 11 | Compaction (session + mid-run) | ✅ | `chat.go` (`compactActiveSession`), `conversation.go` |
| 12 | Provider bridge (openai/claude/kimi + tool schema) | ✅ | `internal/bridges/provider`; per-tool JSON schema via `toolschema.go` |
| 13 | Cursor bridge (live stream, alias coercion, vault) | ✅ | `internal/bridges/cursor` |
| 14 | Widget UI (chat, streaming, markdown, thinking, slash) | ✅ | `cmd/sapaloq-widget`; markdown via `marked`+DOMPurify; wait countdown; one durable lifecycle card per background `task_id`, rehydrated on watcher reconnect |
| 15 | Slash commands (/model, /thinking, /settings, /compaction, /reset) | ✅ | `internal/core/orchestrator/slash.go`, `settings.go`, `config_reload.go`. `/settings` currently supports deterministic `patch <json>`/`show`; natural-language settings sub-agent remains deferred. Unsupported, no-op, and restart-only patch paths are rejected |
| 16 | SQLite chat store (sessions/turns/events/snapshots/compaction) | ✅ | `internal/store/chat/store.go` (inline migrate) |
| 17 | Event bus (in-proc pub/sub) | ✅ | `internal/bus/bus.go`: topic-pattern routing (`*`/`**` via `matchTopic`/`SubscribeTopics`), JSON-lines WAL (`NewWithWAL`, non-blocking append goroutine, seq monotonic across boots), `Replay(since, fn)`, boot replay wired in `cmd/sapaloq-core/main.go` (`newEventBus`). `Subscribe` stays receive-all for live widget updates; IPC `watch` also rehydrates recent task snapshots from durable `status.json`, so reconnect cannot silently lose completion/failure. Terminal task transitions also **speak** a durable assistant turn + `response_delta` republish (`completion.go`), so a finish landing after `sapaloq_wait` is surfaced in chat, not just as a card |
| 18 | Context-SOP: FTS index / prefetch / anti-deep-check / intent-router | 🟡 | `facts` + `facts_fts` (FTS5-probed, LIKE fallback) now live in the chat store (`store.go` migrate, `facts.go`): `AddFact`/`SearchFacts`/`RecentFacts`/`DeleteFact`. No prefetch/anti-deep-check/intent-router yet |
| 19 | Feedback / penalty (👍👎, slices, do_not_repeat, learning_queue, bandit) | 🟡 | `feedback_events` table + `AddFeedback`/`RecentDoNotRepeat` (`feedback.go`); 👎+correction → `do_not_repeat` fact; bounded negative-guidance slice injected into Ask prompt (`session.go`); widget 👍/👎 + correction box wired (`app.go`, `ipc.go`, `main.ts`). No learning_queue / bandit yet |
| 20 | Named sub-agent roles (scribe, memory-janitor, intent-router, boundary-guard, event-watcher, learning-agent, research) | 🟡 | `scribe` is now spawnable (`sapaloq_spawn_scribe`); the sub-agent tool gate is config-driven (`roleAllows` honors `subAgents.roles[].allowedTools` with `*`-wildcards, default-deny mutation when unconfigured); `toolsForRole` offers only allowed+registered tools. memory-janitor/intent-router/boundary-guard/event-watcher/learning-agent/research still not spawnable |
| 21 | Mode-aware scribe storage mapping (personal/work/hobby) | ✅ | `scribe_write_note` resolves a destination via `storage.intents`/explicit id/mode(+kind) and appends a timestamped note, boundary-enforced to declared `storage.paths` only. `internal/config/load.go` (`StorageConfig`/`StoragePath`/`Resolve`), `scribe.go` |
| 22 | Skills system | 🟡 | Scan + trigger/FTS match + bounded injection done; learning-agent skill *writing* still deferred |
| 23 | Nodes (remote sub-agents) | 🟡 | nodes table + local-default bootstrap + role/priority picker + local spawn routing (no behavior change) + remote Transport (ws) behind a connect probe + fake for tests; full remote execution wiring (envelope→runSubAgentLoop bridge) + /settings node CRUD still deferred |
| 24 | Driver / Platform (GNOME / D-Bus notifications, `desktop_*`) | 🟡 | `internal/platform` abstraction + headless + freedesktop/gnome D-Bus adapter (behind session-bus probe) + `desktop_notify`/`desktop_dnd_status` + notify→bus bridge; window/screenshot/clipboard still deferred |
| 25 | Replaceable per-mode system prompts (Ask/planner/agent/scribe) | ✅ | `internal/prompts` — embedded defaults materialized to `~/.config/sapaloq/prompts` with a sha256 manifest; user edits preserved, unmodified files upgraded when the shipped default changes. Wired via `Orchestrator.systemPrompt` in `session.go` (Ask) + `subagent.go` (planner/agent/scribe). `config.prompts.{enabled,dir}` |
| 26 | Host command tool (`exec`) | ✅ | Run any command anywhere (any path; optional `cwd`), also reads any host file via cat/sed/head/tail/rg; available in **every** mode via the shared dispatcher. `tools_workspace.go` (`toolExec`), in `askTools`/`planTools`/`agentTools`, dispatched in `tools_dispatch.go`. Merged the former `system_exec` + `terminal_run` into one flat `exec` (2026-06-21) |
| 27 | Config schema migration / versioning | ✅ | `internal/config/migrate.go` — `CurrentSchemaVersion` (1.1.0) + semver compare; lower version → ordered upgrade chain (old JSON formats preserved, additive/idempotent), equal → no-op, higher → load as-is (forward-compat; mandatory-empty validated post-load via `LLMBridge.Validate`). Hooked into `Load`, upgraded config persisted atomically |
| 28 | Vault audit log rotation / retention | ✅ | `internal/vault/vault.go` — size-based numbered rotation in `Writer.Append` (primary → `.1` → `.2` …, oldest beyond keepFiles dropped), `Options{MaxBytes,KeepFiles}` + `NewWithOptions` (defaults 5 MiB / keep 3; `New` unchanged). `ReadRecent` spans rotated siblings. `config.vault.{maxLogBytes,keepRotatedFiles}`, wired in `chat.go`; cursor-bridge writer inherits default rotation |
| 29 | Local image vision tool (`read_image`) | ✅ | Reads a local image file (png/jpeg/gif/webp) into the model's vision in **every** mode. `toolReadImage` (`tools_system.go`) returns inline `![name](data:<mime>;base64,…)` markdown that `extractImages` re-ingests into `bridge.Request.Images` — the same vision channel as widget attachments (no base64-as-text). In Ask, `runConversation` now re-extracts images from each tool-results turn (+`visionAllowed` guard); Plan/Agent inherit it automatically. In `readOnlyAssessmentTools` + `reg()` schema. Mime via extension map + `http.DetectContentType` fallback; 10 MiB cap; bypasses the text `looksBinary` guard |

---

## Fixed this session (2026-06-21) — chat race: duplicated / interleaved assistant bubble

Regression introduced by the "error auto-follows to chat" note below: that change
made `cmd/sapaloq-widget/app.go`'s `watchEvents` forward **all** `EventResponseDelta`
from the bus to `sapaloq:stream`. But a **live chat turn's** `response_delta` is
ALSO published on the bus, and it already reaches the webview via the per-request
`SendMessage`/`RetryChatTurn` stream. So every live delta was delivered **twice**
to the single `sapaloq:stream` listener and fed into the live renderer twice —
producing a duplicated bubble and character interleave ("MantMantap, agent lagi
jalanap").

- **Root fix — forward only spoken-completion deltas from `watch`.** Completion
  deltas are the *only* `response_delta`s that have no per-request stream, and the
  orchestrator now stamps them with `TaskID` (`completion.go`). `watchEvents`
  forwards a `response_delta` **only when `event.TaskID != ""`**; live-turn deltas
  (no TaskID) flow solely through the per-request stream. No more double source.
- **Defence in depth in the widget.** `frontend/src/main.ts` renders a
  `response_delta` that carries `task_id` as its **own** assistant bubble (never
  fed into the shared live renderer, so two concurrent completions can't
  interleave) and **dedupes per `task_id`** via `spokenTaskIDs` (cleared in
  `clearMessages`), so a re-published terminal transition can't append twice.
- **Cleanup.** Removed the dead `o.progress.Append(...)` for the spoken completion
  in `completion.go` (write-only `orch-<id>.jsonl`, never replayed to clients;
  was also misusing `record.ID` as the session id).
- **Note on the native fail message.** "executor stopped without calling
  sapaloq_complete_task or sapaloq_fail_task after 2 idle nudges" is a **native**
  Go watchdog error (`subagent.go`, `const maxIdleNudges = 2`), not an LLM/provider
  error — expected when a `task-runner` narrates intent without calling a tool.
- **Verify:** `go build/vet/test ./...` + `-race` orchestrator green; widget
  `tsc --noEmit` green. Widget binary must be rebuilt (`make run` / `make
  widget-build`) for the `app.go` + `main.ts` changes to take effect.

---

## Implemented this session (2026-06-22) — stop caging the model: structural liveness + no-limit budgets

The recurring "worker stalled / task failed" pain was traced to two separate
mistakes, both now corrected:

- **Real fix — structural worker liveness.** Sub-agent heartbeat used to be
  event-driven (emitted from inside the inference loop), so a legitimate long
  synchronous tool/stream produced no heartbeat and the watchdog false-killed a
  *healthy* worker. Liveness is now a ticker in `runBackgroundTask` tied to the
  goroutine's life; `subagentSink.beat` only annotates phase (`workers.setPhase`),
  never the heartbeat. The watchdog now only catches a genuinely wedged goroutine.
  `tasks.go`, `worker.go`, `turnloop.go`, `subagent.go`.
- **Reverted bad guard — narration is NOT a failure.** A short-lived
  `maxToollessTurns` guard failed a run when the model "narrated without calling
  a tool". That penalised healthy thinking-before-acting (frontier models do this
  too). Removed entirely; a tool-less turn just gets a plain, non-coercive
  continuation reminder. `conversation.go`, `subagent_stream_retry_test.go`.
- **No-limit budgets (AGENTS.md golden rule #5).** Premature count caps were
  themselves the bug source. The shipped runtime config now sets
  `continuation.{maxInferenceTurns,maxToolCalls,maxNoProgressTurns,maxIdenticalToolCalls}`
  and `subAgents.roles.task-runner.maxTurns` to effectively-unlimited values;
  `maxWallTimeMinutes` (180) is the single final safety net. `roleMaxTurns` lost
  its upper clamp (floor-only). `subagent.go`, `fixes_test.go`, `config.json`.
- **Informational usage readout.** Each continuation carries a one-line
  `[Usage] turn N · tool-calls so far M` so the model can self-pace without being
  throttled. `conversation.go`, `conversation_test.go`.
- **SSE robustness (earlier in arc).** `pumpSSE` only resets the idle timer on
  real data (not blank keep-alives); `runSSE` uses a client with dial/TLS/header
  timeouts so a slow TTFB is bounded too. `internal/bridges/provider/wire.go`.
- **Why a run still ended at "30 turns" before this change:** `task-1782110368069272648`
  was productive (wrote a real 19 KB `index.html`) but spent 3 turns because the
  model wrote the big file via `exec`+heredoc which truncated at 502 bytes, then
  recovered with `python3 -c`. Not a stall, not a weak model — just an arbitrary
  turn cap that has now been lifted.
- **Transient transport retry (was: timeout → instant fail, no retry).**
  `task-1782112420468169101` failed with `Post .../v1/chat/completions:
  net/http: timeout awaiting response headers` — one slow provider request
  killed the whole task because only image-rejection and context-overflow had
  retry paths. Added a third `EventError` branch: a transient transport error
  (timeout / reset / EOF / `5xx` / `429`) retries the same turn with exponential
  backoff (`transportRetryBaseBackoff`, capped 5s) up to `maxTransportRetries=4`,
  resetting the counter after a clean turn. Deterministic errors (auth, bad
  request, context overflow) are not retried here. `conversation.go`
  (`looksLikeTransientTransport`), `subagent_stream_retry_test.go`
  (`TestTaskRunnerRetriesTransientThenSurfaces`, `TestTaskRunnerRecoversFromTransientError`).
- **Inline tool-call reassembly — the real "files never written" root cause.**
  `task-1782117165538175015` failed after 39 turns: the model wrote a real
  diagnosis ("tool invocations were not emitted when file content contained
  patterns the parser interpreted as tool-call syntax"), and the progress log
  proved it — of 7 parsed calls, all were *small* (`mkdir` ×5, `update_progress`,
  `fail_task`); not one carried HTML/CSS/JS. Cause: MiniMax emits big tool calls
  inline in the **content** channel, streamed across many deltas, and
  `emitText`→`ParseToolCallLeak` scanned **one delta at a time** — a balanced
  `{...}` for a large argument never appears in a single delta, so the call was
  lost as text. Fix: a per-stream `leakScanner` (`bridge.go`) accumulates content
  and scans the buffer from a moving frontier, emitting each reassembled call as
  a real `EventToolCall`. `scanOneJSONObject` is now **string-aware** (braces
  inside a JSON string value no longer close the object early — essential for
  file bodies), and matches are **gated to `DeclaredTools`** to avoid misreading
  JSON inside file content. `leak.go` (`ParseToolCallLeakFrom`), `bridge.go`
  (`leakScanner`), `handlers.go` flow; tests `leak_test.go`
  (`...ReassemblesAcrossFragments`, `...IgnoresUnknownNames`),
  `leak_scanner_test.go`.
- **Verify:** `go build/vet/test ./...` green (22 pkgs). Config hot-reloads via
  `StartConfigWatcher`; new tasks pick up the budgets without a restart.

---

## Implemented this session (2026-06-21) — error auto-follows to chat + configurable inference timeout

Two issues seen when a sub-agent failed: the orchestrator looked "silent" (only
a passive task card, no chat follow-up), and the failure itself was an opaque
"context deadline exceeded".

- **Spoken completion now auto-follows into the chat live.** The orchestrator
  already persisted + republished the terminal outcome as a `response_delta` on
  the bus, but the widget's `watch` callback only forwarded `EventTaskUpdate`,
  so the spoken failure/success was dropped live (it only appeared as a card,
  and in chat history on reload). `cmd/sapaloq-widget/app.go` now also forwards
  `EventResponseDelta`; `frontend/src/main.ts` renders an idle `response_delta`
  (no turn in flight) as a new assistant bubble. So a sub-agent that finishes or
  fails AFTER the chat turn closed now speaks into the conversation, not just a
  card. **(Superseded by the race fix above — `watch` now forwards only
  `TaskID`-stamped completion deltas, never live-turn deltas, to avoid a
  double-delivered/interleaved bubble.)**
- **Inference request timeout is configurable; default raised 120s → 600s.**
  Both bridge wires had a hardcoded 120s per-request timeout that truncated long
  sub-agent steps (e.g. generating a large file) into "context deadline
  exceeded". New `llmBridge.providers[].requestTimeoutSec` (default 600) flows
  through `LLMBridge.RequestTimeout()` into the **provider bridge**
  (`WireOptions` → `buildHTTPRequest`, used by tokenrouter/OpenAI/Claude/Kimi)
  **and** the cursor bridge (`StreamOptions`/`AgentStreamOptions`).
- **Deadline errors are now actionable in BOTH bridges.** `explainStreamError`
  (`internal/bridges/provider/bridge.go` + `internal/bridges/cursor/bridge.go`)
  maps "context deadline exceeded" to "inference request timed out after Ns
  (set …requestTimeoutSec higher …)".
- **Tests:** `internal/config/timeout_test.go` (default/override, guards >120s),
  `internal/bridges/cursor/timeout_test.go` and
  `internal/bridges/provider/timeout_test.go` (error wrap, entry→timeout).

---

## Implemented this session (2026-06-21) — fire-and-forget delegation (no chat freeze)

Follow-up to the completion fix: delegation no longer blocks the chat.

- **`waitForTaskChange` ignores non-terminal progress** (`tasks.go`). It used to
  return on any `UpdatedAt` bump, so an agent calling `sapaloq_update_task_progress`
  woke the orchestrator with "changed to in_progress", which tended to re-wait —
  freezing the ring at "working" in a wait→progress→wait loop. It now ends only
  on a terminal state or a real status *transition*; pure progress is surfaced
  live as a task card via the watch stream instead.
- **Prompt now fire-and-forget** (`internal/prompts/defaults/ask.md`). After a
  spawn the orchestrator replies briefly and ends its turn; `sapaloq_wait` is
  demoted to opt-in (only when the user explicitly asks to block), since
  terminal completions are spoken automatically. Unmodified user prompts upgrade
  via the prompt manifest.
- **Tests:** `TestWaitIgnoresNonTerminalProgress`, `TestWaitReturnsOnStatusTransition`
  (`conversation_test.go`).

---

## Implemented this session (2026-06-21) — worker health + event-driven completion that speaks

Fixes the "after delegating to planner/agent we never know if it finished"
bug observed in the widget (agent spawned → `sapaloq_wait` → "still in_progress"
→ generation ends → completion never surfaced).

- **Per-worker identity + health watchdog.** New `internal/core/orchestrator/worker.go`
  adds a `workerRegistry`: every background sub-agent registers a `WorkerHandle`
  (id, role, session, **PID** = `os.Getpid()` today, phase, heartbeat) and
  heartbeats each turn / tool call (`subagent.go`). A watchdog
  (`StartWorkerWatchdog`, wired in `cmd/sapaloq-core/main.go`) force-fails any
  worker with no heartbeat within `completion.staleAfterSec`, so a wedged
  goroutine can no longer masquerade as `in_progress` forever. Live health is
  also persisted to `memory/workers/<task-id>/health.json` for outside-process
  inspection. (PID field is first-class so a future real-subprocess upgrade via
  `internal/node` Transport needs no consumer/schema change.)
- **Per-worker error-only log.** `worklog.go` writes
  `memory/workers/<task-id>/error.log` (errors only — separate from the verbose
  progress JSONL) on inference errors, task failure, and stalls. Gated by
  `completion.workerErrorLog` (default on).
- **Event-driven completion now SPEAKS.** `completion.go`
  (`speakTaskCompletion`) injects a durable assistant turn into the task's
  session **and** republishes it as a `response_delta` on the bus on every
  terminal transition (done/failed/awaiting/stopped). Hooked into
  `publishTaskUpdate`, idempotent per task id, gated by
  `completion.speakOnTerminal` (default on). This closes the loop: a completion
  landing **after** `sapaloq_wait` returns is surfaced as a real chat message,
  not just a card.
- **`sapaloq_wait` no longer over-promises.** The "still in_progress after the
  wait window" reply now tells the user the result will be delivered
  automatically instead of implying continued watching (`tasks.go`).
- **Config:** wired the previously-inert `orchestrator.completion` block
  (`HeartbeatIntervalSec`/`StaleAfterSec` now drive the watchdog) and added
  `speakOnTerminal` + `workerErrorLog` (`load.go`, `schema/config.schema.json`,
  `config/config.example.json`). New `memory/workers` runtime dir
  (`internal/config/paths.go`).
- **Tests:** `worker_test.go` (stall→fail, healthy untouched, health snapshot),
  `completion_test.go` (spoken-on-terminal regression, idempotent, opt-in,
  end-to-end via `runBackgroundTask`).

---

## Implemented this session (2026-06-21) — flat unrestricted tool surface

- **Removed the `workspace_`/`system_`/`terminal_` prefixes.** The split between
  boundary-rooted `workspace_*` tools and an "unrestricted" `system_exec` was an
  AI-coding mistake that created bugs (the model got bound to the process CWD and
  failed on any path outside it) and forced it to choose between overlapping
  tools. SapaLOQ is unrestricted by design (less-bugs over security), so the
  surface is now flat: `read_file`, `write_file`, `create_file`, `edit_file`,
  `delete_file`, `search`, `list_dir`, `glob`, `exec` (and `read_image`).
- **Dropped the workspace sandbox.** `resolveInWorkspace`/`workspaceRoot` are
  gone; every `path` accepts absolute, `~`-relative, or CWD-relative input. New
  `TestFileToolsAreNotWorkspaceBound` proves CRUD works on a path outside the
  CWD with no boundary rejection.
- **Merged `system_exec` + `terminal_run` → `exec`** (one host command tool,
  optional `cwd`). `exec` stays exploration (non-mutating) so Ask/Plan keep it.
- **Schema migration `1.2.0 → 1.3.0`** renames any legacy `allowedTools` entries
  in pre-existing configs (de-duplicating `system_exec`+`terminal_run` → `exec`)
  so the rename is not a silent break. `TestMigrate120FlattensToolNames`.
- Updated default prompts, `config.example.json`, `schema` version, and the
  contract/role tests to the flat names. `internal/core/orchestrator/{tools.go,
  tools_workspace.go,tools_system.go,tools_dispatch.go,subagent.go}`,
  `internal/config/{migrate.go,migrate_test.go}`, `internal/prompts/defaults/*`.

## Audit this session (2026-06-21) — architecture remediation

- Completed a full docs/config/runtime audit and added
  [REMEDIATION-PLAN.md](./REMEDIATION-PLAN.md). The audit preserves
  `system_exec` in Ask and Plan as an intentional exploration capability.
- Confirmed critical contract drift: the example config, JSON schema, and Go
  config structs disagree; multiple documented orchestrator controls are not
  consumed; and shared sub-agent tools are dispatched before role enforcement.
- P0 fixes started: public config reduced to active runtime fields; schema
  parity + load tests added; schema migration bumped to 1.2.0; nested
  `/settings` paths now reject roadmap-only no-ops; and shared sub-agent calls
  are role-gated before dispatch while preserving Planner `system_exec`.
- Plan → Agent binding no longer guesses from the latest session plan. Agent
  receives a plan only through an explicit validated `plan_task_id`.

## Implemented this session (2026-06-21) — durable sub-agent certainty

- Reconstructed `task-1782049903924067527`: provider/tool turns succeeded, but
  `/tmp/profile/index.html` was never created; the executor only narrated its
  next action and was incorrectly persisted as `done`.
- Task-runner completion is now explicit. After bounded idle nudges or turn
  exhaustion without `sapaloq_complete_task`/`sapaloq_fail_task`, the task is
  persisted as `failed` with a concrete reason. Provider `EventDone` no longer
  leaks into progress JSONL as fake task completion.
- Every lifecycle transition (`pending`, `in_progress`, terminal/notable state)
  emits and persists `EventTaskUpdate`; `notifyUserOnDone` no longer suppresses
  chat certainty. Tool execution also emits a concise activity update.
- IPC `watch` sends recent durable task snapshots before live bus events.
  Frontend cards are keyed by `task_id`, update in place, and drive the ring
  state. Core startup marks detached `pending`/`in_progress`/`stopping` tasks
  as explicit orphan failures.
- Regression coverage:
  `subagent_completion_test.go` checks false-done prevention, bridge-done
  separation, durable snapshots, and restart recovery;
  `test/e2e/ipc_test.go` checks late watcher catch-up.

## Implemented this session (2026-06-21) — thinking persistence + widget polish

- **Reasoning now persists across restarts.** Previously the thinking stream was render-only — killing/restarting the core lost it from history. `runConversation` now accumulates `EventThinkingDelta` into an optional `*strings.Builder` (`thinkingOut`); `SendChat`/`RetryChat` persist it as a `chat_turns` row with role `"thinking"` (token estimate 0) **before** the assistant turn. The thinking turn is **show-only**: excluded from the LLM context window (`contextMessages` skips `role=="thinking"`) and from the compaction summary, so reasoning never gets replayed back into the model. Widget `renderTurn` rebuilds it via a new `appendThinkingBubble` (collapsed, re-expandable). `conversation.go`, `chat.go`, `session.go`, `conversation_test.go` (`TestRunConversationCapturesThinking`), `cmd/sapaloq-widget/frontend/src/main.ts`.
- **Live vs finished thinking are now visually distinct.** A settled reasoning bubble was indistinguishable from a streaming one. `flushStream` now adds `is-done` + relabels `thinking`→`thought`; CSS renders the done state with the pulse hidden, a steady green `✓`, and a calm green pill tint. `main.ts`, `style.css`.
- **Removed the "Ask, route, delegate" empty-state card.** It didn't scroll with content, vanished after hide→reopen, and only cramped the panel. Dropped the template block + its CSS. `main.ts`, `style.css`.

## Implemented this session (2026-06-21) — context-token accounting fix + shadow removal

- **Bug: context usage (topbar `N/1M`) under-counted, which also made auto-compaction trigger late.** `ContextUsage` sums `token_estimate` over `chat_turns`, but tool calls/results — though they ARE sent to the model (appended to `cleanMessages` and replayed via `contextMessages`, which maps `tool`→`assistant`) — were never `AppendTurn`'d, so they cost 0 in the accounting. `runConversation` now persists each tool-results message as a `"tool"` turn with a real `estimateTextTokens` estimate (using the outer non-cancelable ctx so a wall-time timeout doesn't drop the record; nil-`chat` guarded for tests). This fixes both the displayed usage **and** the start-of-turn auto-compact, which reads that same DB usage against `autoCompactPercent`. `core/orchestrator/conversation.go`, `store/chat/store_test.go` (`TestUsageCountsAllRolesIncludingToolTurns`).
- **Also count fixed prompt overhead.** `Orchestrator.ContextUsage` now adds the Ask system prompt + negative-guidance block token estimate on top of the turn sum (and recomputes `percent`), since that overhead is sent every request but never stored as turns. The chat store's `Usage` stays pure (turns only); the overhead is layered in the orchestrator wrapper. `core/orchestrator/session.go`.

## Implemented this session (2026-06-21) — sub-agent "kepentok" fix + completion trigger

Root cause of sub-agents stalling ("kepentok") with no continuation, plus the missing "speak"-style event that should tell chat (Ask) a sub-agent finished. Three interconnected bugs:

- **Bug #1 — live config `allowedTools` named non-existent tools (the root cause).** `sapaloq-config/config.json` `subAgents.roles[task-runner/planner/scribe].allowedTools` used abstract doc names (`gnome_*`, `exec`, `write_file`, `mcp:*`, `emit_progress`, `ask_orchestrator`) that match **no** registered Go tool. Because `roleAllows` treats a present allowlist as authoritative, this silently default-denied **every real tool**, so the agent could never act (jsonl evidence: `workspace_list_dir` → "skip workspace_*, pakai system_exec" → denied → tool-less turn → premature done). Fixed the live config to real tool names (`workspace_*`, `terminal_run`, `system_exec`, `read_image`, `web_search`/`web_fetch`, `desktop_*`, `sapaloq_*`). `config.example.json` was already correct from 2026-06-20; the live config had drifted.
- **Defense-in-depth in code.** `roleAllows` now calls a new `allowlistMatchesKnownTool()` — if a configured allowlist matches **zero** registered tools (typo/drift), it falls back to the static per-role default policy + warns, so a broken config can never again silently disarm an agent. `subagent.go`, `subagent_completion_test.go` (`TestRoleAllowsFallsBackOnUnknownAllowlist`, `TestRoleAllowsHonorsValidAllowlist`).
- **Bug #2 — task-runner finished prematurely on a tool-less turn.** `runSubAgentLoop` previously set `Status="done"` whenever a turn had no tool calls. For `task-runner` that's wrong when it merely narrated intent. Added `idleNudges`/`maxIdleNudges=2`: a tool-less task-runner turn injects a bounded nudge to act or call `sapaloq_complete_task`/`sapaloq_fail_task`; if it still does neither, the task fails explicitly. `planner`/`scribe` retain natural no-tool completion. `subagent.go`, `subagent_completion_test.go`.
- **Bug #3 — completion trigger ("speak" event) was config/doc-only.** Lifecycle updates now publish end-to-end and are durable. Success is always visible in chat regardless of `notifyUserOnDone`; late/reconnected watchers receive `status.json` snapshots before live events, and widget cards update in place by task id. `tasks.go`, `ipc/server.go`, `main.ts`, `subagent_completion_test.go`, `test/e2e/ipc_test.go`.
- **Note — `thinking` token=0 is intentional, not a bug.** Thinking turns are skipped in `contextMessages` (never replayed to the LLM) and excluded from the compaction summary, so they genuinely don't consume the window; leaving their estimate at 0 is correct. Sub-agent loop tool accounting is out of scope here (sub-agents track their own task records, not `chat_turns`).
- **Removed the popup drop shadow entirely.** The transparent window (`overflow:hidden`) hard-clips any outer shadow at the edge, leaving a thin ragged line that looked worse than none — especially on light backgrounds. Depth now comes from the border + inset top highlight; dock padding reverted to `10px`. `cmd/sapaloq-widget/frontend/src/style.css`.
- **Removed the orb (collapsed/non-popup mode) drop shadow.** `.orb-body` had `box-shadow: 0 14px 30px …` whose lower half got clipped by the transparent window edge, leaving a gray smudge under the orb on light backgrounds. Dropped the outer shadow, kept the inset rim highlight; depth/glow still come from `.orb-aura`/`.orb-ring`/`.orb-specular`. Verified on a forced-white background. `cmd/sapaloq-widget/frontend/src/style.css`.
- **Unified the composer into a single pill (ChatGPT-style).** The compose bar used to be three separate bordered boxes in a flex row (`.compose-wrap` + standalone `.attach-btn` + gradient `.send-btn`), which read as disjointed/garish. Restructured the markup so `＋`, the textarea, and the send button now live inside one rounded `.compose-wrap` via a new `.compose-row`; `＋` is a flat ghost icon and send is a flat solid circle using `--accent` (dropped the yellow→cyan gradient and the outer glow; `stop` state is now solid `--danger`). All element ids preserved so the attachment/send handlers are untouched. Verified visually against `tmp/gpt*.png`. `cmd/sapaloq-widget/frontend/src/main.ts`, `cmd/sapaloq-widget/frontend/src/style.css`.
- **Replaced raw Unicode glyphs with inline stroke SVG icons.** The composer buttons rendered bare text glyphs (`＋`, `↗`, `■`) that looked stiff/"icon-y". Swapped them for self-authored inline SVGs (no external sprite sheet or icon font — the same approach as the orb's `.sapa-glyph`): a rounded plus for attach, an arrow-up for send (matching ChatGPT), and a filled rounded square for the streaming/stop state. `setSubmittingUI()` now swaps `button.innerHTML` between `ICON_SEND`/`ICON_STOP` consts instead of setting `<span>.textContent`. SVG sizing/colour comes from `.attach-btn svg`/`.send-btn svg` rules (`fill:none; stroke:currentColor; stroke-linecap/linejoin:round`; stop variant uses `fill:currentColor`). NB: ChatGPT's exported `*.svg` are just `<use href="…/sprites-core-….svg#id">` references into a proprietary CDN sprite sheet — not usable directly, hence the inline approach. `cmd/sapaloq-widget/frontend/src/main.ts`, `cmd/sapaloq-widget/frontend/src/style.css`.
- **Fixed the drag overlay getting stuck when a drag passes over SapaLOQ but is dropped on another app.** The "Lepas untuk attach file" overlay (`#popup.is-dragging-file`) is gated by a `dragDepth` counter, but a drag that merely hovers over the widget and drops elsewhere gives us no terminating event (no `drop` here, an unreliable final `dragleave`, no `dragend` for external sources) — and the `document`-level `dragover` even incremented the counter with no matching clear, so the overlay latched on. Added safety-nets in `main.ts`: (1) an idle timer re-armed on every `dragover` that force-clears after ~220ms once `dragover` stops firing (the pointer has left the window) — the primary fix for WebKitGTK/cross-app drops; (2) a `document` `dragleave` that force-clears when leaving to a null `relatedTarget` or to coordinates at/outside the viewport; (3) a `window` `dragend` force-clear for in-webview drag sources. The timer is cleared on real drops/`OnFileDrop`. Verified: an armed overlay auto-clears when no further `dragover` arrives, and genuine drops still attach. `cmd/sapaloq-widget/frontend/src/main.ts`.
- **Taught the model how attachments work (so it stops hunting on disk).** A user attaches `skills-lock.json` and asks "where is this stored?"; the Ask orchestrator ran `workspace_list_dir .`, didn't find it, then guessed random dirs and asked the user back — because no system prompt ever explained that attachments are inlined into the message (`<!--sapaloq-attachment:…-->\n--- file: <name> (<mime>) ---\n<content>\n--- end file: <name> ---`, images as `![name](data:…)`), not saved to disk. Added an attachment-guidance paragraph to `ask.md` (recognise the inline block; do NOT search the workspace / run system_exec to find it; if asked where it's "stored", explain it's inline context and offer to write it to a path) plus a shorter note to `agent.md`/`planner.md` for tasks that carry inlined attachments. Pure prompt change; embedded defaults auto-upgrade unmodified on-disk copies via the prompts manifest. `internal/prompts/defaults/{ask,agent,planner}.md`.
- **Stopped tool-result turns from leaking into the chat.** The backend now persists tool results as `role:"tool"` turns (`[Tool results]\n…`) purely so they count toward context usage — they're internal/context-only. But the frontend `renderTurn()` only special-cased `thinking`/`user`/`error` and dumped everything else (incl. `tool`) into a `message--assistant` bubble, so after a stop→rerun the history reload surfaced a raw `[Tool results]\n.blackbox/\n.git/…` bubble. Added an early `if (turn.role === 'tool') return;` guard and made the final branch an explicit `else if (turn.role === 'assistant')` (so any future unknown role is skipped rather than leaked). `cmd/sapaloq-widget/frontend/src/main.ts`.
- **Auto-growing composer textarea + expand toggle (ChatGPT-style).** The textarea had no JS autosize — it was clamped at `max-height:110px` so multi-line input scrolled internally and only the last ~2 lines showed. Added `autosizeCompose()` (sets `height:auto` then `height:scrollHeight`, clamped by CSS `--compose-max: clamp(96px,38vh,300px)`) wired to the `input` event plus `editText()`/slash-apply/submit-reset. Once content overflows the cap, `.is-tall` reveals a new `#compose-expand` button (diagonal in/out arrow SVG) at the pill's top-right; clicking it toggles `.compose-wrap.expanded` which raises the cap to `--compose-max-tall: 72vh` (composer takes most of the popup, message list shrinks) and swaps the icon to a collapse glyph. `resetComposeSize()` clears the expanded/tall state + height after send. In-window expand (not OS fullscreen) since the widget is a small floating window. Verified visually: textarea grows line-by-line, the toggle appears at threshold, and expand/collapse works. `cmd/sapaloq-widget/frontend/src/main.ts`, `cmd/sapaloq-widget/frontend/src/style.css`.

## Implemented this session (2026-06-21) — `read_image` (local image → vision)

- **New `read_image` tool in every mode.** Until now the model could only see images via widget attachments; it could not open a local image file. `read_image {"path":"..."}` reads a host image (png/jpeg/gif/webp), base64-encodes it, and returns inline `![name](data:<mime>;base64,…)` markdown. Because the orchestrator's `extractImages` scans messages for exactly that markdown and attaches the decoded picture to `bridge.Request.Images`, the result becomes **real vision input on the next turn** (the same channel widget attachments use) — not base64 text. Added to `readOnlyAssessmentTools` (so it propagates to Ask/plan/agent/scribe + `knownToolSet`) with a `reg()` schema; dispatched via `runSharedTool`. Ask parity: `runConversation` now re-extracts images from each appended tool-results message (replacing `images = nil`) and re-applies the `visionAllowed` guard; Plan/Agent already re-extract every turn. Mime resolved by extension map (+ `http.DetectContentType` fallback), 10 MiB cap, bypasses the text-oriented `looksBinary` guard. `core/orchestrator/{tools_system.go,tools.go,tools_dispatch.go,conversation.go,tools_image_test.go}`, `internal/prompts/defaults/{ask,planner,agent}.md`, `docs/{BLUEPRINT.md,STATUS.md}`.

## Implemented this session (2026-06-21) — drop redundant `system_read_file`

- **Removed `system_read_file`, keeping only `system_exec`.** The read tool was redundant: `system_exec` (full host access) already covers any file read via `cat`/`sed -n`/`head`/`tail`/`rg`, so two host tools just widened the tool surface. `system_exec`'s schema/prompt now note it also reads files, plus a **cross-platform caveat** (runs via `bash -lc`/Unix syntax — mind macOS BSD vs GNU flags, and Windows hosts that may lack bash). Removed `toolSystemReadFile` + its byte-cap consts (`looksBinary` stays — it's shared by `workspace_*`), the `system_read_file` schema + dispatch case + allowlist entries, and updated the default prompts. `core/orchestrator/{tools_system.go,tools_system_test.go,tools.go,tools_dispatch.go}`, `internal/prompts/defaults/{ask,planner,agent}.md`, `config/config.example.json`, `docs/{BLUEPRINT.md,STATUS.md}`.

## Implemented this session (2026-06-21) — docs sync + AGENTS.md

- **Synced outdated design docs** to match the code shipped this session: `docs/RUNTIME.md` (vault is now dual-purpose `undeclared`+`executed` audit, added a Rotation & retention subsection + `config.vault.*`; config schema migration marked implemented), `docs/BLUEPRINT.md` (added a `vault` config-domain row + an unrestricted host-tools `system_read_file`/`system_exec` defaults row; noted replaceable on-disk prompts), `docs/PROMPT-BUILDER-SOP.md` (new "Replaceable prompts (on-disk override)" section + `config.prompts.*`). Bumped each doc's `Last updated`.
- **Added `AGENTS.md`** at the repo root: build/test gate (`go build/vet/test ./...` + frontend `npm run build`), conventions, a project map, and a **"Keep docs in sync (REQUIRED)"** table mapping each code area → the doc(s) to update — so future agents update the relevant docs (and STATUS.md) alongside behavior changes.

## Implemented this session (2026-06-21) — vault rotation + widget emoji/render fixes

- **Vault audit-log rotation/retention:** the tool-call vault (`vault/tool-calls.jsonl`) was append-only and unbounded — and now logs both `reason="undeclared"` (provider anomalies, e.g. cursor's server-hardcoded tools) *and* `reason="executed"` (full orchestrator tool-audit), so it grows during normal use. Added size-based numbered rotation directly in `vault.Writer.Append` (best-effort: a rotation error falls back to plain append so an audit write is never lost): when the primary would exceed `MaxBytes` it cascades `tool-calls.jsonl` → `.1` → `.2` …, dropping the oldest beyond `KeepFiles`. New `Options{MaxBytes,KeepFiles}` + `NewWithOptions` with defaults 5 MiB / keep 3 (`New` still works = defaults, so the cursor-bridge writer inherits rotation). New `ReadRecent(path,limit)` reads across rotated siblings so stats/CLI still see recent history after a rotation. New `config.vault.{maxLogBytes,keepRotatedFiles}` (absent block = defaults), wired in `chat.go`. `internal/vault/{vault.go,read.go,rotate_test.go}`, `config/load.go`, `core/orchestrator/chat.go`, `config/config.example.json`.

- **Widget emoji rendering (bundled color font):** the host had no emoji font (`fc-list` empty), so model-emitted emoji (✅/❌ in tables, etc.) rendered as blank/tofu even after we stopped stripping them. Bundled a self-hosted color emoji font the Firefox/WhatsApp way: `TwemojiColor.woff2` (Twemoji SVGinOT→woff2, 3.36 MB, MIT + CC-BY-4.0) in `frontend/src/assets/fonts`, a `@font-face` (`"Twemoji SapaLOQ"`) with a `unicode-range` scoped to emoji/symbol ranges, prepended to the base/chat/monospace `font-family` stacks. Vite fingerprints + emits it into `dist/assets`, embedded into the binary via the existing `//go:embed all:frontend/dist`. `cmd/sapaloq-widget/frontend/src/style.css`, `assets/fonts/{TwemojiColor.woff2,TWEMOJI-LICENSE.md}`.

- **Widget render bug fixes (`main.ts`):** (1) `sanitizeDisplayText` no longer strips emoji/pictographs (it was blanking ✅/❌ table cells) — only trailing whitespace is trimmed; (2) empty/whitespace-only `response_delta`s no longer spawn a blank assistant bubble, and `flushStream` drops an assistant bubble with no visible rendered text (new `hasVisibleText` helper) so 👍/👎 feedback controls never attach to an empty response. Both fixes also cover the batch/fallback `renderEvents` path.

## Implemented this session (2026-06-21) — new-proposal.md

- **Replaceable per-mode system prompts:** new `internal/prompts` package — the Ask/planner/agent/scribe system prompts now ship as embedded Markdown defaults that are materialized to `config.prompts.dir` (default `~/.config/sapaloq/prompts`) alongside a `prompts.manifest.json` sha256 manifest. "Updateable if non-modified": on each boot an on-disk file whose hash still matches the recorded shipped hash is transparently upgraded when the embedded default changes, while any user edit is always preserved (never clobbered). Resolution goes through `Orchestrator.systemPrompt(role)` (on-disk → embedded default), replacing the previously hardcoded inline strings in `session.go` (Ask) and `subagent.go` (`buildSubAgentMessages`). New `PromptsConfig{enabled,dir}` (absent block treated as enabled, like skills). `internal/prompts/{prompts.go,prompts_test.go,defaults/*.md}`, `config/load.go`, `core/orchestrator/{chat.go,session.go,subagent.go}`, `config/config.example.json`.

- **Unrestricted host tools (no workspace sandbox):** by explicit user design SapaLOQ is no longer "kebiri" to the workspace root. New `system_read_file` (read ANY path — e.g. `/etc/hosts`; binary-guarded, byte-capped, supports offset/limit line ranges) and `system_exec` (run ANY shell command anywhere with full access, optional `cwd`, timeout-guarded). Both are offered in **every** mode (Ask, planner, agent) via the shared-tool dispatcher so simple host tasks don't require spawning a plan/agent. The boundary-rooted `workspace_*` tools are unchanged for scoped, safe project edits. `core/orchestrator/tools_system.go` (+ `tools_system_test.go`), `tools.go` (`unrestrictedSystemTools` added to askTools/planTools/agentTools/knownToolSet + JSON schemas), `tools_dispatch.go`, `tools_workspace.go` (`Cwd` arg), `config/config.example.json` (orchestrator/planner/task-runner allowlists).

- **Config schema migration / versioning:** new `internal/config/migrate.go` — `CurrentSchemaVersion` (now `1.1.0`) + a tolerant semver comparator and an ordered, additive/idempotent `migrationSteps` chain that operates on the decoded raw map (old JSON formats are always preserved). `Load` now decodes to a raw map, runs `migrateRaw` (lower → upgrade & persist atomically via `SaveRaw`, equal → no-op, higher → load as-is for forward-compat), then binds the struct; mandatory fields that come out empty are caught by the existing `LLMBridge.Validate`. `config/{migrate.go,migrate_test.go,load.go}`, `config.example.json` (`schemaVersion` → 1.1.0).

## Implemented earlier this session (2026-06-21)

- **Nodes (roadmap #8 / #7 in this list):** new `nodes` SQLite table (+ `idx_nodes_role`) in the inline migrate, with `internal/store/chat/nodes.go` CRUD (`UpsertNode`/`GetNode`/`ListNodes`/`NodesForRole`/`SetNodeEnabled`/`TouchNode`; created_at preserved across upserts; capabilities JSON; tokens never stored). Orchestrator bootstraps an idempotent `local-default` node in `New` (+ writes a comm-spec template) so spawns always have a routable in-proc target. New `pickNode` (hint → highest-priority enabled role/`*` node → local-default; nil-store safe) and spawns now record the chosen `Node` on `taskRecord` — with only local-default configured, behavior is unchanged (regression-tested). New `internal/node` package: a minimal `Transport` interface + bounded `SpawnEnvelope` (with `EnforceRemoteInvariants` stripping memory-bus keys + forcing `NoMemoryBus`), a `FakeTransport` for network-free unit tests, and a real `WSTransport` (gorilla/websocket, Bearer auth header from ENV, connect-probe → fallback). New `NodesConfig` (`allowRemoteRoles`/`requireTlsRemote`/`allowSharedMemoryRemote`/`fallbackToLocalOnRemoteFail`). Deferred: wiring a remote envelope back into the sub-agent loop + `/settings` node CRUD. `store/chat/{store.go,nodes.go,nodes_test.go}`, `core/orchestrator/{nodes.go,nodes_test.go,tasks.go,chat.go}`, `internal/node/{transport,fake,ws,node_test}.go`, `config/load.go`.

- **Platform / desktop driver (roadmap #7 / #6 in this list):** new `internal/platform` package — an OS-agnostic `Desktop` interface (`NotifySend`/`NotifyWatch`/`DNDEnabled`/`Info`/`Capabilities`), a `Capability` set + `Has` helper, and a pure `ResolveAdapterID`/`Detect` (env-driven: `XDG_CURRENT_DESKTOP`/`DESKTOP_SESSION`/GOOS, config `platform.adapter`/`detectOrder`/`allowFallback`, factory registry + headless fallback). Always-available `internal/platform/headless` adapter (no caps, closed watch channel) keeps CI/non-Linux green. `internal/platform/freedesktop` adapter implements the `org.freedesktop.Notifications` D-Bus spec for `NotifySend` (urgency hints) + best-effort eavesdrop `NotifyWatch`, constructed behind a `dbus.SessionBus()` probe (falls back to headless when no bus); the `gnome` adapter reuses it. New `desktop_notify` + `desktop_dnd_status` tools (schemas + Ask + sub-agent dispatch) gated by the active adapter's capabilities. Core wires a notify-watch→bus bridge publishing `sapaloq.v1.platform.notification`. Uses the already-present `godbus/dbus/v5` (no new dependency). `internal/platform/{desktop,capability,detect}.go` + tests, `internal/platform/headless/*`, `internal/platform/freedesktop/notify.go`, `config/load.go` (`PlatformConfig`), `core/orchestrator/{chat.go,tools.go,tools_desktop.go,tasks.go,subagent.go,tools_workspace.go}` + `tools_desktop_test.go`, `cmd/sapaloq-core/main.go`, `config/config.example.json`.

- **Skills system (roadmap #6 / #8 in this list):** new `internal/skills` package — `Load` scans `~/.config/sapaloq/skills/*.md` (minimal YAML frontmatter: `id`, `triggers`, `priority`, `maxBodyLines` + Markdown body; malformed/no-id files skipped, missing dir inert), `Match` does case-insensitive trigger-substring matching, `SortByRelevance` orders by priority + caps, `Render` emits a bounded `### <id>` block. New `SkillsConfig` (`enabled`/`dir`/`maxLoadPerTurn`/`maxBodyLines`, absent-block treated as enabled like feedback). Orchestrator loads skills in `New`, best-effort indexes bodies into `facts` (kind=`skill`) for a secondary FTS signal, and injects a config-bounded `skillsBlock` into the Ask prompt right after the negative-guidance block (`session.go`). Seed skill at `examples/skills/sapaloq-scribe.md`. Scope: scan + match + inject only — learning-agent skill *writing* remains deferred. `internal/skills/{skills.go,skills_test.go}`, `config/load.go`, `core/orchestrator/{chat.go,session.go,skills_test.go}`, `config/config.example.json`.

## Implemented this session (2026-06-20)

- **Markdown via library:** replaced the hand-rolled parser in the widget with `marked` + `DOMPurify` (GFM tables/headings now render). `cmd/sapaloq-widget/frontend/src/main.ts`, `style.css`.
- **Wait countdown UX:** `waiting` status now carries `wait_seconds`; the widget shows a live countdown (`waiting · 10s, 9s, …`). `internal/bridge/events.go`, `tasks.go`, `main.ts`.
- **Atomic task writes:** `writeFileAtomic` (temp + rename) fixes the `status.json` read/write race that made `sapaloq_wait` fail with "unexpected end of JSON input". `tasks.go`. Defensive retry in `readTask`.
- **No fake plan.md:** planner no longer auto-writes `plan.md` from free-form text; only `sapaloq_write_plan_markdown` does. The current explicit `plan_task_id` validator requires a real `plan.md`. `tasks.go`.
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
5. **Event bus completion:** topic-pattern matcher ✅, jsonl WAL ✅, replay-on-boot ✅, completion trigger (`EventTaskUpdate` → bus → widget `watch`) ✅ (2026-06-21). Remaining: a socket-level bus *publish* op for external producers.
6. **Platform/Driver:** GNOME/D-Bus notifications, `desktop_*` tools, `os.json` detect/cache.
7. **Nodes:** remote sub-agent registry + transport.
8. **Skills:** scan `~/.config/sapaloq/skills/`, trigger matching, bounded injection.
9. **Scribe storage mapping:** mode-aware note writing to `storage.paths`.
