---
name: peek-agents
description: Inspect and monitor running or finished sub-agents (planner, task-runner, scribe) from the orchestrator. Use when the user wants to peek at, monitor, watch, or check an agent/planner/worker, ask the status of a task, find out why a task failed or got stuck/stalled, review a plan, or see which tasks are waiting for clarification.
triggers: [intip, pantau, cek agent, lihat agent, status task, status tugas, gagal, stuck, error, monitor, planner, agent, worker, health, heartbeat, stalled, awaiting clarification, clarification, klarifikasi, peek, inspect, review plan, lihat plan]
---

# Peek at agents (planner / task-runner / scribe)

Inspect sub-agents through their on-disk artifacts. Everything an agent does is
written under `state/` as plain JSON + logs, so peeking is just reading files —
no extra tool is needed. Prefer the internal tools (`read_file`, `glob`,
`list_dir`, `search`); drop to `exec` only when you need an ad-hoc shell view.

Always resolve paths from the injected runtime variable `state_path` (see the
"SapaLOQ runtime variables" system block). Never hard-code `~/SapaLOQ/...`.

## Artifact schema (where to look)

| Artifact | Path | Key fields |
|----------|------|------------|
| Task record | `${state_path}/tasks/<id>/status.json` | `role`, `status` (in_progress / done / failed / stopped / awaiting_clarification), `result`, `error`, `question`, `updated_at` |
| Plan | `${state_path}/tasks/<id>/plan.md` | full planner output (markdown) |
| Worker health (liveness) | `${state_path}/workers/<id>/health.json` | `phase`, `status`, `pid`, `started_at`, `last_heartbeat` |
| Worker error trail | `${state_path}/workers/<id>/error.log` | timestamped error lines (most useful on failure) |
| Progress stream | `${state_path}/progress/` | per-task progress (optional, verbose) |

`status.json` is the durable *outcome*; `health.json` is the live *liveness*. A
worker that is `in_progress` but whose `last_heartbeat` is old is stalled.

## Primary path — internal tools (most robust, OS-agnostic)

1. **List live/finished workers**
   `glob` → `${state_path}/workers/*/health.json`, then `read_file` each to see
   `phase` + `last_heartbeat`.
2. **Drill into one task**
   `read_file ${state_path}/tasks/<id>/status.json` → read `status`, `result`,
   `error`, `question`.
3. **Diagnose a failure**
   `read_file ${state_path}/workers/<id>/error.log`, then cross-check
   `status.error` in `status.json`.
4. **Review a plan**
   `read_file ${state_path}/tasks/<id>/plan.md`.
5. **Find tasks awaiting the user**
   `glob ${state_path}/tasks/*/status.json`, open each, look for
   `"status": "awaiting_clarification"` and surface its `question`.

For a live, terminal-state wait, prefer the built-in `sapaloq_wait` /
`sapaloq_get_task_status` tools instead of polling files in a tight loop.

## Flexible path — `exec` (ad-hoc shell)

Use a small `timeout_seconds` (e.g. 10–20). Do **not** assume `jq` exists; parse
JSON with the internal `read_file` tool, or `python3 -c` / PowerShell
`ConvertFrom-Json` when a shell view is required.

### Linux / macOS (bash)

```bash
# One-shot summary of every agent (bundled, no jq):
bash "${skills_path}/peek-agents/scripts/peek.sh"

# Drill into one task:
bash "${skills_path}/peek-agents/scripts/peek.sh" <task-id>

# Raw listings:
ls -1 "${state_path}/workers"
cat "${state_path}/tasks/<id>/status.json"
cat "${state_path}/workers/<id>/error.log"

# Tasks waiting for the user:
grep -rl 'awaiting_clarification' "${state_path}/tasks"/*/status.json 2>/dev/null

# Find failed tasks:
grep -rl '"status": "failed"' "${state_path}/tasks"/*/status.json 2>/dev/null

# Parse a field without jq (portable):
python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["status"])' \
  "${state_path}/tasks/<id>/status.json"
```

### Windows (PowerShell)

```powershell
# List worker health:
Get-ChildItem "$env:state_path\workers" -Directory |
  ForEach-Object { Get-Content "$($_.FullName)\health.json" -Raw | ConvertFrom-Json } |
  Select-Object id, role, status, phase, last_heartbeat

# One task:
Get-Content "$env:state_path\tasks\<id>\status.json" -Raw | ConvertFrom-Json

# Tasks awaiting clarification:
Select-String -Path "$env:state_path\tasks\*\status.json" -Pattern 'awaiting_clarification'
```

> `${skills_path}` and `${state_path}` are SapaLOQ runtime variables; substitute
> the actual values from the runtime system block. On Windows they are exposed as
> environment variables to the shell.

## Workflow

1. **Roster + health** — list `workers/*/health.json`; flag any `in_progress`
   with a stale `last_heartbeat` as likely stalled.
2. **Drill down** — open the task's `status.json` for outcome and any pending
   `question`.
3. **On failure** — read `error.log` first (chronological trail), then
   `status.error` for the final reason.
4. **Plan review** — read `plan.md` when the user asks about a planner.
5. **Awaiting user** — surface any `awaiting_clarification` question so the user
   can answer (then `sapaloq_answer_clarification`).

## Guardrails

- Peeking is read-only — never edit or delete these artifacts.
- Keep `exec` timeouts small; avoid tight polling loops (use `sapaloq_wait`).
- Treat a missing/empty/corrupt file as "no data yet", not an error.
