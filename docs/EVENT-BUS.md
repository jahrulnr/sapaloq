# SapaLOQ — Event Bus (in-process)

> **Internal pub/sub** inside `sapaloq-core` — goroutine + channel + route watcher.
> Bukan service terpisah. Bukan Redis / Rabbit / MQTT.
> Last updated: 2026-06-19

Related: [RUNTIME.md](./RUNTIME.md) · [ORCHESTRATOR.md](./ORCHESTRATOR.md)

---

## Prinsip

Event wake = **fungsi dalam binary yang sama**:

1. **Route table** — topic pattern → watchers
2. **Publish** — fan-out ke `chan Envelope` (<1ms)
3. **Unix socket** — optional IPC ke sub-agent process; tetap ke **same** binary
4. **jsonl WAL** — goroutine append (persist, replay on boot)

```
❌ External broker, Redis, Rabbit, MQTT
✅ internal/bus + route.Watcher
```

---

## Arsitektur

```mermaid
flowchart TB
  subgraph core [sapaloq-core — one binary]
    BUS[Bus.Publish]
    RT[RouteMatcher]
    W1[Watcher orchestrator]
    W2[Watcher widget]
    W3[Watcher sub-agent]
    WAL[jsonl goroutine]
    BUS --> RT
    RT --> W1 & W2 & W3
    BUS --> WAL
  end

  SOCK[sapaloq.sock]
  SUBP[sub-agent child]
  DBUS[D-Bus in-proc]

  SUBP --> SOCK --> BUS
  DBUS --> BUS
```

---

## Route watcher (Go sketch)

```go
// sapaloq-core/internal/bus/bus.go

type Bus struct {
    mu       sync.RWMutex
    watchers []*Watcher
    seq      atomic.Uint64
    walPath  string
    hotCache sync.Map          // optional session dedupe
}

func (b *Bus) Watch(id string, patterns []string, buf int) *Watcher
func (b *Bus) Publish(topic, producer string, payload any) (Envelope, error)
```

Publish never blocks on slow consumer — drop + log.

---

## Built-in watchers (registered at `main()`)

| ID | Patterns | Handler |
|----|----------|---------|
| `orchestrator` | `sapaloq.v1.subagent.*`, `sapaloq.v1.gnome.*`, … | Wake loop |
| `widget` | `sapaloq.v1.subagent.progress.*` | Ring HUD |
| `wal` | all | Append `events.jsonl` |

---

## Unix socket (same binary)

```text
~/.config/sapaloq/run/sapaloq.sock
```

Ops: `publish`, `watch`, `unwatch`, `event`, `ping`.

Orchestrator uses in-proc channel — **no socket hop**.

---

## Topics

Prefix `sapaloq.v1` — see prior catalog (subagent.completed, orchestrator.control.{id}, gnome.notification, …).

---

## Config

```json
{
  "events": {
    "bus": {
      "enabled": true,
      "wakeViaBus": true,
      "socketPath": "~/.config/sapaloq/run/sapaloq.sock",
      "watcherBufferSize": 64
    }
  }
}
```

Heartbeat 60s = watchdog only when `wakeViaBus: true`.

---

## Non-goals

- Separate broker process or container
- Redis / MQTT / Rabbit as dependency
- Cross-machine bus (use file export later if needed)

See [RUNTIME.md](./RUNTIME.md) for full single-binary stack.
