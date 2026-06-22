# SapaLOQ Runtime Map & Roadmap

> Last updated: 2026-06-22

This document is the canonical map for SapaLOQ itself. Runtime actors receive
the active values as a system-context block; do not guess paths from process
working directory.

## Runtime variables

```text
config_path=~/.config/sapaloq/config.json
data_path=~/SapaLOQ/
memory_path=~/SapaLOQ/memory/
state_path=~/SapaLOQ/state/
workspace=~/SapaLOQ/workspace/
prompts_path=~/SapaLOQ/prompts/
skills_path=~/SapaLOQ/skills/
vault_path=~/SapaLOQ/vault/
run_path=~/SapaLOQ/run/
etc_path=~/SapaLOQ/etc/
runtime_roadmap=~/SapaLOQ/etc/ROADMAP.md
```

`SAPALOQ_CONFIG` may override `config_path`. Explicit custom paths in config
remain authoritative.

## Workspace contract

- Each orchestrator/planner/agent actor starts in `workspace`.
- Relative file paths and `exec` resolve from that actor's current workspace.
- A successful shell `cd` persists the resulting directory for later calls by
  the same actor.
- A new actor starts from the default workspace unless it has persisted state.

## Runtime direction

- UI shows the active provider/model and live background actors.
- Tool execution uses durable parallel jobs with resource lanes.
- Actor follow-up uses durable steering events.
- Planner/Agent decisions are mediator-first and reach the UI only when user
  authority is genuinely required.
- Config stays separate from runtime data so backup and cleanup boundaries are
  explicit.
