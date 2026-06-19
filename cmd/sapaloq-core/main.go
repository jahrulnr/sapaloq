// Command sapaloq-core runs the SapaLOQ orchestrator.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/bridges/cursor"
	"github.com/jahrulnr/sapaloq/internal/bridges/provider"
	"github.com/jahrulnr/sapaloq/internal/bus"
	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/core/orchestrator"
	"github.com/jahrulnr/sapaloq/internal/debug"
	"github.com/jahrulnr/sapaloq/internal/ipc"
)

func main() {
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
	case "run":
		dirs := config.RuntimeDirs(cfg)
		if err := config.EnsureRuntimeDirs(dirs); err != nil {
			exitf("runtime dirs: %v", err)
		}
		orch, err := newOrchestrator(cfg, cfgPath)
		if err != nil {
			exitf("orchestrator: %v", err)
		}
		entry, _ := cfg.LLMBridge.ActiveProvider()
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
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

func newOrchestrator(cfg config.Config, cfgPath string) (*orchestrator.Orchestrator, error) {
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
	b, err := reg.Get(entry.Driver)
	if err != nil {
		return nil, err
	}
	debug.Debugf("orchestrator: bridge=%s live_api=%v", b.ID(), b.Caps().LiveAPI)
	return orchestrator.New(cfg, cfgPath, b, bus.New())
}

func exitf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
