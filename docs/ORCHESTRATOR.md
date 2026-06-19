# SapaLOQ — Orchestrator & Config-by-Agent

> Companion doc untuk [VISION.md](./VISION.md). Anchor untuk arsitektur runtime.
> Last updated: 2026-06-19 (execution modes: Ask → Plan → Agent)

---

## Ringkasan

- **Widget agent = orchestrator saja** — tidak melakukan pekerjaan berat; **delegasi** ke sub-agent.
- **Config = `config.json`** — **tidak ada settings UI**; user ubah lewat chat (`/settings ...`) → sub-agent `settings` edit file.
- **Storage/apps mapping** — indexed paths + intents; scribe sub-agent nulis ke file yang benar by mode/boundary.
- **Mode-aware** — personal / hobby / work; memory namespace terpisah.
- **Anti context poisoning** — task stack; tidak loncat task tanpa park/done/switch eksplisit.
- **Anti-blocker** — orchestrator never awaits sub-agent; sub-agent agresif & parallel where safe.
- **Progress streaming** — orchestrator **watch live** sub-agent: thinking, response, toolcall, todo, status.
- **Completion triggers** — in-proc event bus wake (ms) + jsonl WAL; heartbeat hanya watchdog.
- **Event watchers** — GNOME notification + custom (reminder, email, …) → orchestrator react.
- **Context ingress** — intent-router + SQLite prefetch + dynamic prompt before spawn ([CONTEXT-SOP.md](./CONTEXT-SOP.md)).
- **Role system-prompt** — setiap spawn sub-agent dapat `systemPrompt` per role ([PROMPT-BUILDER-SOP.md](./PROMPT-BUILDER-SOP.md)).
- **Post-task learning** — learning-agent builds prompts/skills; optional web research for best practice.
- **Clarification loop** — sub-agent tanya orchestrator saat keputusan unclear; orchestrator jawab sendiri atau forward ke user.
- **Sub-agent control** — orchestrator dapat **delay start**, **pause**, **resume**, **stop**, **delete** sub-agent dari task.
- **Sub-agent nodes** — sub-agent as **node** (local or remote Docker/VPS/EC2/SSH); registry SQLite + comm spec ([NODES.md](./NODES.md)).

---

## Config-by-agent (bukan UI)

### Contoh interaksi

```
User:  /settings matiin read notification kamu dong
Orchestrator: spawn sub-agent:settings
Settings agent: read config.json
              → set notifications.read = false
              → write config.json (updatedBy: sub-agent:settings)
Orchestrator: "Notifikasi read-aloud sudah off."
```

### Slash commands (orchestrator routes)

MVP command surface hanya `/settings ...`. Semua intent lain tetap natural-language chat dan dirutekan ke cursor-bridge/orchestrator tanpa user-facing slash command tambahan.

| Command | Sub-agent | Mutasi config |
|---------|-----------|---------------|
| `/settings ...` | `settings` | paths in `commands.settings.allowedPaths` |

### File

| Path | Role |
|------|------|
| `~/.config/sapaloq/config.json` | Live config (agent-editable) |
| `config.schema.json` (repo) | Validation contract |
| `config.example.json` (repo) | Bootstrap template |

Orchestrator **read-only** config kecuali spawn `settings`. Sub-agent lain **tidak** edit config unless role allows.

---

## Orchestrator vs sub-agent

```mermaid
flowchart TB
  USER[User chat / voice]
  ORCH[Orchestrator — widget]
  CS[context-scaler]
  BG[boundary-guard]
  TR[task-runner]
  SC[scribe]
  MJ[memory-janitor]
  ST[settings]
  MEM[(memory bus — local only)]
  CFG[(config.json)]

  USER --> ORCH
  ORCH --> BG
  BG --> CS
  CS --> TR
  CS --> SC
  CS --> ST
  ORCH -.spawn async.-> MJ
  TR --> MEM
  SC --> MEM
  MJ --> MEM
  ST --> CFG
  BG --> CFG
  CS --> MEM
```

### Orchestrator responsibilities

- Parse user intent; classify mode (personal/hobby/work/auto).
- **Run context ingress** — intent-router → index prefetch → dynamic prompt (see [CONTEXT-SOP.md](./CONTEXT-SOP.md)).
- Maintain **task stack** (active, parked, done).
- Spawn sub-agents; pass **`systemPrompt` (role-specific)** + **context packet** (minimal, scaled).
- Update widget ring state (idle / thinking / delegating / N sub-agents active).
- **Subscribe progress stream** dari setiap sub-agent aktif — mirror ke widget + optional user digest.
- **React to event bus** — GNOME notification, custom reminder, sub-agent completion.
- **Never block** on sub-agent completion — fire-and-forget + callback/event (tapi **tetap watching** stream).
- **Control sub-agent lifecycle** — delay start, pause, resume, stop, delete (see [Sub-agent lifecycle control](#sub-agent-lifecycle-control)).
- **Enforce concurrency cap** — `maxConcurrentSubAgents` (see below).

### At `maxConcurrentSubAgents` capacity

When `orchestrator.maxConcurrentSubAgents` reached (default 4):

| Situation | Behavior |
|-----------|----------|
| New spawn request (user task) | **Queue** as `lifecycle: scheduled` on active task with `spawnAt: now` + reason `capacity_wait` — orchestrator notifies: "Menunggu slot sub-agent." |
| User explicit `/task` spawn | Same queue; widget shows N waiting |
| `scribe` / `settings` one-shot | **Allowed** if role is fast-path (`maxTurns ≤ 8` and no long-running tools) — does not count toward cap *optional* via `orchestrator.spawnRouting.fastPathExemptRoles: ["scribe", "settings"]` |
| Research sub-agent | Counts toward cap; max **1** concurrent `research` (hard sub-limit) |

Does **not** auto-trigger `blockNewTaskUntilParkOrDone` — that rule is for **task stack**, not sub-agent slots.

Per-role limits (defaults): `task-runner: 2`, `planner: 1`, `research: 1`, others: share remaining slots.

### Orchestrator MUST NOT

- Run grep, write large files, embed memory, long LLM chains.
- Hold full conversation history in its prompt (context-scaler feeds slices).

---

## Execution modes (Ask → Plan → Agent)

> Analog Cursor IDE: **Ask**, **Plan**, **Agent** — di SapaLOQ diimplementasikan sebagai **orchestrator + sub-agent roles**, bukan clone wire `UNIFIED_MODE` api2.
> Cursor CLI: Ask = no edits/commands; Plan = read-only planning before implementation; Agent = full tool access.

### Non-Cursor canonical tool surface

Provider-bridge backends (OpenAI/Claude/Kimi/OpenRouter/TokenRouter/etc.) do not get Cursor's native IDE tool set. SapaLOQ exposes **role-scoped canonical tools** instead. Tool availability is resolved by role/mode before each LLM request; never expose Agent tools to Ask.

| SapaLOQ mode | Role | Declared tools profile | Policy |
|--------------|------|------------------------|--------|
| **Ask** | `orchestrator` | `sapaloq_spawn_*`, task status, brief memory/context, clarification, light `desktop_*` | Delegate and observe; no file mutation, no shell |
| **Plan** | `planner` | read-only workspace/search/errors + `sapaloq_write_plan_markdown` | Markdown assessment only; no execution side effects |
| **Agent** | `task-runner` | workspace write tools, terminal/build/test, progress/complete, clarification | Execute approved/direct task under boundary guard |

Recommended MVP profile names:

```text
ask:
  sapaloq_spawn_plan
  sapaloq_spawn_agent
  sapaloq_get_task_status
  sapaloq_read_memory_brief
  sapaloq_prefetch_context
  sapaloq_clarify_user
  desktop_notify
  desktop_screenshot
  desktop_list_windows
  desktop_focus_window

plan:
  sapaloq_read_context_packet
  sapaloq_read_memory_brief
  workspace_search
  workspace_read_file
  workspace_list_errors
  sapaloq_write_plan_markdown
  sapaloq_request_clarification

agent:
  sapaloq_read_context_packet
  sapaloq_read_plan_markdown
  sapaloq_update_task_progress
  sapaloq_complete_task
  sapaloq_fail_task
  workspace_search
  workspace_read_file
  workspace_edit_file
  workspace_create_file
  terminal_run
  tests_run
  build_run
  sapaloq_request_clarification
```

Provider `declaredTools` should be generated from this role profile. Static per-provider `declaredTools` remains a compatibility fallback only.

### Mapping SapaLOQ

| Cursor mode | SapaLOQ | Tool policy | Job |
|-------------|---------|-------------|-----|
| **Ask** | **Orchestrator** (widget) | Extended **companion** tools only (`spawn`, `desktop_*`, read progress/memory, clarify) | Route intent, **score spawn path**, delegate — tidak eksekusi task berat |
| **Plan** | Sub-agent **`planner`** | **Read-only** — explore, draft plan, no write/exec side effects | Merancang task: steps, files, risks, acceptance criteria |
| **Agent** | Sub-agent **`task-runner`** | **Full access (default, non-restricted)** | Eksekusi plan / explicit user steps sampai done atau maxTurns |

**Prinsip:** Ask + Plan sudah merancang pekerjaan → **Agent tidak perlu restriction tambahan** beyond global boundary (mode personal/work) dan `maxTurns`. Restriction di front-load ke orchestrator + planner, bukan di throttle agent.

### Spawn routing (orchestrator / intent-router)

Orchestrator (Ask) memutuskan **spawn planner dulu** atau **direct agent**:

```mermaid
flowchart TD
  USER[User prompt]
  ORCH[Orchestrator — Ask]
  IR[intent-router + score]
  SCRIBE[scribe / desktop only]
  PLAN[planner — Plan]
  REV[Review plan — user or auto]
  AGENT[task-runner — Agent full]

  USER --> ORCH
  ORCH --> IR
  IR -->|no execution needed| SCRIBE
  IR -->|P plan high| PLAN
  IR -->|P agent high, plan low| AGENT
  PLAN --> REV
  REV --> AGENT
```

| Signal | Naikkan P(plan) | Naikkan P(direct agent) |
|--------|-----------------|-------------------------|
| Multi-step / multi-file | ✓ | |
| Ambiguous goal | ✓ | |
| Destructive or cross-boundary risk | ✓ | |
| User gave explicit step list | | ✓ |
| Single-shot / scribe / notify | | ✓ (often skip agent entirely) |
| High intent-router confidence + low risk | | ✓ |

Output intent-router (per task):

```json
{
  "intent": "execute_task",
  "confidence": 0.82,
  "spawnPath": "plan_then_agent",
  "scores": { "plan": 0.71, "directAgent": 0.38, "scribeOnly": 0.05 },
  "reason": "multi_file_refactor_ambiguous_scope"
}
```

| `spawnPath` | Behavior |
|-------------|----------|
| `none` | Orchestrator jawab / scribe / desktop only |
| `direct_agent` | Spawn `task-runner` dengan context packet dari Ask |
| `plan_then_agent` | Spawn `planner` → Markdown plan artifact → spawn `task-runner` dengan `planPath` + checklist |

Config: `orchestrator.spawnRouting` (see `config.schema.json`).

### Plan artifact → Agent packet

Planner menulis Markdown artifact ke `memory/tasks/<taskId>/plan.md` (or scribe path by policy). Ini sengaja mengikuti Cursor/Copilot Plan mode: plan adalah dokumen Markdown yang bisa dibaca user, direview, dan dicentang saat Agent eksekusi.

```markdown
---
planId: plan-001
taskId: task-001
mode: work
status: draft
createdBy: planner
---

# Plan: Refactor config schema validation

## Goal
Refactor config schema validation without touching Cursor worker memory.

## Constraints
- mode=work
- no touch `~/.cursor`
- preserve existing config bootstrap behavior

## Steps
- [ ] Read `config.schema.json` and current config loader.
- [ ] Add `spawnRouting` validation fields.
- [ ] Update `config.example.json`.
- [ ] Run config/load tests.

## Risks
- Schema drift between docs and bootstrap config.

## Acceptance
- [ ] Config validates.
- [ ] Example config bootstraps.
```

`task-runner` spawn payload:

```json
{
  "role": "task-runner",
  "executionMode": "agent",
  "toolPolicy": "full",
  "contextPacket": {
    "taskId": "task-001",
    "planId": "plan-001",
    "planPath": "~/.config/sapaloq/memory/tasks/task-001/plan.md",
    "userSnippet": "..."
  }
}
```

Direct agent (skip planner) — same `toolPolicy: full`, context packet dari Ask + context-scaler saja.

### Plan review

| Policy | When |
|--------|------|
| `autoApprovePlan: false` (default) | Orchestrator surfaces plan summary → user confirm → spawn agent |
| `autoApprovePlan: true` | Low-risk patterns only (configurable allowlist) |

Planner **never** spawns agent — orchestrator only.

---

## Sub-agent roles

| Role | Trigger | Job |
|------|---------|-----|
| **orchestrator** | every user turn | **Ask mode** — route, spawn score, delegate, watch progress |
| **settings** | `/settings ...` | Patch `config.json` |
| **scribe** | "catat ini", notes | Append to `storage.paths` by mode/intent |
| **planner** | spawnPath `plan_then_agent` | **Plan mode** — read-only; produce Markdown `plan.md` artifact |
| **task-runner** | spawnPath `direct_agent` or post-plan | **Agent mode** — **full tool access**; execute designed task |
| **intent-router** | every user prompt | Classify intent; **spawn path scores**; prefetch from SQLite |
| **context-scaler** | every delegation | Build minimal context packet; enforce anti-deep-check |
| **boundary-guard** | before delegation | Reject cross-mode leaks, wrong path |
| **memory-janitor** | auto / idle / schedule | Dedupe, compact, index sync |
| **learning-agent** | post `subagent.completed` | Prompt overlay + skills builder; queue research |
| **research** | learning-agent / novel task | Web best practice → facts + skill draft |
| **event-watcher** | config `events.watchers` | Poll/subscribe GNOME + custom sources → emit to bus |

Sub-agents on **same machine** may share **memory bus** (SQLite + namespaces). **Outer-machine nodes do not** — context packet in, progress/result out only ([NODES.md](./NODES.md#memory-policy-local-vs-remote)).

Sub-agents **publish** ke **progress bus** (append-only stream per subAgentId).

---

## Storage & apps mapping

### `storage.paths[]`

Indexed write targets:

```json
{
  "id": "personal-notes",
  "path": "~/Documents/sapaloq/personal/notes.md",
  "mode": "personal",
  "kind": "notes"
}
```

### `storage.intents`

Maps natural phrases → path id:

```json
{ "catat": "personal-notes", "catat ini": "personal-notes" }
```

### Delegation example

```
User (mode=personal): "tolong catat ini: beli susu besok"
Orchestrator:
  1. boundary-guard: ok, personal → personal-notes
  2. context-scaler: { mode, intent, snippet, taskId }
  3. spawn scribe → append ~/Documents/sapaloq/personal/notes.md
  4. reply immediately: "Dicatat ke personal notes."
```

### `apps.entries[]`

For focus/launch/automation — indexed by `id`, tagged with `mode`. Boundary-guard checks before `gnome_focus_window` ke app work saat mode personal.

---

## Mode & boundary awareness

| Mode | storageRoot | memoryNamespace |
|------|-------------|-----------------|
| personal | `~/Documents/sapaloq/personal` | `personal` |
| hobby | `~/Documents/sapaloq/hobby` | `hobby` |
| work | `~/Documents/sapaloq/work` | `work` |

Rules:

- Default `orchestrator.defaultMode: auto` — infer from active app, time, user hint (see **Mode auto-detect** below).
- `allowCrossModeRead: false` — scribe tidak baca work notes saat personal unless user confirms.
- Memory janitor tags facts with namespace; no merge across modes without explicit link.

### Mode auto-detect (`defaultMode: auto`)

Heuristic order (no LLM required for MVP):

1. User explicit `/mode X` or message prefix → use X
2. `apps.entries` — focused window desktopId → map `mode` tag
3. Time window rules in `modes.autoSchedule` (optional config)
4. Fallback: last user-selected mode from `hot_cache`

If confidence low → orchestrator asks once via widget quick reply.

---

## Task model & anti context poisoning

### Task stack (orchestrator state)

```json
{
  "activeTaskId": "task-001",
  "stack": [
    {
      "id": "task-001",
      "status": "running",
      "mode": "work",
      "summary": "Rangkum email klien",
      "startedAt": "ISO8601",
      "subAgents": [
        {
          "id": "sub-abc",
          "role": "task-runner",
          "lifecycle": "in_progress",
          "spawnAt": null,
          "attachedAt": "ISO8601",
          "pausedAt": null,
          "pauseReason": null
        },
        {
          "id": "sub-def",
          "role": "scribe",
          "lifecycle": "scheduled",
          "spawnAt": "2026-06-19T15:00:00Z",
          "attachedAt": "ISO8601",
          "pausedAt": null,
          "pauseReason": null
        }
      ]
    }
  ],
  "parked": []
}
```

Legacy field `subAgentIds[]` may be derived from `subAgents[].id` where `lifecycle` ∉ `deleted`.

---

## Sub-agent lifecycle control

Orchestrator adalah **control plane** untuk setiap sub-agent yang terikat task. User atau orchestrator (policy/auto) dapat mengirim perintah lifecycle.

### Lifecycle states (`subAgents[].lifecycle`)

| State | Meaning | Transitions in |
|-------|---------|----------------|
| `scheduled` | Delay start — belum spawn | delay_start |
| `pending` | Spawned, belum turn pertama | spawn |
| `in_progress` | Aktif | resume, spawn langsung |
| `paused` | Orchestrator pause — worker frozen | pause |
| `awaiting_clarification` | Sub-agent pause — tunggu jawaban | ask_orchestrator |
| `stopping` | Stop in flight — cooperative shutdown | stop |
| `stopped` | Terminal — cancelled by control | stop complete |
| `done` | Terminal — success | natural complete |
| `failed` | Terminal — error | error |
| `deleted` | Detached from task — record retained/ purged per policy | delete |

```mermaid
stateDiagram-v2
  [*] --> scheduled: delay_start
  scheduled --> pending: spawnAt reached
  [*] --> pending: spawn now
  pending --> in_progress: first turn
  in_progress --> paused: pause
  in_progress --> awaiting_clarification: ask_orchestrator
  paused --> in_progress: resume
  awaiting_clarification --> in_progress: clarification_received / resume
  in_progress --> stopping: stop
  stopping --> stopped: ack
  in_progress --> done: complete
  in_progress --> failed: error
  stopped --> deleted: delete
  done --> deleted: delete optional
  failed --> deleted: delete optional
```

### Control actions (orchestrator → sub-agent)

Control bus per sub-agent:

```text
~/.config/sapaloq/memory/control/<subAgentId>.json
```

Orchestrator writes command; bus publish `sapaloq.v1.orchestrator.control.{subAgentId}`. Legacy control file only if `events.bus.enabled: false`.

```json
{
  "v": 1,
  "kind": "orchestrator.subagent.control",
  "action": "pause",
  "subAgentId": "sub-abc",
  "taskId": "task-001",
  "reason": "user requested",
  "issuedBy": "orchestrator",
  "ts": "ISO8601"
}
```

| Action | Effect | Task binding |
|--------|--------|--------------|
| **delay_start** | Register `spawnAt`; lifecycle `scheduled` — no worker yet | Stays on task |
| **pause** | SIGSTOP-equivalent; no new tool/LLM turns | Stays on task |
| **resume** | Continue from checkpoint; `paused` or `awaiting_clarification`* | Stays on task |
| **stop** | Cooperative cancel → `stopping` → `stopped` | Stays until deleted |
| **delete** | Detach from `task.subAgents`; kill if running; cleanup per policy | **Removed** from task |

\* `resume` after clarification normally via `clarification_received`; orchestrator `resume` is override / user force continue.

### delay_start

At delegation time or mid-task:

```json
{
  "action": "delay_start",
  "role": "scribe",
  "taskId": "task-001",
  "spawnAt": "2026-06-19T15:00:00Z",
  "contextPacket": { "...": "pre-built, stored at tasks/context-packets/" },
  "systemPromptRef": "tasks/context-packets/task-001-sub-def-prompt.json"
}
```

- context-scaler **pre-assembles** prompt + packet; stored until spawn
- sapaloq-core timer fires at `spawnAt` → spawn worker → `scheduled` → `pending` → `in_progress`
- User: *"catat jam 3 nanti"* → orchestrator schedules scribe

Config: `subAgentControl.maxScheduledPerTask`, `subAgentControl.maxDelaySec`.

### pause

```
User: /task pause sub-abc
Orchestrator:
  → validate sub-abc on activeTask
  → write control pause
  → lifecycle paused, progress event status paused
  → widget: sub-agent dimmed on ring
```

Sub-agent **must** ack within `subAgentControl.ackTimeoutSec` — finish in-flight tool if safe, then freeze.

Use cases: user mau intervensi, resource contention, DND, clarification overload.

### resume

```
User: /task resume sub-abc
Orchestrator:
  → write control resume
  → lifecycle in_progress
  → inject last checkpoint (context packet + seq) — no full transcript replay
```

Does not replace clarification flow — if question still open, forward answer first or orchestrator resolves then resume.

**Force resume** (orchestrator override while `awaiting_clarification`):

1. Orchestrator injects synthetic `clarification_received` **before** `control resume`:
   ```json
   {
     "resolvedBy": "orchestrator_force",
     "answer": null,
     "policy": "orchestrator_best_guess"
   }
   ```
2. Sub-agent **must** apply `defaultOnTimeout` from `orchestrator.clarification` (`park` | `fail` | `orchestrator_best_guess`) when `answer` is null.
3. Sub-agent **must not** re-emit same `clarification_request` without new evidence — log `force_resume` to progress stream.
4. User-visible: widget shows "Dilanjutkan tanpa jawaban — orchestrator memutuskan."

### stop

Cooperative cancel — distinct from **failed**:

```
User: /task stop sub-abc
  → control stop
  → sub-agent emit status cancelled
  → events.jsonl subagent.completed status stopped
  → task may continue with other sub-agents
```

Optional `stopReason` logged for learning-agent (do_not_repeat if wrong approach).

### delete

Remove sub-agent **from task** (detach):

```
User: /task delete sub-abc
  → if running: stop first (or force kill after grace)
  → lifecycle deleted
  → remove from task.subAgents
  → progress jsonl: archive or delete per subAgentControl.progressRetention
  → control channel file removed
```

Task without sub-agents: stays on stack until user parks/done — delete does **not** auto-close task.

### Widget chat history (UI-only)

Orchestrator **prompt** excludes full transcript; widget panel still shows scrollable history.

| Store | Path | Content |
|-------|------|---------|
| Chat transcript | `~/.config/sapaloq/widget/chat.jsonl` | User + orchestrator visible replies only |
| Sub-agent detail | `memory/progress/<subAgentId>.jsonl` | thinking, tools — via `/tasks` drill-down |

Widget appends via IPC per turn — **not** injected into orchestrator context. Tail cap: `widget.chatHistoryMaxLines` (default 500).

### Natural language (orchestrator routes)

| User says | Maps to |
|-----------|---------|
| "pause scribe dulu" | pause by role or last active |
| "lanjutin lagi" | resume paused on active task |
| "cancel yang rangkum email" | stop by task summary match |
| "hapus sub-agent itu" | delete |
| "jalankan jam 3" | delay_start |

### Anti-blocker

- Control commands are **async writes** — orchestrator never joins worker
- pause/stop only affect **named** sub-agent
- User chat with orchestrator continues during paused workers
- Widget shows per-sub-agent lifecycle badge

### Sub-agent requirements

All worker roles must:

1. Poll `memory/control/<subAgentId>.json` between turns
2. Honor `pause` / `stop` before next LLM/tool call
3. Emit progress on lifecycle transitions
4. On `stop`: terminal `status: cancelled` before exit
5. Between turns: read `memory/control/<subAgentId>.json` — honor pause/stop before next action

Orchestrator tools: `control_subagent`, `schedule_subagent` (delay_start).

Config: `orchestrator.subAgentControl` in [config.schema.json](../schema/config.schema.json).

---

### Anti-poisoning rules (`orchestrator.antiContextPoisoning`)

1. **blockNewTaskUntilParkOrDone** — user minta task B while task A running → orchestrator: park A, switch, or finish A first.
2. **requireExplicitTaskSwitch** — no silent context blend; new task = new taskId + fresh context packet.
3. **Context packets are task-scoped** — context-scaler never injects full history from other tasks.
4. **parkInactiveAfterMinutes** — auto-park stale running tasks.

### Context poisoning example (blocked)

```
Task A (running): "draft proposal X"
User: "eh sekalian fix bug wec-customer"
Orchestrator: "Task proposal masih jalan. Park dulu, switch ke work bugfix, atau selesaikan proposal?"
```

---

## Progress streaming (orchestrator watches sub-agents)

Orchestrator **tidak menunggu** sub-agent selesai, tapi **streaming melihat progress** — termasuk state terakhir: thinking, response, toolcall, todo, in_progress, done.

### Progress event schema

Setiap sub-agent append ke stream:

```text
~/.config/sapaloq/memory/progress/<subAgentId>.jsonl
```

Satu baris = satu event:

```json
{
  "v": 1,
  "subAgentId": "sub-abc",
  "taskId": "task-001",
  "role": "scribe",
  "seq": 12,
  "ts": "2026-06-19T10:05:03Z",
  "type": "thinking",
  "status": "in_progress",
  "payload": {
    "text": "Mencocokkan intent catat → personal-notes..."
  }
}
```

### Event types (`type`)

| type | payload | Widget / orchestrator use |
|------|---------|---------------------------|
| `thinking` | `{ text }` | Ring pulse "deep think" |
| `response` | `{ text, partial? }` | Last assistant chunk |
| `tool_call` | `{ tool, args }` | Tool icon animation |
| `tool_result` | `{ tool, ok, summary }` | Success/fail flash |
| `todo` | `{ items: [{ id, content, status }] }` | Mini todo strip |
| `status` | `{ from, to, reason? }` | Lifecycle transitions |
| `clarification_request` | `{ question, options?, urgency, context? }` | Sub-agent butuh keputusan — lihat [Clarification loop](#clarification-loop-sub-agent--orchestrator--user) |
| `clarification_received` | `{ answer, source, resolvedBy }` | Jawaban kembali ke sub-agent |
| `control_ack` | `{ action, ok, detail? }` | Sub-agent ack pause/stop/resume |
| `error` | `{ message, recoverable }` | Error ring state |

### Status values (`status`)

| status | Meaning |
|--------|---------|
| `pending` | Spawned, belum mulai turn |
| `in_progress` | Aktif — thinking/tool/LLM |
| `done` | Selesai sukses |
| `failed` | Error terminal |
| `cancelled` | User/orchestrator abort |
| `awaiting_clarification` | Sub-agent paused — menunggu jawaban orchestrator/user |
| `paused` | Orchestrator paused — menunggu resume |
| `scheduled` | Delay start — belum spawn |
| `stopping` | Cooperative shutdown in progress |
| `stopped` | Cancelled by stop control |

### Orchestrator subscription

```mermaid
sequenceDiagram
  participant ORCH as Orchestrator
  participant SUB as sub-agent:scribe
  participant BUS as progress jsonl
  participant W as Widget ring

  ORCH->>SUB: spawn(contextPacket)
  ORCH->>W: delegating (1 active)
  loop each progress event
    SUB->>BUS: append event
    BUS-->>ORCH: tail / inotify
    ORCH->>W: mirror last event
  end
  SUB->>BUS: status done
  BUS-->>ORCH: completion trigger
  ORCH->>W: idle / notify user
```

Orchestrator holds **slim snapshot** per active sub-agent (bukan full stream):

```json
{
  "subAgentId": "sub-abc",
  "taskId": "task-001",
  "role": "scribe",
  "status": "in_progress",
  "lastEvent": {
    "type": "tool_call",
    "ts": "2026-06-19T10:05:04Z",
    "payload": { "tool": "append_file", "args": { "path": ".../notes.md" } }
  },
  "todos": [{ "id": "1", "content": "Append note", "status": "in_progress" }],
  "seq": 12
}
```

User bisa tanya: *"scribe lagi ngapain?"* → orchestrator baca snapshot + optional tail N events — **tanpa** inject full sub-agent history ke prompt (anti poisoning).

Config: `orchestrator.progressStreaming` (see config.schema.json).

---

## Clarification loop (sub-agent → orchestrator → user)

Saat sub-agent menemui **keputusan unclear** (ambiguous intent, boundary, path, config), ia **tidak** nebak — emit event ke orchestrator untuk tanya jawab.

### Trigger (sub-agent)

Tool: `ask_orchestrator` atau progress event `clarification_request`:

```json
{
  "v": 1,
  "subAgentId": "sub-abc",
  "taskId": "task-001",
  "role": "scribe",
  "seq": 8,
  "ts": "2026-06-19T11:00:00Z",
  "type": "clarification_request",
  "status": "awaiting_clarification",
  "payload": {
    "question": "Catat ke personal-notes atau work-inbox?",
    "options": ["personal-notes", "work-inbox"],
    "urgency": "blocking",
    "context": {
      "userSnippet": "catat: meeting besok",
      "mode": "auto",
      "reason": "mode ambiguous — Firefox work profile active but utterance generic"
    }
  }
}
```

Parallel bus (`events.jsonl`):

```json
{
  "v": 1,
  "kind": "subagent.clarification",
  "subAgentId": "sub-abc",
  "taskId": "task-001",
  "role": "scribe",
  "question": "Catat ke personal-notes atau work-inbox?",
  "ts": "ISO8601"
}
```

Sub-agent **pause** turn loop (`status: awaiting_clarification`) — tidak blocking orchestrator atau widget untuk task lain.

### Orchestrator routing (2 paths)

```mermaid
flowchart TD
  REQ[clarification_request]
  ORCH[Orchestrator]
  IX[(SQLite index + config)]
  USER[User via widget]
  SUB[sub-agent resume]

  REQ --> ORCH
  ORCH --> DEC{Can answer?}
  DEC -->|confidence >= threshold| IX
  IX --> A1[Answer from knowledge]
  A1 --> SUB
  DEC -->|low confidence or policy| FWD[Forward to user]
  FWD --> USER
  USER --> A2[User reply]
  A2 --> SUB
  SUB -->|clarification_received| RESUME[status in_progress]
```

#### Path 1 — Orchestrator jawab sendiri

Orchestrator **boleh** jawab langsung kalau jawaban ada di:

| Source | Example |
|--------|---------|
| `config.json` snapshot | `storage.intents`, mode defaults |
| SQLite facts / prefetch | User preference "catat → personal" |
| Active task + mode stack | Task already tagged `mode=work` |
| Context packet | `targetPathId` pre-resolved by boundary-guard |

Decision rule (`orchestrator.clarification.autoAnswerMinConfidence`, default 0.75):

```
if confidence >= threshold AND no policy block:
  → emit clarification_response to sub-agent
  → optional brief widget toast: "Aku arahkan scribe ke personal-notes"
else:
  → Path 2
```

Response event:

```json
{
  "type": "clarification_received",
  "status": "in_progress",
  "payload": {
    "answer": "personal-notes",
    "resolvedBy": "orchestrator",
    "source": "facts:preference/default_notes",
    "rationale": "Mode personal + intent catat → storage.intents"
  }
}
```

#### Path 2 — Forward ke user

Orchestrator tampilkan di widget chat (ring state: **needs-input**):

```
Scribe lagi nulis catatan tapi bingung:
"Catat ke personal-notes atau work-inbox?"
[Personal] [Work]  — atau balas bebas
```

- User reply → orchestrator normalizes → `clarification_received` ke sub-agent
- `resolvedBy: "user"`
- Optional: learning-agent promotes answer ke facts (async)

### Timeouts & fallback

| Config | Default | Behavior |
|--------|---------|----------|
| `clarification.userTimeoutSec` | 300 | Re-prompt once, then park task |
| `clarification.orchestratorMaxAutoAnswers` | 2 | Force user path after N auto-answers per task |
| `clarification.defaultOnTimeout` | `park` | `park` \| `fail` \| `orchestrator_best_guess` |

**Anti-blocker:** orchestrator tetap responsive; clarification hanya **pin** sub-agent yang bertanya, bukan freeze widget.

### Multi sub-agent

- Clarification dari sub-agent A **tidak** block sub-agent B (different namespace/task).
- Same task: max 1 `awaiting_clarification` at a time — queue further questions.

### Role prompt requirement

Setiap `prompt/roles/{role}.md` harus include:

```markdown
## When unclear
- Call ask_orchestrator with concrete question + options if possible
- Do NOT guess cross-mode paths or config mutations
- Set status awaiting_clarification until clarification_received
```

See [PROMPT-BUILDER-SOP.md](./PROMPT-BUILDER-SOP.md).

Config: `orchestrator.clarification` in [config.schema.json](../schema/config.schema.json).

---

## Completion triggers (sub-agent selesai)

Orchestrator perlu **tahu kapan sub-agent selesai** untuk: update widget, notify user, pop task stack, spawn follow-up.

### Primary: event trigger

Sub-agent **wajib** emit terminal event sebelum exit:

```json
{
  "type": "status",
  "status": "done",
  "payload": { "from": "in_progress", "to": "done", "summary": "Catatan ditambah ke personal-notes" }
}
```

Bus internal (`~/.config/sapaloq/memory/events.jsonl`) juga dapat:

```json
{
  "v": 1,
  "kind": "subagent.completed",
  "subAgentId": "sub-abc",
  "taskId": "task-001",
  "role": "scribe",
  "status": "done",
  "ts": "ISO8601"
}
```

Orchestrator **subscribe** via in-proc bus — lihat [EVENT-BUS.md](./EVENT-BUS.md) · [RUNTIME.md](./RUNTIME.md):

1. **Route watchers** — topic `sapaloq.v1.subagent.completed` → wake **<5ms**
2. **jsonl WAL** — async append (audit/replay)
3. **Unix socket** — sub-agent child → same binary

Boot replay: tail `events.jsonl` if `replayOnBoot` — then live bus only.

### Fallback: heartbeat (watchdog only)

Kalau sapaloq-core crash / sub-agent hang tanpa terminal event:

| Config | Default | Behavior |
|--------|---------|----------|
| `orchestrator.completion.heartbeatIntervalSec` | 60 | Watchdog — **not** primary wake when `wakeViaBus: true` |
| `orchestrator.completion.staleAfterSec` | 120 | Mark `failed` if no progress |
| `orchestrator.completion.requireTerminalEvent` | true | Log warning if exit without `done`/`failed` |

Heartbeat **bukan** busy-wait — timer di sapaloq-core; hanya stale/crash detection.

### On completion (orchestrator actions)

```text
subagent.completed (done)
  → update task stack (running → done)
  → widget: decrement active count
  → optional: notify user (if config notifications + user asked for update)
  → optional: spawn memory-janitor (async)

subagent.completed (failed)
  → surface last error event to user
  → keep task parked for retry
```

---

## Event watching (GNOME + custom)

Orchestrator **watching** sumber event di luar chat — proactive companion.

### Event bus (unified)

```text
~/.config/sapaloq/memory/events.jsonl
```

Semua sumber → satu append-only bus:

| source | kind | Example |
|--------|------|---------|
| `gnome` | `notification.received` | Slack, email client toast |
| `gnome` | `focus.changed` | User switch app → infer mode |
| `custom` | `reminder.fired` | "Minum obat 14:00" |
| `custom` | `email.received` | IMAP idle / webhook |
| `internal` | `subagent.completed` | Sub-agent lifecycle |
| `internal` | `subagent.clarification` | Sub-agent needs decision |
| `internal` | `task.parked` | Task stack change |

### Watcher config (`events.watchers[]`)

```json
{
  "id": "gnome-notifications",
  "enabled": true,
  "source": "gnome.notification",
  "mode": "any",
  "filters": {
    "apps": ["slack", "thunderbird"],
    "ignoreSilent": true
  },
  "action": {
    "type": "orchestrator",
    "spawnSubAgent": "task-runner",
    "template": "Summarize notification: {{body}}"
  }
}
```

```json
{
  "id": "daily-reminder",
  "enabled": true,
  "source": "custom.reminder",
  "schedule": "0 14 * * *",
  "payload": { "text": "Minum obat" },
  "action": {
    "type": "notify_user",
    "priority": "high"
  }
}
```

### GNOME notification path

```
GNOME Shell D-Bus / MCP gnome-desktop-mcp
  → event-watcher adapter
  → respect config.notifications (enabled, dndRespect, rateLimit)
  → append events.jsonl
  → wake orchestrator (inotify / socket)
```

Orchestrator **tidak** block di D-Bus — dedicated **event-watcher** process/thread.

### Custom sources (extensible)

| source | Mechanism | MVP |
|--------|-----------|-----|
| `custom.reminder` | cron / systemd timer / internal scheduler | ✅ |
| `custom.email` | IMAP IDLE, Gmail push, local script | later |
| `custom.webhook` | local HTTP `:sapaloq/events` | later |
| `custom.file` | inotify on drop folder | later |

Agent bisa tambah watcher via `/settings` → sub-agent:settings patch `events.watchers`.

### Rate limiting & mode

- `events.globalRateLimitPerMinute` — flood protection.
- Watcher `mode: work` — hanya aktif saat orchestrator mode = work.
- `notifications.dndRespect` — skip GNOME notify → orchestrator saat DND on.

---

## Anti-blocker design

| Principle | Implementation |
|-----------|----------------|
| Orchestrator fast | max ~3 turns; delegate immediately |
| Async sub-agents | subprocess / worker pool; events on complete |
| Parallel when safe | memory-janitor + scribe concurrent if different namespaces |
| Widget responsive | ring shows sub-agent count + **last progress event**; chat never frozen |
| Progress without blocking | tail jsonl / inotify; orchestrator never joins sub-agent process |
| Completion without blocking | event trigger primary; heartbeat only for stale detection |
| Aggressive workers | sub-agent task-runner high maxTurns; orchestrator doesn't wait |

Event flow:

```
orchestrator → spawn(sub-agent, contextPacket)
orchestrator → subscribe progress stream (tail jsonl)
orchestrator → user reply "Oke, lagi kutangani"
... streaming ...
sub-agent → progress: tool_call, response, todo ...
orchestrator → widget mirror last event
... later ...
sub-agent → progress: status done + events.jsonl subagent.completed
orchestrator → notify user + widget idle
```

Parallel event flow (proactive):

```
GNOME notification → event-watcher → events.jsonl
orchestrator → match watcher rule → spawn task-runner (async)
user sees: "Ada notif Slack — mau aku rangkum?"
```

---

## Shared memory (sub-agents only)

Orchestrator reads **summaries**, not raw dumps.

```text
~/.config/sapaloq/memory/
  companion.db           # namespaces: personal, hobby, work
  tasks/                 # task stack persistence
  context-packets/       # ephemeral, task-scoped
    <taskId>.json
  progress/              # sub-agent progress streams
    <subAgentId>.jsonl
  control/               # orchestrator → sub-agent commands
    <subAgentId>.json
  events.jsonl           # unified event bus (GNOME, custom, internal)
```

**memory-janitor** (auto-spawn):

- Dedupe facts
- Promote short-term → long-term
- Prune context-packets for done tasks
- Never blocks orchestrator

---

## Context packet (context-scaler output)

```json
{
  "taskId": "task-001",
  "mode": "personal",
  "intent": "catat",
  "userSnippet": "beli susu besok",
  "targetPathId": "personal-notes",
  "relevantFacts": ["..."],
  "excludedTasks": ["task-002"],
  "configSnapshot": {
    "notifications.read": false
  },
  "prefetch": {
    "confidence": 0.82,
    "factsUsed": 3,
    "skillsLoaded": ["sapaloq-scribe"],
    "antiDeepCheck": true
  }
}
```

Small by design — anti poisoning.

### Spawn payload (includes role system-prompt + node)

```json
{
  "subAgentId": "sub-abc",
  "node": "vps-scribe",
  "role": "scribe",
  "systemPrompt": "<assembled from roles/scribe.md + overlay + skills — see PROMPT-BUILDER-SOP.md>",
  "contextPacket": { "...": "..." },
  "allowedTools": ["append_file", "emit_progress"],
  "maxTurns": 8
}
```

`node` references `nodes.name` in SQLite. Default: `local-default`. See [NODES.md](./NODES.md).

Post-task: orchestrator emits `subagent.completed` → **learning-agent** async (prompt/skill builder, optional research).

---

## Implementation order

| Step | Deliverable |
|------|-------------|
| 1 | `config.schema.json` + example + validator |
| 2 | Orchestrator task stack + `/settings` router |
| 3 | sub-agent:settings (config R/W) |
| 4 | Progress jsonl schema + orchestrator tail/subscribe |
| 5 | Completion events + heartbeat stale detection |
| 6 | Event bus + GNOME notification watcher |
| 7 | sub-agent:scribe + storage mapping |
| 8 | boundary-guard + context-scaler |
| 9 | memory-janitor auto-spawn |
| 10 | Widget ring: progress mirror + delegate states |
| 11 | Clarification loop: ask_orchestrator + user forward |
| 12 | Sub-agent control: delay_start, pause, resume, stop, delete |

---

## Related files

- [PROMPT-BUILDER-SOP.md](./PROMPT-BUILDER-SOP.md)
- [CONTEXT-SOP.md](./CONTEXT-SOP.md)
- [RUNTIME.md](./RUNTIME.md)
- [EVENT-BUS.md](./EVENT-BUS.md)
- [VISION.md](./VISION.md)
- [config.schema.json](../schema/config.schema.json)
- [config.example.json](../config/config.example.json)
