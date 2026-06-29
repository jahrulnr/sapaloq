---
description: |
  Search Brave, Startpage, Wikipedia, and GitHub concurrently for current information. Returns fused title, URL, and snippet results; wait_for_output:false for slow queries—collect with wait mode=tool.
---

# web_search

Use when repo/local context is insufficient or facts may be stale. Results are
deduplicated and ranked across sources; one failed source does not discard
successful results. Follow a result URL with `web_fetch` when the snippet is
not enough to answer accurately.
