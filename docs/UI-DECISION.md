# SapaLOQ — UI Decision (Widget / HUD)

> Locked direction for M5 widget. Supersedes "GTK4 + Layer Shell everywhere" in older drafts.
> Last updated: 2026-06-19 (M5a spike synced)

**Single binary principle:** `runtime.singleBinary` means **no external broker/daemon** — orchestrator, bus, SQLite, and socket server live in **`sapaloq-core` only**. M5a may build a separate `sapaloq-widget` artifact for spike speed; **production target** is one user-facing install (subcommand `sapaloq-core ui`, embedded Wails in same binary, or launcher script) — not two independent products long-term.

Related: [PLATFORM.md](./PLATFORM.md) · [RUNTIME.md](./RUNTIME.md) · [ORCHESTRATOR.md](./ORCHESTRATOR.md)

---

## Summary

| Layer | Choice |
|-------|--------|
| **UI framework** | **Wails v2** (re-evaluate v3 at M5 kickoff) |
| **Frontend** | Web (Svelte or React) — CSS ring/thinking animations |
| **Backend coupling** | Thin client → `sapaloq.sock`; orchestrator stays in `sapaloq-core` |
| **GNOME Wayland window policy** | Optional **thin GJS shell shim** (`sapaloq-shell@`) — not full UI extension |
| **KDE / Sway / COSMIC** | `gtk-layer-shell` hook on WebKitGTK window (post-M5c adapter) |
| **X11** | Normal floating window; `wmctrl` / EWMH hints optional |

**Not chosen:** full GNOME Shell extension UI, Flutter desktop, React Native Desktop, pure GTK4+gotk4 widget.

---

## Why not Layer Shell on GNOME?

GNOME Shell (Mutter) **does not implement** `zwlr_layer_shell_v1`. Official [gtk4-layer-shell](https://github.com/wmww/gtk4-layer-shell) docs:

> Does not work on X11 or **GNOME on Wayland**.

Layer Shell remains valid for KDE Plasma, wlroots compositors (Sway), COSMIC — not Ubuntu/Pop GNOME MVP target.

Additionally, GTK4 removed `gtk_window_set_keep_above`. GNOME maintainers confirm: **no programmatic always-on-top** from client apps on Wayland ([GNOME Discourse](https://discourse.gnome.org/t/any-way-to-set-window-always-on-top-programmatically/31579)). The "Always on Top" menu action is **Shell-side**, not app-side.

---

## Architecture

```
┌──────────────────────────────────────────────────────────┐
│  sapaloq-widget (Wails — separate binary from core)      │
│  Web UI: ring, chat panel, progress mirror               │
│  Go glue: IPC client → sapaloq.sock                      │
└────────────────────────┬─────────────────────────────────┘
                         │ unix socket
┌────────────────────────▼─────────────────────────────────┐
│  sapaloq-core (single binary)                              │
│  orchestrator · bus · SQLite · sub-agents                │
└──────────────────────────────────────────────────────────┘

GNOME Wayland (optional, M5c):
┌──────────────────────────────────────────────────────────┐
│  sapaloq-shell@ (GJS extension, ~150 LOC)                │
│  window-created → match app_id → Meta.Window.make_above()│
└──────────────────────────────────────────────────────────┘
```

Widget is **not** embedded inside `sapaloq-core` process for M5 — separate `sapaloq-widget` binary keeps core headless-testable and matches `doctor` / no-UI recovery story. Same repo, shared types package optional.

---

## Wails v2 vs v3

| | v2 | v3 (alpha, Jun 2026) |
|---|-----|----------------------|
| Status | Stable | `v3.0.0-alpha.102`; approaching beta |
| Linux stack | GTK3 + WebKit2GTK 4.1 | GTK4 + WebKitGTK 6.0 default (alpha.93+) |
| Multi-window | Limited | First-class |
| Risk | Low | Alpha API/build churn |

**Lock for spike (M5a–M5b):** Wails **v2** — predictable, docs mature.

**Re-evaluate at M5 kickoff:** if v3 is **beta or stable** and GTK4 default is proven on Ubuntu 24.04/26.04, migrate before production widget. Do not block M5a on v3.

Track: [Wails v3 FAQ](https://github.com/wailsapp/wails/discussions/5139), [GTK4 default issue #5459](https://github.com/wailsapp/wails/issues/5459).

---

## GNOME shell shim (`sapaloq-shell@`)

### Scope (minimal)

Extension **only** handles window stacking policy — no UI, no D-Bus business logic in GJS.

```javascript
// Pseudocode — ESM (GNOME 45+)
import Meta from 'gi://Meta';

const APP_ID = 'sapaloq-widget'; // match Wails app_id / WM_CLASS

export default class SapaLOQShellExtension extends Extension {
  enable() {
    this._handler = global.display.connect('window-created', (_d, win) => {
      if (!win || !win.get_wm_class()?.toLowerCase().includes(APP_ID)) return;
      if (win.is_fullscreen()) return;
      win.make_above();
    });
  }
  disable() {
    global.display.disconnect(this._handler);
  }
}
```

### `make_above()` stability (GNOME 45→48)

| Question | Answer |
|----------|--------|
| API still exists? | Yes — core `Meta.Window.make_above()` in Mutter; documented, long-lived |
| Breaks each GNOME major? | **API stable**; breakage is usually **extension packaging** (ESM port GNOME 45, `shell-version` in metadata) |
| Real failure mode | Calling `make_above()` on **destroyed** `Meta.Window` → Shell crash ([pip-on-top #22](https://github.com/Rafostar/gnome-shell-extension-pip-on-top/issues/22)) — fix with validity checks + defer via `Mainloop.idle_add` |

**Mitigation:** ship extension per Shell line (`metadata.json`: `["45","46","47","48"]`); CI smoke on target Ubuntu LTS; never call stacking APIs after `window-unmanaged`.

### Optional vs required

| Mode | Always-on-top | Install |
|------|---------------|---------|
| **M5a/b default** | Not guaranteed — normal floating HUD | Widget only |
| **M5c recommended** | Reliable via shim | Widget + `sapaloq-shell@` |

Document in `doctor`: warn if shim missing and `widget.requireAlwaysOnTop: true`.

---

## Tiered MVP (M5)

| Phase | Goal | Blocks on AOT? |
|-------|------|----------------|
| **M5a** | Wails spike: FAB+popup, IPC, input shape | ✅ Done — see [cmd/sapaloq-widget/](../cmd/sapaloq-widget/) |
| **M5b** | Production widget: chat, progress mirror, position persist | No |
| **M5c** | `sapaloq-shell@` + `doctor` check | Only if product requires guaranteed AOT |
| **M5d** | KDE/Sway layer-shell adapter | No (GNOME path independent) |

**Priority:** validate **companion feel** (animation, IPC latency, ring states) before window stacking perfection.

---

## Platform matrix (updated)

| Platform | UI | Window policy |
|----------|-----|---------------|
| **GNOME Wayland** (MVP) | Wails + web | Shell shim `make_above()` or user manual toggle |
| **GNOME X11** | Wails + web | EWMH `_NET_WM_STATE_ABOVE` optional |
| **KDE Plasma Wayland** | Wails + web | gtk-layer-shell hook (`Layer::Overlay`) |
| **Sway / wlroots** | Wails + web | gtk-layer-shell |
| **Windows** (later) | Wails + web | WebView2 always-on-top APIs |
| **macOS** | Out of scope | — |

---

## Spikes (M5a — completed 2026-06-19)

| # | Result |
|---|--------|
| 1 | Wails v2 frameless + transparent on Ubuntu 24.04 (`-tags webkit2_41`) ✅ |
| 2 | IPC round-trip widget ↔ unix socket < 50ms ✅ |
| 3 | FAB pojok + popup expand/collapse ✅ |
| 4 | GTK circular `input_shape` — click-through outside orb ✅ |

See [M5a spike notes](./development/m5a-spike.md).

Pending: GJS shim prototype (M5c), KDE layer-shell (M5d).

---

## Non-goals (UI)

- Full GNOME Shell extension as primary UI (rewrite cost, GJS animation pain)
- Layer Shell as GNOME MVP requirement
- Embedding webview inside `sapaloq-core` main process (M5)
- Flutter / React Native Desktop widget

---

## Open items

| Item | When |
|------|------|
| Frontend framework | Vanilla TS (spike); Svelte/React optional M5b |
| `app_id` / WM_CLASS for GJS shim | Lock before M5c |
| Wails v3 migration | M5 kickoff gate |
| `setExpanded` + drag race | Fix M5b |
| Product name | [NAME-RECOMMENDATIONS.md](./NAME-RECOMMENDATIONS.md) |
