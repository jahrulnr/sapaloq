---
description: |
  Run any shell command on the host via bash -lc (full access; also reads files via cat/sed/head/tail/rg). For SHORT commands that finish promptly. NEVER for long-running servers, watchers, or tail -f—use wait_for_output:false to get job_id, then wait mode=tool or sapaloq_cancel_job. cwd persists per actor.
---

# exec

Mind macOS BSD vs GNU flags and Windows hosts without bash.
