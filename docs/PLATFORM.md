# SapaLOQ - Platform Abstraction

> **Modular platform drivers** + cached **`os.json`**. LLM bridges: [BRIDGE.md](./BRIDGE.md). See [DRIVER.md](./DRIVER.md) - supersedes adapter naming in older drafts.
> Last updated: 2026-06-19

Related: [VISION.md](./VISION.md) · [RUNTIME.md](./RUNTIME.md) · [LIMITATIONS.md](./LIMITATIONS.md)

---

## Prinsip

```
┌─────────────────────────────────────────────────┐
│  sapaloq-core (portable)                        │
│  orchestrator · bus · SQLite · widget IPC       │
├─────────────────────────────────────────────────┤
│  internal/platform.Desktop (interface)          │
├──────────┬──────────┬──────────┬──────────────┤
│  gnome   │  kde     │  freedesktop │  windows  │
│  (MVP)   │  (later) │  (linux fb)  │  (later)  │
└──────────┴──────────┴──────────┴──────────────┘
```

| Layer | GNOME-specific? |
|-------|-----------------|
| Orchestrator, task stack, sub-agents | ❌ |
| Event bus, jsonl, SQLite | ❌ |
| Config, skills, memory SOP | ❌ |
| Widget shell (GTK/Layer Shell) | ⚠️ Linux-first; abstract later - see [UI-DECISION.md](./UI-DECISION.md) |
| **Desktop automation** | ✅ per adapter |
| **Notification watch** | ✅ per adapter (some share freedesktop) |
| **Window/focus/screenshot** | ✅ per adapter |

**Less dependency** = tidak hardcode `gnome-shell` extension sebagai requirement runtime. Extension/MCP = **optional backend** untuk adapter `gnome`, bukan core.

---

## `Desktop` interface (Go sketch)

```go
// internal/platform/desktop.go

type Info struct {
    OS           string // linux, windows, darwin
    DE           string // gnome, kde, cinnamon, ...
    Session      string // wayland, x11, windows
    AdapterID    string // gnome-v1, kde-v1, ...
    Capabilities CapabilitySet
}

type Capability string

const (
    CapNotify      Capability = "notify"
    CapNotifyWatch Capability = "notify.watch"
    CapScreenshot  Capability = "screenshot"
    CapWindowList  Capability = "window.list"
    CapWindowFocus Capability = "window.focus"
    CapClipboard   Capability = "clipboard"
    CapDND         Capability = "dnd"
    CapTray        Capability = "tray"
)

type Desktop interface {
    Info() Info
    Capabilities() []Capability

    NotifySend(ctx context.Context, n Notification) error
    NotifyWatch(ctx context.Context) (<-chan NotificationEvent, error)

    Windows(ctx context.Context) ([]Window, error)
    FocusWindow(ctx context.Context, id string) error

    Screenshot(ctx context.Context, opts ScreenshotOpts) ([]byte, error)

    ClipboardRead(ctx context.Context) (string, error)
    ClipboardWrite(ctx context.Context, text string) error

    DNDEnabled(ctx context.Context) (bool, error)
}
```

Orchestrator & tools call **`desktop.*`** - never import GNOME types in core.

---

## Adapters (roadmap)

| Adapter | OS / DE | MVP | Backend (less deps first) |
|---------|---------|-----|---------------------------|
| **gnome** | Linux GNOME / Ubuntu / Pop! | ✅ Phase 1 | D-Bus: `org.freedesktop.Notifications`, portal screenshot; optional gnome-desktop-mcp |
| **freedesktop** | Linux generic | Phase 2 | Notifications D-Bus; X11/Wayland via libportal / wl-clipboard |
| **kde** | Linux KDE Plasma | Phase 3 | KWin D-Bus, Plasma notifications |
| **windows** | Windows 10/11 | Phase 4 | WinRT toast, Win32 window enum |
| **headless** | SSH / no DE | Optional | Notify watch off; file + timer only |

### Auto-detect (`platform.adapter: auto`)

```text
1. $XDG_CURRENT_DESKTOP, $DESKTOP_SESSION, /etc/os-release
2. Try probe: gnome → kde → freedesktop
3. First adapter with minimum Capabilities for config
4. Else headless + log warning
```

Pop!_OS, Debian GNOME, CentOS Stream GNOME → **`gnome` adapter** (same DE family, test matrix differs).

KDE Neon, Kubuntu → **`kde` adapter** when implemented.

---

## GNOME today ≠ GNOME forever

### Phase 1 (now) - GNOME adapter without Shell extension dependency

| Capability | Preferred | Avoid as hard dep |
|------------|-----------|-------------------|
| Watch notifications | D-Bus `org.freedesktop.Notifications` (Signal/New) | GNOME Shell extension |
| Send notification | same bus | |
| Screenshot | xdg-desktop-portal | Shell-only API |
| Window list/focus | gnome-desktop-mcp **or** Meta/Wayland when available | Import `resource:///org/gnome/shell/...` |
| DND | gsettings / portal | Shell internal |

`desktop-automation@gnomemcp.github.io` = **accelerator** on your machine, not SapaLOQ runtime requirement.

### Event bus topics (platform-neutral)

Prefer:

```text
sapaloq.v1.platform.notification
sapaloq.v1.platform.focus.changed
```

Legacy alias (GNOME era docs): `sapaloq.v1.gnome.notification` → same handler, deprecated name.

Config watchers:

```json
{ "source": "platform.notification", "adapter": "any" }
```

---

## Tools naming

| Old (GNOME-tied) | New (portable) |
|------------------|----------------|
| `gnome_notify` | `desktop_notify` |
| `gnome_screenshot` | `desktop_screenshot` |
| `gnome_focus_window` | `desktop_focus_window` |
| `gnome_*` in sub-agent allowlist | `desktop_*` |

Role `task-runner` tools: `desktop_*` with capability check - if `CapScreenshot` missing, orchestrator says "not supported on this adapter".

---

## Widget UI (honest scope)

> Full decision: [UI-DECISION.md](./UI-DECISION.md)

| Platform | Widget UI | Window policy (always-on-top) |
|----------|-----------|-------------------------------|
| **GNOME Wayland** (MVP) | Wails v2 + web frontend | Thin GJS shim `sapaloq-shell@` (`make_above`) - Layer Shell **not available** on Mutter |
| **KDE / Sway / COSMIC** | Same Wails binary | gtk-layer-shell hook on WebKitGTK window |
| **Linux X11** | Wails + web | EWMH hints / normal floating |
| **Windows** | Wails + WebView2 (later) | Win32 topmost APIs |
| **macOS** | Out of scope until stated | - |

Widget is **thin client** to `sapaloq.sock` - separate `sapaloq-widget` binary, same IPC all platforms. GNOME Shell extension is **optional window-policy shim only**, not the UI layer.

---

## Config

```json
{
  "platform": {
    "adapter": "auto",
    "detectOrder": ["gnome", "kde", "freedesktop", "headless"],
    "allowFallback": true,
    "capabilitiesRequired": ["notify.watch", "notify"]
  }
}
```

See [config.schema.json](../schema/config.schema.json) → `platform`.

---

## Packaging / matrix (future)

| Distro | DE | Adapter | Notes |
|--------|-----|---------|-------|
| Ubuntu 24.04 | GNOME | gnome | MVP target |
| Pop!_OS | COSMIC/GNOME fork | gnome | test portal |
| Debian 12 | GNOME | gnome | |
| CentOS Stream | GNOME optional | gnome | |
| Fedora KDE | Plasma | kde | later |
| Windows 11 | - | windows | later |

One **sapaloq-core** binary per GOOS (linux/amd64, linux/arm64, windows/amd64).

---

## What stays universal (your vision intact)

- Single binary, in-proc bus, SQLite, jsonl
- Orchestrator + sub-agents + config-by-agent
- Isolation from cursor-agent
- LIMITATIONS.md constraints (offline, boot gap) apply **all** platforms

---

## Implementation order

| Step | Deliverable |
|------|-------------|
| 1 | `internal/platform.Desktop` interface + `Capabilities` |
| 2 | `adapters/gnome` - D-Bus notifications, portal screenshot |
| 3 | Rename tools/events docs → `desktop_*` / `platform.*` |
| 4 | Auto-detect from env |
| 5 | `adapters/freedesktop` linux fallback |
| 6 | KDE, Windows adapters |

---

## Non-goals (platform)

- One UI binary identical pixel-perfect on all OS day one
- GNOME Shell extension as SapaLOQ core requirement
- Separate broker per platform
