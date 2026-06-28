---
description: |
  Resume a failed or stopped background task from its persisted turns. Use after connection/core errors or to continue the same sub-agent work. Omit task_id to resume the latest resumable task in this session. Does not accept wait_for_output.
---

# sapaloq_resume_task

Re-enters the same task id with `state/tasks/{id}/turns.json` replayed. Prefer this over a new `sapaloq_spawn_*` when continuing the **same** failed/stopped task; parallel spawns for separate jobs remain allowed.

Not for `awaiting_clarification` — use `sapaloq_answer_clarification` instead.

Task artifacts are removed when the parent chat session is deleted.
