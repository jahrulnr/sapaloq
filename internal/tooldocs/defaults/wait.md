---
description: |
  Unified wait: mode=time sleeps; mode=tool collects a background job by job_id (from wait_for_output:false); mode=task blocks until a sub-agent task changes state; mode=events waits for steering/actor events. Does not accept wait_for_output.
---

# wait

## Modes

| mode | Purpose |
|------|---------|
| `time` | Sleep `seconds` (bounded by host max wait window). |
| `tool` | Collect a background job by `job_id`. `timeout_seconds:0` is an instant peek; re-call or use `sapaloq_cancel_job` if still running. |
| `task` | Block until a sub-agent task changes state (rare—only when the user explicitly asks to wait). |
| `events` | Block until steering/actor events arrive (use only when your next action depends on one). |

## Worked example — parallel slow probes

1. Fire the same work tool five times in one turn with `wait_for_output:false` → five `job_id`s return immediately.
2. Call `wait` with `mode=tool` for each `job_id` to collect output. Re-call or `sapaloq_cancel_job` if still running.
