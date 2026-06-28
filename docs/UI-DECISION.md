# SapaLOQ - UI Decision (Widget / HUD)

> Locked direction for M5 widget. Supersedes "GTK4 + Layer Shell everywhere" in older drafts.
> Last updated: 2026-06-28 (reader-aware chat auto-scroll during live updates)

**Single binary principle:** `runtime.singleBinary` means **no external broker/daemon** - orchestrator, bus, JSON store, and socket server live in **`sapaloq-core` only**. M5a may build a separate `sapaloq-widget` artifact for spike speed; **production target** is one user-facing install (subcommand `sapaloq-core ui`, embedded Wails in same binary, or launcher script) - not two independent products long-term.

Related: [PLATFORM.md](./PLATFORM.md) · [RUNTIME.md](./RUNTIME.md) · [ORCHESTRATOR.md](./ORCHESTRATOR.md)

---

## Summary

| Layer | Choice |
|-------|--------|
| **UI framework** | **Wails v2** (re-evaluate v3 at M5 kickoff) |
| **Frontend** | Web (Svelte or React) - CSS ring/thinking animations |
| **Backend coupling** | Thin client → `sapaloq.sock`; orchestrator stays in `sapaloq-core` |
| **GNOME Wayland window policy** | Optional **thin GJS shell shim** (`sapaloq-shell@`) - not full UI extension |
| **KDE / Sway / COSMIC** | `gtk-layer-shell` hook on WebKitGTK window (post-M5c adapter) |
| **X11** | Normal floating window; `wmctrl` / EWMH hints optional |

**Not chosen:** full GNOME Shell extension UI, Flutter desktop, React Native Desktop, pure GTK4+gotk4 widget.

---

## Why not Layer Shell on GNOME?

GNOME Shell (Mutter) **does not implement** `zwlr_layer_shell_v1`. Official [gtk4-layer-shell](https://github.com/wmww/gtk4-layer-shell) docs:

> Does not work on X11 or **GNOME on Wayland**.

Layer Shell remains valid for KDE Plasma, wlroots compositors (Sway), COSMIC - not Ubuntu/Pop GNOME MVP target.

Additionally, GTK4 removed `gtk_window_set_keep_above`. GNOME maintainers confirm: **no programmatic always-on-top** from client apps on Wayland ([GNOME Discourse](https://discourse.gnome.org/t/any-way-to-set-window-always-on-top-programmatically/31579)). The "Always on Top" menu action is **Shell-side**, not app-side.

---

## Architecture

```
┌──────────────────────────────────────────────────────────┐
│  sapaloq-widget (Wails - separate binary from core)      │
│  Web UI: ring, chat panel, progress mirror               │
│  Go glue: IPC client → sapaloq.sock                      │
└────────────────────────┬─────────────────────────────────┘
                         │ unix socket
┌────────────────────────▼─────────────────────────────────┐
│  sapaloq-core (single binary)                              │
│  orchestrator · bus · JSON store · sub-agents                │
└──────────────────────────────────────────────────────────┘

GNOME Wayland (optional, M5c):
┌──────────────────────────────────────────────────────────┐
│  sapaloq-shell@ (GJS extension, ~150 LOC)                │
│  window-created → match app_id → Meta.Window.make_above()│
└──────────────────────────────────────────────────────────┘
```

Widget is **not** embedded inside `sapaloq-core` process for M5 - separate `sapaloq-widget` binary keeps core headless-testable and matches `doctor` / no-UI recovery story. Same repo, shared types package optional.

The long-lived `watch` scanner is cancellable: widget shutdown closes its Unix
socket to wake an idle blocking read immediately. Core event writes carry a
five-second write deadline, so a wedged/disconnected webview cannot retain an
IPC stream goroutine indefinitely.

### Background task visibility

The widget keeps one lifecycle card per `task_id`. `pending`, `in_progress`,
`stopping`, `awaiting_clarification`, `done`, `failed`, and `stopped` update the
same card and ring state. On startup/reconnect, IPC `watch` first sends recent
durable task snapshots and then live events, so a completion or failure cannot
disappear merely because the widget was disconnected.

The header includes a compact telemetry rail showing the active model/provider,
live Planner and Agent slots with their current phase, and the effective Ask
session workspace (`session_workspace`). Clicking the WORKSPACE tile opens the
OS-native directory chooser (GTK/Nautilus-style on GNOME; hidden dot-directories
visible); the choice persists per chat session via `workspace_set` IPC. It
refreshes every three seconds and immediately after task
events; the existing task cards remain the detailed lifecycle history.

### Foreground steering while Ask is running

The compose remains editable during a foreground generation. Its amber
`is-steering` state replaces Send with two explicit actions: **Stop** cancels
the existing generation, while **Steer** queues text guidance through the
`chat_steering` IPC operation. Enter sends steering, Shift+Enter remains a
newline, and the placeholder/hint states that guidance is applied after the
current tool batch. When idle, the same Enter gesture and Send button start a
normal chat turn.

Steering v1 is text-only: attachment controls are disabled during a run and a
draft containing an attachment is rejected without clearing it. A queued
message gets a local optimistic `message--steering` bubble and status ack; a
failed enqueue keeps the draft and marks the bubble failed. These bubbles are
UI-only and are not restored from chat history because steering is actor
control input, not a persisted user turn. Background actor targeting and
mid-stream `priority: interrupt` remain follow-ups.

### Reader-aware transcript scrolling

Live transcript patches, direct message/tool appends, and same-session history
refreshes follow the newest content only while the reader is already at the
bottom of the chat. Moving upward disables auto-follow immediately and keeps
the current `scrollTop`; returning to the end enables it again on the next
update. The end check uses a 2px tolerance solely for browser layout rounding.
Initial hydration, a deliberate session switch, and a new/reset chat open at
the newest transcript entry.

### Visual language

The widget uses a dark graphite visual system: near-black shell, layered
gunmetal surfaces, cool silver controls, restrained technical linework, and
compact squared geometry. Graphite is the base rather than the whole palette:
cyan, blue, indigo, magenta, and a small amber accent carry focus, energy, and
state. User messages use a blue-indigo surface, reasoning uses violet, and
active runtime elements use cyan; green, yellow, and red remain semantic status
colors.

The orb and application icon use a network-core visual derived from the supplied
abstract artwork. A luminous blue local core sits inside concentric processing
rings and a cyan/violet orchestration mesh. The original wide composition is
reframed around the center; all source text and branding sit outside the crop
and are not shipped.

The orb uses a tighter circular crop of the same artwork, while the application
icon keeps more of the surrounding mesh. At HUD size, state is carried by core
brightness and ring motion; latency remains internal to ping handling so tiny
rendering cannot clip a partial label.

Icon packaging is platform-specific but derives from the same raster master:
`build/appicon.png` feeds Wails/macOS, `build/windows/icon.ico` contains Windows
multi-resolution sizes, `build/linux/sapaloq.png` is installed into the user
hicolor icon theme, and `frontend/src/assets/images/orb-core.png` feeds the HUD.
Linux live/dev windows also receive the embedded icon bytes directly via
`gtk_window_set_icon`.

That in-window icon is **not** what GNOME Shell shows in the taskbar/dock,
though: GNOME matches a window to a `.desktop` entry by `WM_CLASS` and takes the
icon from there. Wails never sets `WM_CLASS` on Linux (it only calls
`g_set_prgname` + `gtk_window_set_icon`), so the class defaults to the binary
name (e.g. `sapaloq-widget-dev-linux-amd64` under `wails dev`) and no entry
matches - yielding a generic placeholder icon and the dev binary name as the
title. Two pieces fix this:

- **`WM_CLASS = sapaloq`** is set from `cmd/sapaloq-widget/input_shape_linux.go`
  (`g_set_prgname` + `gdk_set_program_class`) before `wails.Run`, alongside the
  existing input-shape CGO. No-op on non-Linux.
- **`build/linux/sapaloq.desktop`** (`Icon=sapaloq`, `StartupWMClass=sapaloq`)
  is installed into `${XDG_DATA_HOME}/applications`. `make run` depends on a
  `desktop-entry` target that seeds the icon + entry, so dev runs already show
  the right taskbar icon; `make install` and `install.sh` install it too (with
  `Exec=` rewritten to the installed widget), and both remove it on uninstall.

The visual change does not alter IPC, streaming states, attachments, runtime
telemetry, or window sizing contracts.

---

## Wails v2 vs v3

| | v2 | v3 (alpha, Jun 2026) |
|---|-----|----------------------|
| Status | Stable | `v3.0.0-alpha.102`; approaching beta |
| Linux stack | GTK3 + WebKit2GTK 4.1 | GTK4 + WebKitGTK 6.0 default (alpha.93+) |
| Multi-window | Limited | First-class |
| Risk | Low | Alpha API/build churn |

**Lock for spike (M5a–M5b):** Wails **v2** - predictable, docs mature.

**Re-evaluate at M5 kickoff:** if v3 is **beta or stable** and GTK4 default is proven on Ubuntu 24.04/26.04, migrate before production widget. Do not block M5a on v3.

Track: [Wails v3 FAQ](https://github.com/wailsapp/wails/discussions/5139), [GTK4 default issue #5459](https://github.com/wailsapp/wails/issues/5459).

---

## GNOME shell shim (`sapaloq-shell@`)

### Scope (minimal)

Extension **only** handles window stacking policy - no UI, no D-Bus business logic in GJS.

```javascript
// Pseudocode - ESM (GNOME 45+)
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
| API still exists? | Yes - core `Meta.Window.make_above()` in Mutter; documented, long-lived |
| Breaks each GNOME major? | **API stable**; breakage is usually **extension packaging** (ESM port GNOME 45, `shell-version` in metadata) |
| Real failure mode | Calling `make_above()` on **destroyed** `Meta.Window` → Shell crash ([pip-on-top #22](https://github.com/Rafostar/gnome-shell-extension-pip-on-top/issues/22)) - fix with validity checks + defer via `Mainloop.idle_add` |

**Mitigation:** ship extension per Shell line (`metadata.json`: `["45","46","47","48"]`); CI smoke on target Ubuntu LTS; never call stacking APIs after `window-unmanaged`.

### Optional vs required

| Mode | Always-on-top | Install |
|------|---------------|---------|
| **M5a/b default** | Not guaranteed - normal floating HUD | Widget only |
| **M5c recommended** | Reliable via shim | Widget + `sapaloq-shell@` |

Document in `doctor`: warn if shim missing and `widget.requireAlwaysOnTop: true`.

---

## Tiered MVP (M5)

| Phase | Goal | Blocks on AOT? |
|-------|------|----------------|
| **M5a** | Wails spike: FAB+popup, IPC, input shape | ✅ Done - see [cmd/sapaloq-widget/](../cmd/sapaloq-widget/) |
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
| **macOS** | Out of scope | - |

---

## Spikes (M5a - completed 2026-06-19)

| # | Result |
|---|--------|
| 1 | Wails v2 frameless + transparent on Ubuntu 24.04 (`-tags webkit2_41`) ✅ |
| 2 | IPC round-trip widget ↔ unix socket < 50ms ✅ |
| 3 | FAB pojok + popup expand/collapse ✅ |
| 4 | GTK circular `input_shape` - click-through outside orb ✅ |

See [M5a spike notes](./development/m5a-spike.md).

Pending: GJS shim prototype (M5c), KDE layer-shell (M5d).

---

## Topbar layout + chat-history switcher (2026-06-25)

The popup header is a **single row** to stay usable at the smallest panel width
(376px). It was previously brand + a tight cluster of usage/conn/resize/close
with no room for new affordances.

- **Left:** brand mark + a **history switcher** (`#btn-history`): clock icon +
  the active session title + a caret. Clicking it opens `#history-menu`, a
  dropdown anchored under the header listing recent sessions (title derived from
  the first user turn, message count + relative time, a green dot on the active
  session) plus a **"Chat baru"** action.
- **Right (compacted):** context-usage pill, the connection indicator reduced to
  a single dot (`.conn-pill` now dot-only), a new-chat icon button, the resize
  cycle button, and close.

Session model: the store keeps a **single active session** (`chat_sessions.active`).
Switching reuses that invariant (`Store.Activate`), "Chat baru" reuses
`Store.Reset`, and the list comes from `Store.ListSessions`. The widget reaches
these via IPC ops `session_list` / `session_switch` / `session_new`
(orchestrator `ListSessions` / `SwitchSession` / `NewSession`). UI detail lives
in `docs/STATUS.md`; files: `ui/template.ts`, `style.css`, `features/history.ts`,
`main.ts`.

---

## Attachments: folder drops + bubble links (2026-06-25)

- **Folders are path-only.** A dropped directory ingests as an attachment that
  carries only its path (`DIR` pill, `[Local folder: <path>]` model pointer). We
  deliberately do **not** read the tree - the model lists/reads it with its own
  tools, avoiding prompt flooding. Same single-path model as native file drops on
  WebKitGTK (GTK delivers the path; `ReadDroppedFile` returns it).
- **Path-backed attachments render as links in the bubble.** Anything with a real
  host path (dropped file or folder) serializes into the *visible* chat bubble as
  a markdown link `[name](path)`, clickable and routed to the file manager via
  `OpenExternal`. Previously the bubble showed the bare name as plain text while
  the composer pill was effectively a link - this is now consistent in both the
  live and restored bubble. Pathless (browser/pasted) attachments keep the bare
  name and surface through the "N attachments" badge. Files:
  `ui/compose.ts` (`serialize`, `attachmentModelBlock`, `pillTag`),
  `features/messages.ts` (`parseTurnContent`), `features/attachments.ts`,
  `app.go` (`ReadDroppedFile`/`OpenAttachment`).
- **Orb layers counter-rotate.** The orb's gradient ring (`.orb-ring`) spins
  clockwise (`ring-spin`); the inner icon (`.orb-art`) now spins
  counter-clockwise (`core-counter-spin`, `rotate(-360deg)`) so the two layers
  read as opposed motion. The thinking-state pulse was moved off `transform`
  (lighting only) so it composes with the spin instead of fighting its rotate.
  `style.css`.
- **Drag overlay is idle-timer driven, not depth-counted.** The "Lepas untuk
  attach file" highlight is shown once on the first `dragover` and cleared by a
  single idle timer (re-armed on each `dragover`); there is intentionally **no**
  `dragenter`/`dragleave` hide path. On WebKitGTK `dragleave` fires on every
  child crossing (frequently `relatedTarget === null`) while a file is merely
  *held* over the widget, which previously flickered the class on/off many times
  a second. `features/drag-overlay.ts`.

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
