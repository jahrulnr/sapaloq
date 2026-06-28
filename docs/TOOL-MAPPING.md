# Tool mapping — Cursor upstream → SapaLOQ declared

> Last updated: 2026-06-28

SapaLOQ declares a **small, stable** tool surface (`read_file`, `exec`, `glob`, …).
Cursor / Kimi / api2 / api5 emit a **larger upstream namespace** (`glob_file_search`,
`run_terminal_cmd`, `Glob`, `Shell`, …). The cursor-bridge normalizes ingress before
`VaultReason` and `EventToolCall` emission.

Implementation: `internal/bridges/cursor/declared_map.go` (`ResolveToolCall`), also
`internal/core/orchestrator/tool_normalize.go` at dispatch ingress.

---

## Pipeline

```
raw tool call (wire / Kimi inline / protobuf)
  → CoerceToolCall        provider.aliases (glob → glob_file_search)
  → MapToDeclared         upstream / product → SapaLOQ name
  → RemapDeclaredArguments  field renames (glob_pattern → pattern)
  → VaultReason           drop if not on declared surface for this request
  → orchestrator dispatch (normalizeUpstreamToolCall repeats ResolveToolCall for openai_inline / provider paths)
```

---

## Upstream native → SapaLOQ

| Upstream (after aliases) | SapaLOQ | Notes |
|--------------------------|---------|-------|
| `run_terminal_cmd` | `exec` | `command` preserved |
| `grep` | `search` | regex content search |
| `glob_file_search` | `glob` | `glob_pattern` → `pattern` |
| `file_search` | `glob` | filename search |
| `codebase_search` | `search` | semantic → regex fallback |
| `write` | `write_file` | |
| `search_replace` | `edit_file` | |
| `apply_patch` / `StrReplace` | `edit_file` | patch → str replace |
| `read_file` | `read_file` | identity |
| `delete_file` | `delete_file` | identity |
| `list_dir` | `list_dir` | identity |
| `web_search` | `web_search` | `search_term` → `query` |
| `webfetch` | `web_fetch` | |
| `read_lints` | `exec` | run linter via shell |
| `ask_question` | `request_clarification` | sub-agent only |

---

## Cursor product names (PascalCase / IDE)

| Product | SapaLOQ | Vault if undeclared |
|---------|---------|---------------------|
| `Shell` | `exec` | |
| `Grep` | `search` | |
| `Glob` | `glob` | |
| `Read` | `read_file` | |
| `Write` | `write_file` | |
| `Delete` | `delete_file` | |
| `StrReplace` | `edit_file` | |
| `WebSearch` | `web_search` | |
| `WebFetch` | `web_fetch` | |
| `SemanticSearch` | `search` | |
| `AskQuestion` | `request_clarification` | |
| `ReadLints` | `exec` | |
| `TodoWrite` | — | vault (`productOnly`) |
| `Task` | — | vault (`productOnly`) |
| `Await` | — | vault (`productOnly`) |
| `CallMcpTool` | — | vault (`productOnly`) |
| `GenerateImage` | — | vault (`productOnly`) |
| `SwitchMode` | — | vault (`productOnly`) |
| `EditNotebook` | — | vault (no impl) |
| `ListMcpResources` | — | vault |
| `FetchMcpResource` | — | vault |

Schema reference (documentation only today): `cursor-bridge.schema.json` →
`clients.cursorAgent.tools.*.mapsTo`.

---

## SapaLOQ declared tools (orchestrator)

Full registry: `internal/core/orchestrator/tools.go` + `reg()` schemas.

| Category | Tools |
|----------|-------|
| Read / search | `read_file`, `search`, `list_dir`, `glob`, `read_image`, `web_search`, `web_fetch` |
| Write / edit | `write_file`, `create_file`, `edit_file`, `delete_file` |
| Host | `exec`, `desktop_notify`, `desktop_dnd_status` |
| Lifecycle | `sapaloq_stop`, `wait`, `sapaloq_cancel_job`, … |
| Delegation | `sapaloq_spawn_plan`, `sapaloq_spawn_agent`, `sapaloq_spawn_scribe`, … |
| Plan / scribe | `write_plan`, `read_plan`, `scribe_write_note`, … |

---

## Vault workflow

When a structured call is dropped, a row is appended to `~/SapaLOQ/vault/tool-calls.jsonl`:

| `reason` | Meaning |
|----------|---------|
| `undeclared` | Mapped upstream name not in this request's `declaredTools` |
| `unknown_upstream` | Name not in schema native catalog + aliases |
| `executed` | Orchestrator ran the tool (audit) |

**Triage:**

```bash
jq -r 'select(.reason!="executed") | [.reason,.raw_name,.resolved_name] | @tsv' \
  ~/SapaLOQ/vault/tool-calls.jsonl | sort | uniq -c | sort -rn
```

1. Add row to `upstreamToDeclared` / `productToDeclared` in `declared_map.go`
2. Add arg rewrites in `RemapDeclaredArguments` if needed
3. Ensure tool is in the role's static profile (`tools.go`) if new
4. Update this doc + `declared_map_test.go`

---

## api5 agent path (built-in exec)

On the Agent API path, Cursor may issue **built-in exec** frames (`exec_read`,
`exec_grep`, …) instead of MCP tool calls. Those are currently **rejected on the
wire** (`builtinToolRejectReason`) — the model is steered toward declared MCP tools.
Mapping table above applies to **MCP / protobuf tool_call** ingress; native exec
translation is a separate follow-up.

---

## Related docs

- `docs/BRIDGE.md` — vault + declared surface
- `docs/ORCHESTRATOR.md` — per-role tool profiles
- `internal/bridges/cursor/cursor-bridge.schema.json` — upstream catalog + client coercions
