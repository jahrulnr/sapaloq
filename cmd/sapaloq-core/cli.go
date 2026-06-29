package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/debug"
	"github.com/jahrulnr/sapaloq/internal/vault"
)

const usageText = `SapaLOQ core - orchestrator, IPC, cursor-bridge brain.

Usage:
  sapaloq-core [--debug|-d] [--verbose|-v] <command> [args]

Global flags:
  --debug, -d         Audit logs on stderr (bridge, credentials, stream summary)
  --verbose, -v       Debug + wire frame/payload detail (implies --debug)

Commands:
  run                 Start IPC server on sapaloq.sock (default)
  chat [message]      One-shot chat stream to stdout (mock without token)
  doctor              Validate config, runtime dirs, and cursor token env
  vault list          Show recent undeclared/unknown tool calls
  vault stats         Summarize vault log
  vault path          Print vault log file path
  service install     Install systemd --user unit, enable + start it
  service uninstall   Stop, disable and remove the unit (config is kept)
  service start       Start the service (manual)
  service stop        Stop the service (manual)
  service status      systemctl --user status passthrough
  version             Print the build version (also --version, -V)
  help                Show this help

Environment:
  SAPALOQ_DEBUG           1/true - same as --debug
  SAPALOQ_VERBOSE         1/true - same as --verbose
  SAPALOQ_CONFIG          Path to config.json (default: ~/.config/sapaloq/config.json)
  SAPALOQ_CURSOR_TOKEN    Cursor API token (optional if autoload succeeds)
  CURSOR_ACCESS_TOKEN     Same as above (cursor-bridge convention)
  CURSOR_MACHINE_ID       Checksum machine id (optional; from state.vscdb if missing)
  CURSOR_STATE_VSCDB      Override path to Cursor IDE state.vscdb

Credential autoload priority (same as cursor-bridge):
  1. process.env (SAPALOQ_CURSOR_TOKEN / CURSOR_ACCESS_TOKEN + CURSOR_MACHINE_ID)
  2. .env (cwd, then ~/.config/sapaloq/.env)
  3. ~/.config/Cursor/User/globalStorage/state.vscdb

Examples:
  sapaloq-core --debug run
  sapaloq-core --verbose chat "halo"
  sapaloq-core chat "halo"
  sapaloq-core vault list --limit 20
  SAPALOQ_CURSOR_TOKEN=... sapaloq-core chat "test live stream"
  sapaloq-core doctor
  sapaloq-core service install
  sapaloq-core service status
`

func printUsage() {
	fmt.Print(usageText)
}

func runVault(args []string) {
	if len(args) == 0 {
		exitf("usage: sapaloq-core vault <list|stats|path>")
	}
	cfgPath := config.ConfigPath(os.Getenv("SAPALOQ_CONFIG"), config.DefaultConfig())
	cfg, err := config.Load(cfgPath)
	if err != nil {
		exitf("config: %v", err)
	}
	dirs := config.RuntimeDirs(cfg)
	logPath := vault.LogPath(dirs.DataDir)

	switch args[0] {
	case "path":
		fmt.Println(logPath)
	case "list":
		fs := flag.NewFlagSet("vault list", flag.ExitOnError)
		limit := fs.Int("limit", 20, "max entries to show (most recent)")
		asJSON := fs.Bool("json", false, "output JSON array")
		_ = fs.Parse(args[1:])
		entries, err := vault.ReadEntries(logPath, *limit)
		if err != nil {
			exitf("vault: %v", err)
		}
		if *asJSON {
			b, _ := json.MarshalIndent(entries, "", "  ")
			fmt.Println(string(b))
			return
		}
		if len(entries) == 0 {
			fmt.Println("vault empty")
			return
		}
		for _, e := range entries {
			fmt.Printf("%s  %s  %s→%s  %s  session=%s\n",
				e.At.Format("2006-01-02 15:04:05"),
				e.Reason,
				e.RawName,
				e.ResolvedName,
				e.Source,
				shortID(e.SessionID),
			)
		}
	case "stats":
		fs := flag.NewFlagSet("vault stats", flag.ExitOnError)
		asJSON := fs.Bool("json", false, "output JSON")
		_ = fs.Parse(args[1:])
		entries, err := vault.ReadEntries(logPath, 0)
		if err != nil {
			exitf("vault: %v", err)
		}
		stats := vault.StatsFor(entries)
		if *asJSON {
			b, _ := json.MarshalIndent(stats, "", "  ")
			fmt.Println(string(b))
			return
		}
		fmt.Printf("total=%d\n", stats.Total)
		for reason, count := range stats.ByReason {
			fmt.Printf("  %s: %d\n", reason, count)
		}
		if len(stats.TopTools) > 0 {
			fmt.Println("top tools:")
			for _, t := range stats.TopTools {
				fmt.Printf("  %s: %d\n", t.Name, t.Count)
			}
		}
	default:
		exitf("unknown vault command %q", args[0])
	}
}

func runChat(cfg config.Config, cfgPath string, message string) {
	orch, err := newOrchestrator(cfg, cfgPath)
	if err != nil {
		exitf("orchestrator: %v", err)
	}
	stream, err := orch.SendChat(context.Background(), "cli", message, nil)
	if err != nil {
		exitf("chat: %v", err)
	}
	for ev := range stream {
		if debug.Enabled() {
			debug.Debugf("chat: event kind=%s session=%s", ev.Kind, ev.SessionID)
		}
		switch ev.Kind {
		case bridge.EventTranscript:
			if ev.Transcript == nil {
				continue
			}
			for _, e := range ev.Transcript.Entries {
				switch e.Kind {
				case bridge.TranscriptThinking:
					fmt.Printf("[thinking] %s\n", e.Text)
				case bridge.TranscriptText:
					fmt.Printf("[response] %s\n", e.Text)
				case bridge.TranscriptTool:
					fmt.Printf("[tool] %s\n", e.ToolName)
				case bridge.TranscriptError:
					fmt.Printf("[error] %s\n", e.Text)
				}
			}
			if ev.Transcript.Finished {
				fmt.Println("[done]")
			}
		case bridge.EventThinkingDelta:
			fmt.Printf("[thinking] %s\n", ev.Delta)
		case bridge.EventResponseDelta:
			fmt.Printf("[response] %s\n", ev.Delta)
		case bridge.EventToolCall:
			if ev.ToolCall != nil {
				fmt.Printf("[tool] %s %s\n", ev.ToolCall.Name, string(ev.ToolCall.Arguments))
			}
		case bridge.EventError:
			fmt.Printf("[error] %s\n", ev.Error)
		case bridge.EventDone:
			fmt.Println("[done]")
		default:
			if ev.Delta != "" {
				fmt.Printf("[%s] %s\n", ev.Kind, ev.Delta)
			} else {
				fmt.Printf("[%s]\n", ev.Kind)
			}
		}
	}
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}
