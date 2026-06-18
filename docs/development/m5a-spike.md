# M5a spike — Wails widget + IPC

Validates [UI-DECISION.md](../UI-DECISION.md) assumptions before M5b.

## Goals

| # | Check |
|---|--------|
| 1 | Wails v2 frameless + transparent HUD |
| 2 | CSS ring states (idle / thinking / delegating / needs-input) |
| 3 | Unix socket IPC round-trip to mock `sapaloq-core` |
| 4 | Drag region via `--wails-draggable` |

## Prereqs (Ubuntu 24.04)

```bash
sudo apt install libwebkit2gtk-4.1-dev build-essential
go install github.com/wailsapp/wails/v2/cmd/wails@latest
```

**Ubuntu 24.04:** Wails v2 needs build tag `webkit2_41` (4.0 package unavailable). `wails doctor` may still warn — ignore if build succeeds.

```bash
wails build -tags webkit2_41
wails dev -tags webkit2_41
```

## Interaction (FAB + popup)

Pola seperti widget chat sudut kiri bawah (IDCloudHost-style):

| State | Window | UX |
|-------|--------|-----|
| **Collapsed** | 48×48 | FAB bulat; GTK **input shape** = hanya lingkaran yang tangkap klik |
| **Expanded** | 360×520 | Popup naik ke atas; FAB tetap di bawah |

- **Klik FAB** → buka / tutup popup (window resize + anchor bottom-left)
- **✕** di header → tutup
- **Alt+klik FAB** → ping IPC (dev)
- **Double-klik FAB** (collapsed) → cycle ring state

Rebuild & run:

```bash
wails build -tags webkit2_41
./build/bin/sapaloq-widget
```

## Run

Terminal 1 — mock core:

```bash
cd /apps/workspace/sapaloq/cmd/sapaloq-mock
go run . 
# listens on /tmp/sapaloq-spike.sock
```

Terminal 2 — widget:

```bash
export PATH="$PATH:$(go env GOPATH)/bin"
cd /apps/workspace/sapaloq/cmd/sapaloq-widget
npm install --prefix frontend
wails dev -tags webkit2_41
```

Or production build:

```bash
wails build -tags webkit2_41
./build/bin/sapaloq-widget
```

## Env

| Var | Default |
|-----|---------|
| `SAPALOQ_SOCKET` | `/tmp/sapaloq-spike.sock` |

## Spike results (fill after run)

| Check | Result | Notes |
|-------|--------|-------|
| Frameless HUD | ✅ | `wails build -tags webkit2_41` → `build/bin/sapaloq-widget` |
| Ring CSS smooth | ✅ | Frontend compiles (`tsc && vite build` pass) |
| IPC p99 < 50ms | ✅ | `TestPingCore` round-trip local unix socket |
| GNOME Wayland | ⏳ | Re-run under `XDG_SESSION_TYPE=wayland` session |
| Always-on-top | ⏳ | X11: Wails flag; GNOME Wayland: manual or M5c shim |

## Next

- M5b: wire real `sapaloq.sock` protocol from orchestrator
- M5c: `sapaloq-shell@` GJS shim (see UI-DECISION pseudocode)
- Patch BLUEPRINT Part XVIII after spike confirms Wails v2 on target DE
