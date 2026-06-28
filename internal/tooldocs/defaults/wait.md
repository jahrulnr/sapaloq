---
description: |
  Unified wait: mode=time sleeps; mode=tool collects a background job by job_id (from wait_for_output:false); mode=task blocks until a sub-agent task changes state; mode=events waits for steering/actor events. Does not accept wait_for_output.
---

# wait

mode=tool timeout_seconds=0 is an instant peek. Re-call or sapaloq_cancel_job if still running.
