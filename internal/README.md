# internal/

Private Go packages for the SapaLOQ monorepo. See [docs/BLUEPRINT.md](../docs/BLUEPRINT.md) Part XIX (package map).

| Package | Role |
|---------|------|
| `bridge/` | `StreamEvent`, registry, canonical `ToolCall` contract |
| `bridges/cursor/credentials/` | Autoload token + machine id (env → .env → state.vscdb) |
| `bridges/cursor/` | cursor-bridge driver — live api2 stream, alias coercion, vault hook |
| `bridges/cursor/wire/` | Connect+proto encode/decode |
| `config/` | `config.json` load, patch, runtime paths |
| `core/orchestrator/` | Chat loop, `/settings`, slash routing, progress |
| `ipc/` | Unix socket protocol for widget |
| `parse/` | Tool/thinking parsers (cursor, kimi) |
| `vault/` | JSONL log for undeclared structured tool calls |
| `bus/` | In-process event bus |

Vault log: `~/SapaLOQ/vault/tool-calls.jsonl` — review via `sapaloq-core vault list`.
