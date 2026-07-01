---
description: |
  End YOUR OWN run only. Foreground Orchestrator: default (or scope=generation) stops the current chat turn—background planner/agent keep running. Sub-agent (planner/agent/scribe): stops that actor's loop only. To cancel a stuck background task from orchestrator, use scope=task with task_id (supervisor kill—not self-stop). scope=all cancels every session background task—use only when the user explicitly asks to stop everything. A tool-less turn is NOT a stop. Silent action: invoke the tool, do not narrate status in prose.
---

# sapaloq_stop

## Contract (read before calling)

**Self-stop vs supervisor kill**

| Caller | Args | Effect |
|--------|------|--------|
| **Orchestrator** | `{}` or `{"scope":"generation"}` | Ends the **foreground chat turn** only. Delegated planner/agent **continue in background**. Use after fire-and-forget spawn acknowledgement. |
| **Orchestrator** | `{"scope":"task","task_id":"task-..."}` | Cancels **one** background task by id **and** ends the foreground turn. Use when the user asks to abort a stuck sub-agent. Get `task_id` from `sapaloq_get_task_status`. |
| **Orchestrator** | `{"scope":"all"}` | Cancels **all** background tasks in the session **and** ends foreground. Rare—only when user wants everything stopped. |
| **Planner / Agent / Scribe** | `{}` or optional `reason` | Stops **this sub-agent's run only**. Does not affect other actors. Planner: call when plan is final. Agent: prefer `sapaloq_complete_task` / `sapaloq_fail_task` when reporting outcome. |

**Do not confuse**

- Stopping **your chat turn** (generation) ≠ stopping a **background planner**.
- `request_clarification` pauses a sub-agent; it is **not** a stop signal.
- Widget Stop button uses `generation` scope so background work is not killed accidentally.

## Examples

```json
{"reason":"acknowledged delegate"}
```

Ask after spawning planner (fire-and-forget)—background keeps working:

```json
{"scope":"generation","reason":"done replying to user"}
```

Ask abort one stuck planner (user said "abort planner"):

```json
{"scope":"task","task_id":"task-20250628-abc","reason":"user abort"}
```

Planner finished plan:

```json
{"reason":"plan complete and verified"}
```
