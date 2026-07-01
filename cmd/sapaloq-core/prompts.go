package main

import (
	"flag"
	"fmt"
	"strings"

	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/prompts"
)

func runPrompts(cfg config.Config, args []string) {
	if len(args) == 0 {
		exitf("usage: sapaloq-core prompts <list|show|preview|where> [args]")
	}
	pcfg := cfg.Prompts.WithDefaults()
	mgr := prompts.New(config.ExpandPath(pcfg.Dir), pcfg.Enabled)

	switch args[0] {
	case "list":
		fs := flag.NewFlagSet("prompts list", flag.ExitOnError)
		tier := fs.String("tier", "all", "filter: editable, internal, bridge, all")
		_ = fs.Parse(args[1:])
		filter := strings.ToLower(strings.TrimSpace(*tier))
		for _, e := range prompts.Catalog() {
			if filter != "all" && string(e.Tier) != filter {
				continue
			}
			edit := "no"
			if e.Editable {
				edit = "yes"
			}
			fmt.Printf("%s\t%s\teditable=%s\t%s\n", e.Key, e.Tier, edit, e.File)
		}
	case "show":
		if len(args) < 2 {
			exitf("usage: sapaloq-core prompts show <key>")
		}
		key := args[1]
		body := prompts.Resolve(mgr, key)
		if strings.TrimSpace(body) == "" {
			exitf("unknown or empty prompt key %q (try: prompts list)", key)
		}
		fmt.Print(body)
		if !strings.HasSuffix(body, "\n") {
			fmt.Println()
		}
	case "preview":
		if len(args) < 2 {
			exitf("usage: sapaloq-core prompts preview <role>")
		}
		role := args[1]
		if role == "task-runner" {
			role = prompts.RoleAgent
		}
		fmt.Println("=== composed system prompt ===")
		fmt.Println(prompts.ComposeRole(mgr, role))
		fmt.Println()
		fmt.Println("=== typical internal blocks (static keys) ===")
		for _, key := range prompts.PreviewBlocks(role) {
			fmt.Printf("- %s (%s)\n", key, prompts.GetInternal(key))
		}
	case "where":
		fmt.Printf("prompts.enabled: %v\n", pcfg.Enabled)
		fmt.Printf("prompts.dir:     %s\n", config.ExpandPath(pcfg.Dir))
		fmt.Printf("embedded roles:  internal/prompts/defaults/*.md\n")
		fmt.Printf("embedded internal: internal/prompts/internal/**\n")
	default:
		exitf("unknown prompts command %q", args[0])
	}
}
