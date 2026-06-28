package debug

import (
	"fmt"
	"os"
	"strings"
)

type Level int

const (
	LevelOff Level = iota
	LevelDebug
	LevelVerbose
)

var current Level

func Configure(debugFlag, verboseFlag bool) {
	switch {
	case verboseFlag || envTruthy("SAPALOQ_VERBOSE"):
		current = LevelVerbose
	case debugFlag || envTruthy("SAPALOQ_DEBUG"):
		current = LevelDebug
	default:
		current = LevelOff
	}
}

func CurrentLevel() Level { return current }

func Enabled() bool { return current >= LevelDebug }

func Verbose() bool { return current >= LevelVerbose }

func Debugf(format string, args ...any) {
	if !Enabled() {
		return
	}
	fmt.Fprintf(os.Stderr, "[debug] "+format+"\n", args...)
}

func Verbosef(format string, args ...any) {
	if !Verbose() {
		return
	}
	fmt.Fprintf(os.Stderr, "[verbose] "+format+"\n", args...)
}

// TraceBoundary logs layer crossings when SAPALOQ_TRACE_BOUNDARIES=1.
// Marker sites use the comment tag: sapaloq:boundary <from>→<to> — …
// See docs/BOUNDARIES.md.
func TraceBoundary(from, to, event string) {
	if !envTruthy("SAPALOQ_TRACE_BOUNDARIES") {
		return
	}
	fmt.Fprintf(os.Stderr, "[boundary] %s→%s %s\n", from, to, event)
}

func RedactSecret(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "<empty>"
	}
	if len(value) <= 8 {
		return "***"
	}
	return value[:4] + "…" + value[len(value)-4:]
}

func envTruthy(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
