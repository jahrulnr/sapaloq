---
description: |
  Read a text file from any host path (absolute, ~-relative, or CWD-relative). Returns numbered lines; refuses binary. Use offset/limit for large files. Default blocks until content returns; set wait_for_output:false for slow reads and collect with wait mode=tool.
---

# read_file

Prefer this over exec cat/sed for structured reads. Any path on the host is allowed.
