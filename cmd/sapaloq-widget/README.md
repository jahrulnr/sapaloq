# sapaloq-widget

Wails v2 thin client — FAB bottom-left + popup chat shell. Talks to `sapaloq-core` over unix socket (`sapaloq.sock`).

M5a spike validated: frameless transparency, GTK input shape (click-through), IPC ping to mock core.

## Dev

From repo root:

```bash
make run           # starts core + widget dev; Ctrl+C stops both
```

Widget-only dev from this directory:

```bash
wails dev -tags webkit2_41
```

## Build

```bash
make widget-build
./build/bin/sapaloq-widget
```

## App icon

The cross-platform icon master is `build/appicon.png`. The HUD uses the tighter
`frontend/src/assets/images/orb-core.png` crop of the same network-core artwork.

- `build/appicon.png` — Wails source and macOS app icon
- `build/windows/icon.ico` — Windows multi-resolution icon
- `build/linux/sapaloq.png` — Linux hicolor icon installed by `make install`
  and `install.sh`

The Linux dev window also embeds `build/appicon.png`, so `make run` shows the
same icon before installation.

See [docs/development/m5a-spike.md](../../docs/development/m5a-spike.md) and [docs/UI-DECISION.md](../../docs/UI-DECISION.md).
