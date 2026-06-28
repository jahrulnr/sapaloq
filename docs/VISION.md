# SapaLOQ - Visi & Misi

> Dokumen anchor proyek. Tulis ulang hanya kalau arah produk berubah.
> Last updated: 2026-06-19 (platform abstraction - GNOME first adapter)

---

## Visi

**SapaLOQ** adalah companion desktop **cross-platform** (Linux multi-DE dulu, Windows later) - floating HUD di pojok layar, memori & otak sendiri, otomasi lokal via **platform adapter**, dan **tidak** tercampur dengan agent coding (`cursor-agent`, GoClaw, 9router).

**Sekarang:** kamu pakai GNOME → adapter `gnome` first. **Bukan** produk GNOME-only - lihat [PLATFORM.md](./PLATFORM.md).

User merasakan *presence* agent di desktop (ring animasi, thinking, tool feedback) tanpa membuka IDE atau terminal - sambil tetap bisa **handoff** ke worker agent kalau butuh coding berat.

Referensi visual: ilustrasi HUD ring + avatar (astronaut/orbit), neon blue, draggable, always-on-top.

---

## Misi

1. **Isolasi penuh** dari agent-cli - bukan wrapper, bukan subprocess default, bukan shared session.
2. **Modular drivers (Go)** - **platform** detect OS/distro/DE → `os.json` ([DRIVER.md](./DRIVER.md)); **LLM bridge** `cursor-bridge` + compat APIs ([BRIDGE.md](./BRIDGE.md)). GNOME platform driver first.
3. **Desktop MCP optional** - per-adapter backend; bukan dependency global SapaLOQ.
4. **Memory terpisah** - companion ingat hal desktop/personal; tidak merge dengan agent transcript.
5. **Schema-driven UI** - animasi/icon tool dari kontrak tool (fork ToolCall spec), bukan hardcode.
6. **Companion-feel dulu** - ngobrol, remind, automate desktop; coding = explicit handoff.
7. **Orchestrator-only widget** - agent di widget hanya koordinasi; pekerjaan ke sub-agent (local shared memory; remote = context packet only).
8. **Config-by-agent** - tidak ada settings UI; `/settings ...` → sub-agent edit `config.json`.
9. **Context SOP** - index-first prefetch, dynamic system-prompt, anti-deep-check, auto-learning - tidak "lupa" saat compaction.
10. **Role prompts & builders** - sub-agent spawn dengan system-prompt per role; post-task learning-agent + optional web research.
11. **Single binary runtime** - satu `sapaloq-core` Go; goroutine + JSON store + jsonl; zero Redis/Rabbit/MQTT deps.
12. **Less dependency** - modular drivers + cached `os.json` ([DRIVER.md](./DRIVER.md)).
13. **Sub-agent nodes** - local or remote (Docker/VPS/EC2/SSH); JSON registry (`nodes.json`) + comm spec ([NODES.md](./NODES.md)).

---

## Bukan scope (explicit non-goals)

- Reimplement protobuf Cursor (`aiserver.v1`, `agent.v1`) di widget.
- Replace cursor-agent / IDE / 9router sebagai coding brain.
- Shared memory dengan `~/.cursor/`, acp-sessions, atau agent jsonl.
- Full 49 ToolCall handlers di companion (cloud worker tools).
- Parity Cursor IDE (browser automation, computer-use, PR tools, dll.).
- Settings panel / preferences UI - semua config via agent.
- External message broker / Redis as runtime dependency for SapaLOQ.

See [LIMITATIONS.md](./LIMITATIONS.md) for **hard limits with no engineering solution** (offline brain, missed events while down, LLM latency, etc.).

---

## Prinsip arsitektur

### Orchestrator + sub-agent (core runtime)

Widget agent = **orchestrator saja** - assign task → sub-agent dengan **context packet**; memory bus shared **local nodes only**.

| Role | Fungsi |
|------|--------|
| **orchestrator** | Route intent, **spawn path score** (Plan vs Agent), control sub-agents; **Ask mode** |
| **settings** | Edit `config.json` dari `/settings ...` |
| **scribe** | Tulis ke `storage.paths` by mode/intent ("catat ini") |
| **planner** | **Plan mode** - read-only; Markdown `plan.md` sebelum eksekusi |
| **task-runner** | **Agent mode** - **full access**; eksekusi task yang sudah dirancang Ask/Plan |
| **context-scaler** | Minimal context per task - **anti poisoning** |
| **boundary-guard** | personal / hobby / work boundary |
| **memory-janitor** | Auto: rapihin memory, dedupe, naik/turun context |
| **intent-router** | Classify intent → prefetch dari JSON index sebelum spawn |
| **learning-agent** | Post-task prompt overlay + skills builder |
| **research** | Web best practice (async) |
| **event-watcher** | Platform notification + custom reminder → event bus |

Detail: [ORCHESTRATOR.md](./ORCHESTRATOR.md) · [PLATFORM.md](./PLATFORM.md) · [CONTEXT-SOP.md](./CONTEXT-SOP.md) · [PROMPT-BUILDER-SOP.md](./PROMPT-BUILDER-SOP.md)

### Context SOP (anti lupa & anti deep-check)

Saat prompt masuk → **ingress pipeline**: intent-router → JSON fact prefetch → dynamic system-prompt → context packet. Tidak grep/skills dump dulu.

Compaction/low context → **reload from index**, bukan replay transcript. memory-janitor + learning queue = auto-learning over time.

### Progress streaming & completion

Orchestrator **watch live** sub-agent progress (thinking, response, toolcall, todo, in_progress/done) via `progress/<subAgentId>.jsonl` - **tanpa blocking**.

Sub-agent **bisa pause** (`awaiting_clarification`) saat tanya orchestrator/user - lihat [ORCHESTRATOR.md](./ORCHESTRATOR.md#clarification-loop-sub-agent--orchestrator--user).

Sub-agent selesai → **event trigger** (`events.jsonl`) + **heartbeat fallback** untuk stale detection.

### Event watching (proactive)

GNOME/KDE/… notifications (via adapter), custom reminders → unified `events.jsonl` → orchestrator react async.

### Config-by-agent (no settings UI)

```
/settings matiin read notification
  → sub-agent:settings
  → config.json notifications.read = false
```

Schema: [config.schema.json](../schema/config.schema.json) · Example: [config.example.json](../config/config.example.json)

### Storage & apps mapping

Indexed `storage.paths` + `storage.intents` + `apps.entries` - agent tahu **mana file/app** untuk personal vs work vs hobby.

---

## Prinsip arsitektur (isolasi)

### Dua produk, satu bridge opsional

| | **SapaLOQ (Companion)** | **Worker (Agent CLI)** |
|--|-------------------------|-------------------------|
| Feel | Ambient HUD, ngobrol santai | Task execution, coding |
| Brain | Companion sendiri via **LLM bridge driver** ([BRIDGE.md](./BRIDGE.md)) - local llama or API | `cursor-agent` / GoClaw |
| Memory | `~/.config/sapaloq/` | `~/.cursor/`, cursor-agent sessions |
| MCP | Desktop adapter (notify, window, …) | git, fs, terminal, repo |
| Rules | `companion.md`, automation rules | `.cursor/rules`, AGENTS.md |
| Default | Always-on widget | Invoke on explicit handoff |

### Hard boundaries

1. Widget **tidak** spawn `agent` di main loop.
2. **Tidak** shared memory store/jsonl antara companion dan worker.
3. **Tidak** shared MCP config file.
4. Handoff ke worker = **aksi eksplisit** user (“Open in Agent”), bukan implicit.
5. `cursor-agent-toolcall-spec` = **referensi visual + mirror**; bukan runtime dependency companion.

---

## Product surface (MVP → later)

### Floating widget

- Icon + interactive animated ring (idle, thinking, tool-active, blocked, error).
- Draggable; posisi persist `~/.config/sapaloq/widget/position.json`.
- Always-on-top; non-modal; tidak block desktop.

### Click → panel

- Chat companion (bukan agent transcript).
- Mode companion (bukan clone `--mode` cursor-agent 1:1):
  - **Chat** - ngobrol + desktop automation.
  - **Automate** - fokus GNOME actions (notify, screenshot, focus window).
  - **Handoff** - siapkan packet ke worker agent (prompt + cwd), buka terpisah.

### States (ring animator)

| State | Trigger |
|-------|---------|
| idle | tidak ada aktivitas |
| thinking | companion brain streaming |
| tool-active | extended tool jalan (gnome_*) |
| interactive | butuh input user (confirm, pilih window) |
| blocked | hook deny / policy |
| mirror-worker | optional: listen worker events tanpa ingest memory |

### Desktop automation (portable tools)

Adapter implements; core exposes uniform tools:

- `desktop_notify`
- `desktop_screenshot`
- `desktop_focus_window`
- `desktop_clipboard`

GNOME MVP via D-Bus + portal - **not** Shell extension required. Optional: gnome-desktop-mcp as adapter backend.

Via config `mcp/servers.json` only when adapter delegates externally - **config terpisah** from Cursor MCP.

---

## Yang perlu di-reverse-engineer (focused)

| Area | Target | Untuk SapaLOQ |
|------|--------|---------------|
| **Rules** | Format context injection Cursor | Design `companion.md` + automation rules sendiri |
| **Hooks** | Pattern `hooks-exec` (fail-closed, pre-step) | Hook runner sapaloq; **config terpisah** |
| **Extended tools** | Platform adapter + portal APIs | Core companion capability |
| **ToolCall spec** | 49 variants CLI | Icon/category map; worker mirror UI only |

**Skip RE untuk SapaLOQ:** agent-cli auth, protobuf encode, 9router transport (pattern only - built-in compat bridges, not third-party dep).

**Adopt as driver contract:** cursor-bridge schema, tool/thinking parsers - see [BRIDGE.md](./BRIDGE.md).

---

## Artefak terkait (sudah ada)

| File | Role |
|------|------|
| `/apps/workspace/cursor-agent-toolcall-index.json` | Ringkas 49 ToolCall variants - input fork schema |
| `/apps/workspace/cursor-agent-toolcall-spec.json` | Full protobuf field reference |
| `/apps/workspace/scripts/extract-cursor-agent-toolcall-spec.py` | Regenerator spec dari CLI bundle |

| `docs/ORCHESTRATOR.md` | Orchestrator, sub-agents, anti-poisoning |
| `docs/CONTEXT-SOP.md` | Anti-forget, dynamic prompt, JSON index, auto-learning |
| `schema/config.schema.json` | Config contract |
| `config/config.example.json` | Bootstrap config |

Planned:

- `sapaloq-tools.schema.json` - extended GNOME tools + mirror map
- `sapaloq-core` event schema + task stack persistence

---

## Layout config (target)

```text
~/.config/sapaloq/
  config.json                 # agent-editable (NO settings UI)
  os.json                     # auto-generated: driver + fingerprint (see DRIVER.md)
  cache/                      # os.json backups on rescan
```
  prompt/
    core.md                   # orchestrator SOP (always tiny)
    roles/                    # per sub-agent role base templates
    roles.d/                  # auto overlays from learning-agent
    modes/                    # personal, hobby, work slices
    positive/ negative/       # FEEDBACK-SOP behavioral slices
    slices/                   # conditional dynamic templates
  skills/                     # sapaloq-local skills (indexed)
  memory/
    facts.json                # JSON memory index (legacy companion.db migrated on boot)
    files/                    # optional markdown mirror per namespace
    learning-queue.jsonl      # auto-learning events
    tasks/                    # task stack (anti context poisoning)
    context-packets/          # ephemeral per-task context
    progress/                 # sub-agent progress streams (*.jsonl)
    control/                  # orchestrator lifecycle commands per sub-agent
    events.jsonl              # GNOME + custom + internal event bus
  rules/
    companion.md
    automation.md
  mcp/
    servers.json              # GNOME MCP only
  widget/
    position.json             # UI chrome only (not agent settings)
    theme.json
  bridge/
    cursor-bridge.schema.json   # synced from cursor-bridge monorepo at build
    handoff/
      <uuid>.json
```

Worker tetap: `~/.cursor/`, `~/.local/share/cursor-agent/` - **zero overlap**.

---

## Handoff protocol (sketch)

Companion **tidak** merge memory ke agent. Handoff packet:

```json
{
  "id": "uuid",
  "createdAt": "ISO8601",
  "prompt": "user intent",
  "cwd": "/path/to/project",
  "attachments": [],
  "source": "sapaloq",
  "consumeOnce": true
}
```

Worker dibuka terpisah (terminal `agent`, IDE, GoClaw). Widget boleh **mirror** state visual (ring) tanpa simpan transcript worker ke memory index.

---

## Tech stack

| Layer | Direction |
|-------|-----------|
| UI | Wails v2 + web FAB+popup - [UI-DECISION.md](./UI-DECISION.md); M5a spike ✅ |
| Core | **sapaloq-core** Go - portable |
| Platform | [PLATFORM.md](./PLATFORM.md) adapters: gnome → kde → windows |
| Memory | JSON files |
| IPC / events | [RUNTIME.md](./RUNTIME.md) · [EVENT-BUS.md](./EVENT-BUS.md) |

Optional on dev machine: `gnome-desktop-mcp` as gnome adapter backend - not global runtime dep.

---

## Roadmap singkat

| Phase | Deliverable |
|-------|-------------|
| **M0** | VISION + ORCHESTRATOR + CONTEXT-SOP + config.schema ✅ |
| **M1** | JSON index + `nodes.json` + local-default bootstrap |
| **M2** | Orchestrator task stack + progress streaming |
| **M3** | Completion triggers + event bus + platform watcher (gnome) |
| **M4** | sub-agent:scribe + storage mapping |
| **M5** | Floating widget (Wails FAB+popup) + progress mirror on ring - M5a spike ✅ |
| **M6** | context-scaler + memory-janitor auto-spawn |
| **M7** | Desktop tools (platform adapter) + handoff worker |
| **M8** | LLM bridge: `openai-compat` + `parse/tools` + `parse/thinking` |
| **M9** | `cursor-bridge` driver + coercion + community bridge template |

---

## Keputusan terbuka (TBD)

- [ ] Default `llmBridge.driver`: **`cursor-bridge`** primary; `local-llama` fallback only - see [BRIDGE.md](./BRIDGE.md).
- [ ] Nama final: lihat [NAME-RECOMMENDATIONS.md](./NAME-RECOMMENDATIONS.md).
- [ ] Worker mirror: listen stream-json agent atau purely visual idle.
- [ ] systemd user unit untuk sapaloq-core atau manual start.
- [ ] GJS `sapaloq-shell@` shim untuk guaranteed AOT di GNOME (M5c).

---

## One-liner (elevator pitch)

> **SapaLOQ** - portable desktop companion (HUD + memory + platform adapter), GNOME first - handoff opsional ke coding agent.

---

## Konteks percakapan (anti-compaction)

Proyek ini lahir dari reverse engineering Cursor agent tools:

- 9router / `aiserver.v1` = transport **pattern reference** only - SapaLOQ ships built-in compat bridges, **bukan** third-party dep.
- **cursor-bridge** = first-class **LLM bridge driver** (tool poisoning, coercion) - see [BRIDGE.md](./BRIDGE.md).
- **Cursor thinking/tools L0 RE** - [RE-CURSOR-THINKING-TOOLS.md](./RE-CURSOR-THINKING-TOOLS.md); 9router bukan truth untuk thinking.
- `cursor-agent` CLI + `agent.v1.ToolCall` oneof (49 variants) = referensi kontrak tool.
- IDE vs CLI ToolCall union **identik** (49); SapaLOQ **sengaja pisah** dari keduanya.
- Widget ilustrasi = mission control HUD di client, bukan chat bubble generic.
- User insist: **jangan campur** agent work dengan companion-feel → arsitektur dua produk + bridge.
- Config **tanpa UI** - agent edit `config.json` via `/settings`.
- Widget agent = **orchestrator only**; sub-agent agresif, **local** shared memory, remote context packet only, boundary/mode aware, anti context poisoning.
- **Context SOP** - JSON index + prefetch + dynamic prompt; anti "lupa" & "deep check" saat compaction.
