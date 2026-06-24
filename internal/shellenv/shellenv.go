// Package shellenv folds interactive shell startup environment into the
// process environment at boot.
//
// Why this exists: SapaLOQ normally runs as a systemd `--user` service (and via
// XDG autostart for the widget). Neither starts a login/interactive shell, so a
// user's `~/.bashrc` / `~/.zshrc` exports (e.g. provider tokens) are NOT in the
// process environment — the credential loader then falls back to `.env` or the
// Cursor vscdb and a token set only in the shell rc is silently invisible.
//
// LoadOnce sources the shell rc files (bash first, then zsh — some terminals
// default to zsh) and copies the RELEVANT, not-already-set variables into the
// process environment, BEFORE the credential loader runs. The resulting
// priority is therefore:
//
//	real process env (unchanged)  >  shell rc (this package)  >  .env  >  vscdb
//
// because the credential loader only consults `.env`/vscdb when a value is
// still empty, and LoadOnce never overwrites a variable that is already set.
//
// It is deliberately conservative: Linux-only, best-effort (any missing shell,
// missing rc file, or failed/timed-out source is silently ignored), bounded by
// a short timeout so an interactive/hanging rc can't freeze startup, and it
// only imports a small allowlist of key prefixes so the rest of the shell
// environment (PATH, prompt vars, …) never leaks in.
package shellenv

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// relevantPrefixes is the allowlist of env-var name prefixes imported from the
// shell rc. Anything else in the shell environment is ignored so we don't drag
// in PATH, locale, prompt, or unrelated secrets.
var relevantPrefixes = []string{
	"SAPALOQ_",
	"CURSOR_",
	"BLACKBOX_",
	"OPENAI_",
	"ANTHROPIC_",
	"KIMI_",
	"MOONSHOT_",
	"OPENROUTER_",
}

// sourceTimeout bounds how long a single shell rc source may take. An rc that
// blocks (waiting on input, a slow network call, …) must not freeze startup.
// Package var so tests can shrink it.
var sourceTimeout = 3 * time.Second

var once sync.Once

// LoadOnce sources the user's shell rc files and folds the relevant, not-set
// variables into the process environment. It runs at most once per process and
// is a no-op on non-Linux hosts or when nothing can be sourced.
func LoadOnce() {
	once.Do(load)
}

func load() {
	if runtime.GOOS != "linux" {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return
	}

	// bash first, then zsh — later sources do NOT override earlier imports
	// (applyEnv skips keys already present), so bash wins a tie, matching the
	// "bashrc first" intent. Each is independently best-effort.
	for _, sh := range []shellRC{
		{shell: "bash", rc: filepath.Join(home, ".bashrc")},
		{shell: "zsh", rc: filepath.Join(home, ".zshrc")},
	} {
		env, ok := sourceShellRC(sh)
		if !ok {
			continue // silent: shell missing, rc missing, or source failed
		}
		applyEnv(env)
	}
}

type shellRC struct {
	shell string // "bash" / "zsh"
	rc    string // absolute path to the rc file
}

// sourceShellRC runs the shell so it sources rc, then prints the environment
// NUL-separated (`env -0`) so values containing newlines survive. Returns the
// parsed map and ok=false on any failure (missing shell/rc, non-zero exit,
// timeout) — the caller treats !ok as "nothing to import".
func sourceShellRC(s shellRC) (map[string]string, bool) {
	if _, err := os.Stat(s.rc); err != nil {
		return nil, false // rc file absent → nothing to do
	}
	shellPath, err := exec.LookPath(s.shell)
	if err != nil {
		return nil, false // shell not installed
	}

	ctx, cancel := context.WithTimeout(context.Background(), sourceTimeout)
	defer cancel()

	// `source <rc>` then emit the environment. `2>/dev/null` swallows any noise
	// the rc prints; a failing source still lets `env -0` run so partial
	// exports are captured. We invoke a non-interactive shell to avoid prompts.
	script := "source " + shellQuote(s.rc) + " 2>/dev/null; env -0"
	cmd := exec.CommandContext(ctx, shellPath, "-c", script)
	// Start from the current environment so the rc sees a realistic context.
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	if err != nil {
		// Non-zero exit can still carry usable stdout (e.g. rc `return`s after
		// exports). Use whatever was captured rather than discarding it.
		if len(out) == 0 {
			return nil, false
		}
	}
	return parseNulEnv(out), true
}

// applyEnv copies relevant keys into the process env, skipping any key that is
// already set (so an explicit process env / earlier shell always wins).
func applyEnv(env map[string]string) {
	for k, v := range env {
		if !isRelevant(k) {
			continue
		}
		if _, present := os.LookupEnv(k); present {
			continue
		}
		_ = os.Setenv(k, v)
	}
}

func isRelevant(key string) bool {
	for _, p := range relevantPrefixes {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
}

// parseNulEnv parses the NUL-separated `key=value` output of `env -0`.
func parseNulEnv(b []byte) map[string]string {
	out := map[string]string{}
	for _, rec := range strings.Split(string(b), "\x00") {
		if rec == "" {
			continue
		}
		eq := strings.IndexByte(rec, '=')
		if eq <= 0 {
			continue
		}
		out[rec[:eq]] = rec[eq+1:]
	}
	return out
}

// shellQuote single-quotes a path for safe embedding in the `source` command
// (handles spaces and most metacharacters; embedded single quotes are escaped).
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
