---
node: vps-scribe
role: scribe
wrapper: vps
address: deploy@103.250.x.x
communicate: ws
---

# Node: vps-scribe

Remote scribe on VPS — append-only notes, no desktop tools.

## Endpoints

- WebSocket: `wss://103.250.x.x:8443/sapaloq/v1/node`
- Health: `GET https://103.250.x.x:8443/health`

## Auth

- Environment variable: `SAPALOQ_NODE_VPS_SCRIBE_TOKEN`
- Header: `Authorization: Bearer ${SAPALOQ_NODE_VPS_SCRIBE_TOKEN}`

## Spawn protocol

1. Open WS; send hello: `{ "op": "hello", "node": "vps-scribe", "token": "..." }`
2. Send spawn envelope with `systemPrompt`, `contextPacket`, `subAgentId`, `taskId`
3. Stream progress events — same schema as local progress jsonl
4. Terminal: `{ "type": "status", "status": "done" | "failed" }`

## Control

| Frame | Payload |
|-------|---------|
| `control` | `{ "action": "pause" \| "resume" \| "stop" }` |
| `clarification_response` | `{ "answer": "...", "resolvedBy": "orchestrator" }` |

## Storage boundary

- Remote write root: `/data/sapaloq/scribe/`
- Do not write to orchestrator local `~/Documents/sapaloq/` unless explicit sync job

## Failure

- Retry: 3 attempts, backoff 1s / 2s / 4s
- Fallback node: `local-default` (if orchestrator policy allows)
