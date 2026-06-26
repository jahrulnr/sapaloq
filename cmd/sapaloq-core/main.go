// Command sapaloq-core runs the SapaLOQ orchestrator.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/bridges/codex"
	"github.com/jahrulnr/sapaloq/internal/bridges/cursor"
	"github.com/jahrulnr/sapaloq/internal/bridges/provider"
	"github.com/jahrulnr/sapaloq/internal/bus"
	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/core/orchestrator"
	"github.com/jahrulnr/sapaloq/internal/debug"
	"github.com/jahrulnr/sapaloq/internal/ipc"
	"github.com/jahrulnr/sapaloq/internal/platform"
	"github.com/jahrulnr/sapaloq/internal/platform/freedesktop"
	"github.com/jahrulnr/sapaloq/internal/shellenv"
)

// version is the build version, stamped at release time via
//
//	-ldflags "-X main.version=<tag>"
//
// It defaults to "dev" for local/unstamped builds. Surfaced via the `version`
// command and the `--version`/`-V` flag so the release installer and users can
// confirm what is installed.
var version = "dev"

// init registers the concrete desktop backends so platform.Detect can build
// them. The core stays OS-agnostic: backends self-probe the session bus and
// fall back to headless when unavailable.
func init() {
	platform.RegisterFactory(platform.AdapterFreedesktop, freedesktop.Factory())
	platform.RegisterFactory(platform.AdapterGnome, freedesktop.Factory(
		freedesktop.WithAdapterID("gnome-v1"),
		freedesktop.WithDE("gnome"),
	))
}

func main() {
	if len(os.Args) >= 2 && isVersionArg(os.Args[1]) {
		fmt.Printf("sapaloq-core %s\n", version)
		return
	}
	// Fold the user's shell rc (~/.bashrc then ~/.zshrc) into the process
	// environment before anything reads credentials. Under systemd --user /
	// XDG autostart there is no login shell, so tokens exported only in the
	// shell rc would otherwise be invisible. Best-effort + silent on any
	// failure; never overrides an already-set variable. (.env is still handled
	// later by the credential loader, ranking below shell rc.)
	shellenv.LoadOnce()
	if len(os.Args) < 2 || isHelpArg(os.Args[1]) {
		printUsage()
		if len(os.Args) >= 2 && isHelpArg(os.Args[1]) {
			return
		}
		os.Exit(1)
	}

	rest := initDebugFromArgs(os.Args[1:])
	if len(rest) == 0 {
		printUsage()
		os.Exit(1)
	}
	cmd, cmdArgs := splitCommand(rest)

	cfgPath := config.ConfigPath(os.Getenv("SAPALOQ_CONFIG"), config.DefaultConfig())
	cfg, err := config.Load(cfgPath)
	if err != nil {
		exitf("config: %v", err)
	}

	switch cmd {
	case "doctor":
		credSource, err := config.Doctor(cfg)
		if err != nil {
			exitf("doctor failed: %v", err)
		}
		dirs := config.RuntimeDirs(cfg)
		fmt.Println("doctor ok")
		fmt.Printf("  config: %s\n", cfgPath)
		fmt.Printf("  socket: %s\n", dirs.SocketPath)
		fmt.Printf("  vault:  %s/vault/tool-calls.jsonl\n", dirs.DataDir)
		fmt.Printf("  cursor: %s\n", credSource)
	case "chat":
		message := "halo"
		if len(cmdArgs) > 0 {
			message = cmdArgs[0]
		}
		runChat(cfg, cfgPath, message)
	case "vault":
		runVault(cmdArgs)
	case "service":
		runService(cfg, cfgPath, cmdArgs)
	case "run":
		if err := config.MigrateDefaultDataRoot(); err != nil {
			fmt.Fprintf(os.Stderr, "sapaloq-core: %v\n", err)
		}
		dirs := config.RuntimeDirs(cfg)
		if err := config.EnsureRuntimeDirs(dirs); err != nil {
			exitf("runtime dirs: %v", err)
		}
		// Move pre-split artifacts out of memory/ into state/. Best-effort and
		// idempotent; never fatal so a migration hiccup can't block startup.
		if err := config.MigrateLegacyLayout(dirs); err != nil {
			fmt.Fprintf(os.Stderr, "sapaloq-core: %v\n", err)
		}
		orchestrator.SetBridgeFactory(newBridge)
		orch, err := newOrchestrator(cfg, cfgPath)
		if err != nil {
			exitf("orchestrator: %v", err)
		}
		entry, _ := cfg.LLMBridge.ActiveProvider()
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		orch.StartConfigWatcher(ctx)
		orch.StartWorkerWatchdog(ctx)
		startNotifyWatch(ctx, orch)
		defer stop()
		fmt.Printf("sapaloq-core listening on %s%s\n", dirs.SocketPath, envDebugHint())
		if debug.Enabled() {
			debug.Debugf("sapaloq-core: ipc server starting driver=%s", entry.Driver)
		}
		if err := ipc.NewServer(cfg, orch).ListenAndServe(ctx, dirs.SocketPath); err != nil {
			exitf("ipc: %v", err)
		}
	default:
		exitf("unknown command %q\n\n%s", cmd, usageText)
	}
}

// startNotifyWatch bridges incoming desktop notifications onto the event bus
// under the canonical topic sapaloq.v1.platform.notification. It is a no-op when
// the active adapter lacks CapNotifyWatch (headless returns a closed channel, so
// the goroutine exits immediately).
func startNotifyWatch(ctx context.Context, orch *orchestrator.Orchestrator) {
	d := orch.Desktop()
	if !platform.Has(d.Capabilities(), platform.CapNotifyWatch) {
		return
	}
	ch, err := d.NotifyWatch(ctx)
	if err != nil || ch == nil {
		debug.Debugf("platform: notify-watch unavailable: %v", err)
		return
	}
	b := orch.Bus()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-ch:
				if !ok {
					return
				}
				if b != nil {
					b.Publish("sapaloq.v1.platform.notification", orchestrator.NotificationStreamEvent(ev))
				}
			}
		}
	}()
}

func newOrchestrator(cfg config.Config, cfgPath string) (*orchestrator.Orchestrator, error) {
	b, err := newBridge(cfg)
	if err != nil {
		return nil, err
	}
	debug.Debugf("orchestrator: bridge=%s live_api=%v", b.ID(), b.Caps().LiveAPI)

	eventBus := newEventBus(cfg)
	return orchestrator.New(cfg, cfgPath, b, eventBus)
}

// newEventBus constructs the event bus, enabling the JSON-lines WAL when
// config.events.bus.walPath is set. When replayOnBoot is also enabled it logs
// how many durable events are recoverable (watchers attaching after boot can
// call Bus.Replay to rehydrate). Falls back to an in-memory bus on any WAL
// setup error so the core never fails to start over event durability.
func newEventBus(cfg config.Config) *bus.Bus {
	walPath := config.ExpandPath(cfg.Events.Bus.WALPath)
	if walPath == "" {
		return bus.New()
	}
	b, err := bus.NewWithWAL(walPath)
	if err != nil {
		debug.Debugf("event-bus: WAL disabled (%v); using in-memory bus", err)
		return bus.New()
	}
	if cfg.Events.Bus.ReplayOnBoot {
		var recovered int
		_ = b.Replay(0, func(bus.Event) { recovered++ })
		debug.Debugf("event-bus: WAL=%s replayable_events=%d", walPath, recovered)
	}
	return b
}

func newBridge(cfg config.Config) (bridge.Bridge, error) {
	entry, err := cfg.LLMBridge.ActiveProvider()
	if err != nil {
		return nil, fmt.Errorf("orchestrator: %w", err)
	}
	reg := bridge.NewRegistry()
	if entry.Driver == "cursor-bridge" {
		if err := cursor.Register(reg, entry, cfg.Runtime); err != nil {
			return nil, err
		}
	}
	if entry.Driver == "provider-bridge" {
		if err := provider.Register(reg, entry); err != nil {
			return nil, err
		}
	}
	if entry.Driver == "codex-bridge" {
		if err := codex.Register(reg, entry, cfg.Runtime); err != nil {
			return nil, err
		}
	}
	return reg.Get(entry.Driver)
}

func exitf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
