# SapaLOQ - Sub-agent Nodes

> Sub-agent = **node** - bisa local goroutine **atau** remote (Docker, VPS, EC2, SSH host).
> Registry di `state/config/nodes.json` + **comm spec** (SKILL-like) per node.
> Last updated: 2026-06-28 (JSON node registry; SQLite retired)

Related: [ORCHESTRATOR.md](./ORCHESTRATOR.md) · [DRIVER.md](./DRIVER.md) · [EVENT-BUS.md](./EVENT-BUS.md)

---

## Konsep

```
Orchestrator (local sapaloq-core)
    │
    ├── spawn → node:local-default     (in-proc goroutine)
    ├── spawn → node:vps-scribe        (HTTP/WS worker)
    ├── spawn → node:ec2-research     (remote MCP)
    └── spawn → node:docker-task       (container wrapper)
```

| Local | Remote (outer machine) |
|-------|------------------------|
| Same machine, `sapaloq.sock` | Out-of-machine execution |
| **Shared memory bus OK** | **No shared memory** - context packet only |
| Low latency | Network latency + stale cache risk |
| Full desktop driver | Role-specific; own storage root |

Core orchestrator **tetap** di mesin user (widget). Node = **where sub-agent runs**.

---

## Memory policy (local vs remote)

| | Same machine | Outer machine |
|--|--------------|---------------|
| **Shared memory index?** | ✅ Local sub-agents (`facts.json`) | ❌ **Not recommended** |
| **What remote gets** | Full memory bus access | **Context packet** at spawn (bounded) |
| **What remote returns** | - | Progress stream + task result summary |
| **Learning / facts** | memory-janitor local | Remote **may not** write orchestrator JSON store; local promotes after `completed` |

### Why no shared memory to remote nodes

1. **Latency** - prefetch & FTS useless over network RTT
2. **Stale memory** - remote acts on outdated facts; orchestrator assumes fresh index
3. **Sync complexity** - replication, conflict resolution, offline partitions = out of scope

### Remote node contract

```json
{
  "spawn": {
    "systemPrompt": "...",
    "contextPacket": { "taskId", "mode", "userSnippet", "relevantFacts", "configSnapshot" },
    "noMemoryBus": true
  },
  "return": {
    "progressStream": true,
    "resultSummary": "optional structured facts for local learning_queue"
  }
}
```

Optional: remote node keeps **its own** local store - **never** mounted as orchestrator memory.

Same-host Docker: default `shareMemory: false` in node row; explicit opt-in only for dev.

### `share_memory` enforcement

| Who | When |
|-----|------|
| **orchestrator** (pre-spawn) | Remote or `share_memory=0` → `contextPacket.noMemoryBus=true`; block memory bus tools on remote |
| **boundary-guard** | Reject remote `share_memory=1` unless `nodes.allowSharedMemoryRemote` |
| **Node client** | Must not open orchestrator memory files - packet-only over wire |

`local-default` bootstrap: **`share_memory=1`**. Remote nodes: **always 0**.

---

## Node registry: `state/config/nodes.json`

Path: `~/SapaLOQ/state/config/nodes.json` (array of node records; CRUD in `internal/store/chat/nodes.go`).

```json
{
  "name": "local-default",
  "role": "*",
  "wrapper": "local",
  "communicate": "unix",
  "comm_spec_path": "~/SapaLOQ/nodes/local-default.md",
  "enabled": true,
  "priority": 0,
  "share_memory": true
}
```

Legacy DDL (pre-JSON) for reference:

```sql
CREATE INDEX idx_nodes_role ON nodes(role, enabled, priority DESC);
```

### Field guide

| Field | Example | Notes |
|--------|---------|-------|
| **name** | `vps-scribe` | Stable id; orchestrator references this |
| **role** | `scribe` | Must match `subAgents.roles` |
| **wrapper** | `vps` | How compute is wrapped |
| **address** | `deploy@103.250.x.x` | SSH target, URL host, container name |
| **communicate** | `ws` | Transport to node agent |
| **capabilities** | JSON override | Optional |
| **share_memory** | `false` / `true` | `true` only same-machine; **always false for outer machine** |

---

## Wrapper types (`wrapper`)

| Value | Meaning | Typical address |
|-------|---------|-----------------|
| `local` | In-proc / same binary | empty or `unix://sapaloq.sock` |
| `machine` | Another bare metal / VM | `user@host` |
| `docker` | Container on local or remote | `container://name` or `ssh://user@host/docker/name` |
| `vps` | Generic VPS | `user@ip` |
| `ec2` | AWS EC2 (still ssh/http underneath) | `user@ec2-xx.compute.amazonaws.com` |
| `ssh` | SSH tunnel / remote shell worker | `user@host:22` |

Wrapper = **topology hint** for orchestrator UI + doctor; transport = **`communicate`**.

---

## Communicate (`communicate`)

| Value | Use case |
|-------|----------|
| `unix` | Local `sapaloq.sock` (default local node) |
| `http` | REST spawn + progress poll/webhook |
| `ws` | Bidirectional stream (progress, control, clarification) |
| `mcp` | Remote MCP server as node backend |
| `grpc` | Future structured RPC |
| `ssh` | Invoke remote `sapaloq-node` CLI over SSH |

Orchestrator reads **comm spec** to know exact URLs, headers, auth env vars.

---

## Comm spec (`nodes/{name}.md`) - like SKILL.md

Not hand-wavy config - **operating manual** for talking to this node.

```markdown
---
node: vps-scribe
role: scribe
wrapper: vps
address: deploy@103.250.x.x
communicate: ws
---

# Node: vps-scribe

## Endpoints

- WS: `wss://103.250.x.x:8443/sapaloq/v1/node`
- Health: `GET https://103.250.x.x:8443/health`

## Auth

- Env: `SAPALOQ_NODE_TOKEN` (never commit value)
- Header: `Authorization: Bearer ${SAPALOQ_NODE_TOKEN}`

## Spawn protocol

1. Connect WS with `node: vps-scribe` in hello frame
2. Send `spawn` envelope (systemPrompt, contextPacket, taskId, subAgentId)
3. Receive `progress` events → forward to local bus topic `sapaloq.v1.subagent.progress.{id}`
4. On `completed`, close or keep-alive per config

## Control

- `pause` / `resume` / `stop`: WS frame `control`
- Must ack within 10s

## Boundaries

- No desktop tools - scribe only writes to agreed paths
- Storage root: `/data/sapaloq/scribe/` on remote (sync policy: pull via rsync optional)

## Failure

- Retry 3x exponential
- On fail: orchestrator fallback to `node:local-default` if enabled
```

Agent can create/update via `/settings register node ...` → sub-agent writes row + md file.

---

## Default local node (bootstrap)

On first boot, insert:

```sql
INSERT INTO nodes (name, role, wrapper, address, communicate, comm_spec_path, ...)
VALUES (
  'local-default',
  '*',                    -- any role fallback
  'local',
  '',
  'unix',
  '~/SapaLOQ/nodes/local-default.md',
  1,                      -- share_memory = 1 (local bus OK)
  ...
);
```

Role-specific locals optional: `local-scribe`, `local-task-runner`.

---

## Orchestrator spawn with node

```json
{
  "subAgentId": "sub-abc",
  "node": "vps-scribe",
  "role": "scribe",
  "systemPrompt": "...",
  "contextPacket": { "taskId": "task-001" }
}
```

Selection logic:

1. User hints node name → use if enabled
2. Else highest `priority` node matching `role`
3. Else `local-default`
4. boundary-guard: remote node → **no memory bus**; validate context packet paths only

---

## Progress & control over network

Remote node **must** mirror local progress protocol:

```
Remote WS → sapaloq-core → bus.Publish(sapaloq.v1.subagent.progress.{id})
```

Clarification + control frames work same as local - orchestrator routes to WS instead of unix socket.

Config: `nodes.allowRemoteRoles`, `nodes.requireTls`.

---

## Security notes

| Risk | Mitigation |
|------|------------|
| Token in comm spec | Reference env var only; never store secret in node registry |
| Remote scribe path escape | boundary-guard + allowed paths in comm spec |
| MITM | TLS required for http/ws (`requireTls: true`) |

---

## Agent commands

| User | Action |
|------|--------|
| `/settings list nodes` | Query `nodes` table |
| `/settings register node vps-scribe ...` | Insert row + generate comm spec template |
| `/settings disable node vps-scribe` | `enabled=0` |

---

## Implementation order

| Step | Deliverable |
|------|-------------|
| 1 | `nodes` table migration |
| 2 | Bootstrap `local-default` + comm spec template |
| 3 | Orchestrator node picker on spawn |
| 4 | `communicate: unix` local (existing) |
| 5 | `communicate: ws` remote client |
| 6 | `/settings` node CRUD sub-agent |
| 7 | Fallback + `last_seen_at` health |

---

## Limitations

See [LIMITATIONS.md](./LIMITATIONS.md) - remote nodes add network partition, latency, stale memory risk, and trust boundaries. **No shared memory to outer machines.**
