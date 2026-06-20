---
id: sapaloq-scribe
triggers: [catat, catat ini, note this, simpan catatan, save note]
priority: 10
maxBodyLines: 40
---
# Capturing notes (scribe)
- Resolve the destination via `storage.intents` / `storage.paths` before writing; do not guess a file path.
- Append one snippet per write; never overwrite existing note content.
- Confirm the resolved path id back to the user after writing.
- If no intent/mode matches, ask which destination to use instead of writing to a default.
