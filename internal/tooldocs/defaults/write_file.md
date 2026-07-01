---
description: |
  Overwrite a file at any host path (creates parent dirs). Executor/Ask only—not planner. Default blocks; wait_for_output:false for large writes—collect with wait mode=tool. Independent paths in the same turn run in parallel.
---

# write_file

Creates parent directories as needed. Prefer edit_file for small changes to existing files.
