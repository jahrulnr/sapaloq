---
node: local-default
role: "*"
wrapper: local
address: ""
communicate: unix
---

# Node: local-default

In-process sub-agent on the same machine as `sapaloq-core`.

## Endpoints

- Unix socket: `~/.config/sapaloq/run/sapaloq.sock`

## Spawn protocol

1. Publish spawn via in-proc bus or socket `op: spawn_local`
2. Progress: `~/.config/sapaloq/memory/progress/{subAgentId}.jsonl`
3. Control: bus topic `sapaloq.v1.orchestrator.control.{subAgentId}`

## Auth

None - same user session.

## Failure

If local spawn fails, log and surface to orchestrator; no remote fallback unless configured.
