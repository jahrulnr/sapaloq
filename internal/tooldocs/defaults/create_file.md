---
description: |
  Create a new file that must not exist yet (any host path; parent dirs created). Default blocks; wait_for_output:false for large payloads—collect with wait mode=tool. Independent paths in the same turn run in parallel—prefer this over exec heredocs for multi-file scaffolds.
---

# create_file

Fails if the path already exists—use write_file or edit_file to update.
