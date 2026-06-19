package main

import (
	"github.com/jahrulnr/sapaloq/internal/debug"
)

type globalFlags struct {
	Debug   bool
	Verbose bool
}

func parseGlobalFlags(args []string) (globalFlags, []string) {
	flags := globalFlags{}
	out := make([]string, 0, len(args))
	for _, arg := range args {
		switch arg {
		case "--debug", "-d":
			flags.Debug = true
		case "--verbose", "-v":
			flags.Verbose = true
		default:
			out = append(out, arg)
		}
	}
	return flags, out
}

func splitCommand(args []string) (cmd string, cmdArgs []string) {
	if len(args) == 0 {
		return "", nil
	}
	return args[0], args[1:]
}

func initDebugFromArgs(args []string) []string {
	flags, rest := parseGlobalFlags(args)
	debug.Configure(flags.Debug, flags.Verbose)
	if debug.Enabled() {
		level := "debug"
		if debug.Verbose() {
			level = "verbose"
		}
		debug.Debugf("sapaloq-core: log level=%s (also SAPALOQ_DEBUG / SAPALOQ_VERBOSE env)", level)
	}
	return rest
}

func envDebugHint() string {
	if debug.Enabled() {
		return ""
	}
	return " (use --debug or --verbose for audit logs on stderr)"
}
