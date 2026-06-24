# SapaLOQ - Limitations (No Solution)

> Hal-hal yang **tidak bisa diselesaikan** hanya dengan arsitektur - tradeoff produk, batas OS, atau fisika network.
> Untuk yang **bisa** dimitigasi (boot FSM, offline queue, doctor CLI), lihat [RUNTIME.md](./RUNTIME.md) - **bukan** file ini.
> Last updated: 2026-06-19

Related: [VISION.md](./VISION.md) · [RUNTIME.md](./RUNTIME.md)

---

## Cara baca dokumen ini

| Label | Meaning |
|-------|---------|
| **Hard limit** | Tidak ada fix engineering yang menghilangkan limit; hanya accept + UX honest |
| **By design** | Sengaja dipilih; bukan bug |
| **External** | Di luar kontrol SapaLOQ |

---

## 1. Brain & network

### LLM latency (Hard limit · External)

User kirim chat → first token cloud API **200ms–30s+**. Event bus internal <5ms **tidak** mempercepat bagian ini.

SapaLOQ bisa delegasi async, tapi **companion-feel "instant reply"** tidak realistis dengan cloud-only brain.

### Offline / no connection (Hard limit)

Tanpa LLM (cloud atau local):

- Orchestrator **tidak bisa** reasoning NL
- `/settings` natural language **tidak jalan**
- Clarification auto-answer butuh index saja - **terbatas**
- Research sub-agent **mati total**

Yang masih jalan offline: tulis file (scribe), GNOME tools lokal, baca SQLite - **bukan** companion pintar.

Local LLM fallback = **partial**, bukan parity penuh (quality, RAM, battery).

### API rate limit & cost (External)

Sub-agent agresif + research + multi-turn = biaya & quota provider. SapaLOQ tidak bisa guarantee unlimited usage.

### Connection flap mid-stream (Hard limit · partial)

Stream putus saat generation - user dapat partial response. Retry tidak bisa "unsay" apa yang sudah user baca. Idempotency tool **partial**; conversational continuity **tidak perfect**.

---

## 2. Boot, sleep, downtime

### Proactive gap saat SapaLOQ off (Hard limit)

Notifikasi GNOME, reminder, email event **saat sapaloq-core tidak jalan** → **generally lost**.

OS tidak menyediakan replay buffer unlimited untuk third-party companion. Setelah boot: hanya event **baru**.

Scheduled `delay_start` yang fire saat core mati → **miss** kecuali persist + catch-up on boot (mitigasi partial - lihat RUNTIME; **miss window tetap ada**).

### Login → session ready delay (Hard limit)

Antara power-on dan `graphical-session` + D-Bus + network ready: **5–60+ detik** tanpa companion. Tidak eliminable - urutan boot OS.

### Sleep / suspend (Hard limit)

Lid close / suspend: in-memory state hilang; D-Bus reconnect quirks. Wake resume **partial** - task `running` bisa orphan; full "seolah tidak pernah pause" **tidak guaranteed**.

### Hard power loss (Hard limit · partial)

Crash mid-write: SQLite WAL usually survives; jsonl line bisa corrupt **satu event**. Tidak ada exactly-once semantics tanpa distributed consensus - **overkill** untuk SapaLOQ.

---

## 3. Isolation & product boundaries

### Handoff ≠ shared memory (By design)

Worker (`cursor-agent`, GoClaw) **tidak otomatis** tahu companion memory. User harus explicit handoff packet. **Tidak ada solusi** tanpa melanggar misi isolasi.

Same rule for **outer-machine nodes**: no live memory bus - context packet at spawn only ([NODES.md](./NODES.md#memory-policy-local-vs-remote)).

### Dua otak, dua truth (By design)

Companion ingat desktop/personal; worker ingat repo/coding. **Tidak sync** - user bisa lihat jawaban bertentangan antar produk.

### No full settings UI (By design · mitigated)

Config utama user ada di `~/.config/sapaloq/config.json` (atau path dari `SAPALOQ_CONFIG`). User boleh edit langsung file itu tanpa lewat UI; `config/config.example.json` hanya template repo dan disalin otomatis saat first boot jika `config.json` belum ada. Jangan taruh token/kredensial nyata di `config.example.json` karena file itu aman untuk GitHub.

Perubahan `config.json` di-reload otomatis berdasarkan `mtime` (polling ringan, mirip env watcher Laravel). Untuk switch cepat dari chat, gunakan `/model <provider-key>`; autocomplete mengambil key dari `llmBridge.providers`.

LLM down + config corrupt masih perlu manual edit/CLI doctor. Doctor mitigates **partial**; tidak mengganti UI untuk non-technical user.

---

## 4. GNOME / Linux platform

### GNOME-first, not GNOME-only (scope note)

**MVP adapter:** GNOME on Ubuntu/Pop!/Debian. **Product direction:** portable via [PLATFORM.md](./PLATFORM.md).

KDE, Windows, CentOS desktop = **later adapters** - same core, different `internal/platform` impl.

### Per-adapter limits (Hard limit)

### Notification body incomplete (Hard limit)

Beberapa app: silent notif, empty body, privacy redaction. SapaLOQ **tidak bisa** rangkum apa yang OS tidak expose.

### Wayland / compositor variance (Hard limit)

Always-on-top HUD behavior beda per compositor: **GNOME Wayland** tidak punya layer-shell - pakai GJS shim atau manual toggle; **KDE/Sway** pakai gtk-layer-shell. Multi-monitor & fractional scaling tetap variance. **Click-through** di area transparan: GTK `input_shape` (M5a validated), bukan otomatis di semua toolkit.

### Draggable widget panel positioning on multi-monitor (Hard limit · GNOME/Linux)

Orb kecil yang bisa di-drag lalu berubah menjadi panel besar membutuhkan kombinasi native window **resize + reposition** saat expand/close. Pada GNOME/Linux multi-monitor, hasilnya tidak reliable antar user karena:

- urutan monitor, origin coordinate, dan scaling bisa berbeda (termasuk negative/offset origin);
- window manager/compositor bisa clamp posisi window saat resize;
- GTK/Wails geometry callback bisa transient/asynchronous setelah drag atau resize;
- behavior beda antara X11, Wayland, fractional scaling, dan setup 1/2/3+ monitor.

SapaLOQ **tidak bisa guarantee** panel selalu expand/close tepat dari posisi orb pada semua layout monitor. Mitigasi partial:

- default placement ke satu posisi stabil (mis. kiri bawah monitor aktif);
- hindari resize/reposition native window saat toggle;
- fixed outer transparent window + precise `input_shape` mask supaya area transparan click-through;
- user manual reposition setelah drift.

Semua mitigasi punya tradeoff UX. Ini bukan bug core/orchestrator; ini batas platform desktop/windowing.

### Portal permission prompts (Hard limit)

Screenshot, clipboard first-use → user must approve. Automation **tidak fully silent** on modern Wayland.

### Flatpak sandbox vs full D-Bus (Hard limit)

Sandboxed SapaLOQ = **less** GNOME access. Full power = native package - tradeoff security vs capability; **keduanya tidak perfect**.

---

## 5. Architecture tensions (inherent)

### Strict anti context poisoning vs fast task switch (By design)

User loncat task cepat → orchestrator block/park. **UX friction intentional** - tidak bisa "poisoning-free" dan "zero friction switch" sekaligus.

### Orchestrator slim vs full awareness (Hard limit)

Orchestrator sengaja tidak pegang full history. User tanya "detail thinking sub-agent 10 menit lalu" → **bounded** tail N events; full replay **tidak** (anti poisoning).

### Single binary SPOF (Hard limit · partial)

Satu crash = widget + bus + orchestrator mati. systemd restart mitigates **partial**; **tidak** zero-downtime.

### SQLite write concurrency (Hard limit · partial)

Many sub-agents writing facts concurrently → single-writer bottleneck. Queue writer mitigates **partial**; **bukan** infinite scale.

### Remote sub-agent nodes (Hard limit · partial)

Node di VPS/EC2 = network partition, latency, trust boundary. Progress/clarification over WS **partial**; offline node = spawn fail (fallback local optional).

### Shared memory across outer-machine nodes (Hard limit · **not recommended**)

**Tidak rekomendasikan** sync `companion.db` / memory bus ke node di **mesin lain** dari orchestrator.

| Problem | Why no simple fix |
|---------|-------------------|
| **Latency** | Every fact read/write = RTT; prefetch & anti-deep-check collapse |
| **Stale memory** | Remote cache diverges; orchestrator reads truth A, remote acts on B |
| **Conflict** | Concurrent writes across machines without distributed consensus |
| **Complexity** | CRDT/sync/replication = product baru - violates single-binary less-deps |

**Policy:** shared memory bus = **same machine only** (`wrapper: local`, same-host Docker optional with shared volume - still risky, default off).

Remote nodes receive **scoped context packet only** at spawn + return **results/progress** - tidak live query ke SQLite orchestrator. Learning promotion happens **local** after task done.

See [NODES.md](./NODES.md#memory-policy-local-vs-remote).

---

## 6. Intelligence & expectations

### Auto-learning quality (Hard limit)

Learning dari interaksi + web **bisa salah**, outdated, atau contradict user. Obsolete/mark helps **partial**; **tidak guarantee** agent selalu benar over time.

---

### Companion ≠ human assistant (Hard limit)

Tidak ingat nuance tanpa explicit memory; tidak infer intent perfect; tidak proactive dengan social intelligence penuh.

### Research from web (Hard limit)

Sumber salah, SEO spam, outdated docs. `requireSourceUrl` helps audit **partial**; **truth tidak guaranteed**.

### Bandit / prefetch tuning (Hard limit)

Needs samples; cold start = bad routing. **Tidak instant optimal** dari hari pertama.

### LLM bridge format drift (Hard limit · partial)

Tool/thinking wire formats differ per provider ([BRIDGE.md](./BRIDGE.md)). Cursor L0 truth: [RE-CURSOR-THINKING-TOOLS.md](./RE-CURSOR-THINKING-TOOLS.md). Wrong parser = lost or hallucinated tool calls.

### Tool poisoning (Backend-dependent)

Cursor-like backends may emit **fake tool names** - requires coercion layer. OpenAI/Claude direct APIs usually clean. Gemini/Copilot paths **may** need cursor-like sanitizer - probe at connect, not assume.

---

## 7. Explicit non-solutions (jangan chase)

| Idea | Why no |
|------|--------|
| Redis/Rabbit for reliability | User chose single binary - different failure profile, not elimination of network/offline limits |
| Sync companion memory to cursor-agent | Violates isolation mission |
| Shared SQLite memory bus across remote nodes | Stale memory + sync complexity - use context packet instead |
| Replay all missed OS notifications after boot | OS/API does not provide |
| Zero LLM latency | Physics |
| Settings UI "just in case" | Violates config-by-agent mission unless scope change |
| Full Cursor ToolCall parity on companion | Explicit non-goal |
| Require 9router as runtime dependency | Built-in compat bridges instead |
| Derive Cursor thinking from 9router transport | Skips thinking channel - see RE-CURSOR-THINKING-TOOLS.md |
| One universal tool/thinking parser | Formats differ - per-provider parsers required |

---

## 8. Honest UX contract (what we tell user)

SapaLOQ **is**:

- Local-first runtime, isolated companion, GNOME desktop buddy
- Smart **when** brain available; useful offline **only** for bounded local actions
- Proactive **only while running**

SapaLOQ **is not**:

- Always-aware while laptop off
- Guaranteed instant cloud replies
- Perfect memory without explicit capture
- Replacement for IDE agent

---

## Cross-reference

| Topic | Mitigations (partial) |
|-------|----------------------|
| Boot recovery, offline queue, doctor | [RUNTIME.md](./RUNTIME.md) |
| Event wake speed (when core running) | [EVENT-BUS.md](./EVENT-BUS.md) |
| Task stack, clarification | [ORCHESTRATOR.md](./ORCHESTRATOR.md) |

---

## Open product decisions (limit shape, not remove)

- [ ] Local LLM fallback scope (how dumb is offline OK?)
- [ ] How aggressive proactive catch-up on boot (accept miss vs fake catch-up)
- [ ] Minimum CLI/doctor for no-UI recovery

Perubahan scope di atas **bisa mengurangi** beberapa limit - tapi menambah misi baru.
