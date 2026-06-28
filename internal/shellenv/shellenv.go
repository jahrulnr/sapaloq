// Package shellenv folds interactive shell startup environment into the
// process environment at boot.
//
// Why this exists: SapaLOQ normally runs as a systemd `--user` service (and via
// XDG autostart for the widget). Neither starts a login/interactive shell, so a
// user's `~/.bashrc` / `~/.zshrc` exports (e.g. provider tokens) are NOT in the
// process environment unless we import them here.
//
// LoadOnce sources the shell rc files (bash first, then zsh) and copies every
// variable from the sourced environment into the process, skipping keys already
// set by the parent (systemd, explicit Environment=, …). Then it loads
// ~/.config/sapaloq/.env and cwd .env the same way.
//
// Priority: real process env (unchanged) > shell rc > dotenv files.
//
// Linux-only, best-effort, short timeout on rc source. The rc is sourced with an
// interactive shell (`bash -ic` / `zsh -ic`) so the stock Debian/Ubuntu
// `~/.bashrc` non-interactive guard does not skip exports below it.
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

// sourceTimeout bounds how long a single shell rc source may take.
var sourceTimeout = 3 * time.Second

var once sync.Once

// LoadOnce sources the user's shell rc files and folds their environment into
// the process. It runs at most once per process.
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

	for _, sh := range []shellRC{
		{shell: "bash", rc: filepath.Join(home, ".bashrc")},
		{shell: "zsh", rc: filepath.Join(home, ".zshrc")},
	} {
		env, ok := sourceShellRC(sh)
		if !ok {
			continue
		}
		applyEnv(env)
	}
	loadDotEnvFiles(home)
}

func loadDotEnvFiles(home string) {
	paths := []string{
		filepath.Join(home, ".config", "sapaloq", ".env"),
	}
	if cwd, err := os.Getwd(); err == nil && strings.TrimSpace(cwd) != "" {
		paths = append(paths, filepath.Join(cwd, ".env"))
	}
	for _, path := range paths {
		env, ok := readDotEnvFile(path)
		if !ok {
			continue
		}
		applyEnv(env)
	}
}

func readDotEnvFile(path string) (map[string]string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		eq := strings.Index(trimmed, "=")
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(trimmed[:eq])
		val := strings.TrimSpace(trimmed[eq+1:])
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		out[key] = val
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

type shellRC struct {
	shell string
	rc    string
}

func sourceShellRC(s shellRC) (map[string]string, bool) {
	if _, err := os.Stat(s.rc); err != nil {
		return nil, false
	}
	shellPath, err := exec.LookPath(s.shell)
	if err != nil {
		return nil, false
	}

	ctx, cancel := context.WithTimeout(context.Background(), sourceTimeout)
	defer cancel()

	script := "source " + shellQuote(s.rc) + " 2>/dev/null; env -0"
	cmd := exec.CommandContext(ctx, shellPath, "-i", "-c", script)
	cmd.Env = os.Environ()
	cmd.Stdin = nil
	out, err := cmd.Output()
	if err != nil {
		if len(out) == 0 {
			return nil, false
		}
	}
	return parseNulEnv(out), true
}

// applyEnv copies keys into the process env when not already set.
func applyEnv(env map[string]string) {
	for k, v := range env {
		if k == "" {
			continue
		}
		if _, present := os.LookupEnv(k); present {
			continue
		}
		_ = os.Setenv(k, v)
	}
}

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

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
