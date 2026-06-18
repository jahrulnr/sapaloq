# sapaloq-widget

Wails v2 thin client — FAB bottom-left + popup chat shell. Talks to `sapaloq-core` over unix socket (`sapaloq.sock`).

M5a spike validated: frameless transparency, GTK input shape (click-through), IPC ping to mock core.

## Dev

From repo root:

```bash
make mock          # terminal 1
make widget-dev    # terminal 2 (Ubuntu: webkit2_41 tag)
```

Or from this directory:

```bash
wails dev -tags webkit2_41
```

## Build

```bash
make widget-build
./build/bin/sapaloq-widget
```

See [docs/development/m5a-spike.md](../../docs/development/m5a-spike.md) and [docs/UI-DECISION.md](../../docs/UI-DECISION.md).
