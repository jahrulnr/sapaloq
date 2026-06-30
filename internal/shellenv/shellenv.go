// Package shellenv folds interactive shell startup environment into the
// process environment at boot.
//
// Why this exists: SapaLOQ normally runs as a systemd `--user` service (and via
// XDG autostart for the widget). Neither starts a login/interactive shell, so a
// user's `~/.bashrc` / `~/.zshrc` exports (e.g. provider tokens) are NOT in the
// process environment unless we import them here.
//
// Bootstrap waits until a desktop user session looks ready (XDG_RUNTIME_DIR),
// then sources shell rc files and ~/.config/sapaloq/.env. Watch retries in the
// background when configured credential env names are still empty — no systemd
// restart required after login.
//
// Priority: real process env (non-empty) > shell rc > dotenv files.
//
// Linux-only, best-effort. Rc is sourced with an interactive shell (`bash -ic` /
// `zsh -ic`) so the stock Debian/Ubuntu ~/.bashrc guard does not skip exports.
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

	"github.com/jahrulnr/sapaloq/internal/debug"
)

// sourceTimeout is the absolute cap for one bash/zsh rc source subprocess.
// We wait for the shell to finish (not a short per-poll kill); heavy nvm/conda
// bashrc must be allowed to complete.
var sourceTimeout = 120 * time.Second

// sessionPoll is how often Bootstrap/Watch recheck session readiness.
var sessionPoll = 500 * time.Millisecond

// watchInterval is how long Watch sleeps between reload attempts.
var watchInterval = 5 * time.Second

var (
	bootOnce sync.Once
	loadMu   sync.Mutex
)

// LoadOnce is an alias for Bootstrap (legacy name).
func LoadOnce() { Bootstrap() }

// Bootstrap waits for a user session (when needed), then imports shell rc and
// dotenv once per process.
func Bootstrap() {
	bootOnce.Do(func() {
		waitForUserSession(10 * time.Minute)
		reload()
	})
}

// Watch keeps importing shell rc/dotenv in the background until every listed
// env name is non-empty. Intended for `sapaloq-core run` under systemd linger:
// the first import may run before login; Watch picks up tokens after
// XDG_RUNTIME_DIR appears without restarting the service.
func Watch(keys ...string) {
	keys = dedupeKeys(keys)
	if len(keys) == 0 {
		return
	}
	go func() {
		for {
			if len(missingKeys(keys)) == 0 {
				time.Sleep(watchInterval)
				continue
			}
			waitForUserSession(0)
			reload()
			if debug.Enabled() {
				if miss := missingKeys(keys); len(miss) > 0 {
					debug.Debugf("shellenv: still missing %v after reload", miss)
				}
			}
			time.Sleep(watchInterval)
		}
	}()
}

func reload() {
	loadMu.Lock()
	defer loadMu.Unlock()
	importEnv()
}

func importEnv() {
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

func waitForUserSession(maxWait time.Duration) {
	if sessionReady() {
		return
	}
	deadline := time.Now().Add(maxWait)
	if maxWait <= 0 {
		deadline = time.Now().Add(24 * time.Hour)
	}
	ticker := time.NewTicker(sessionPoll)
	defer ticker.Stop()
	for time.Now().Before(deadline) {
		if sessionReady() {
			return
		}
		<-ticker.C
	}
}

func sessionReady() bool {
	xdg := strings.TrimSpace(os.Getenv("XDG_RUNTIME_DIR"))
	if xdg == "" {
		return false
	}
	st, err := os.Stat(xdg)
	return err == nil && st.IsDir()
}

func missingKeys(keys []string) []string {
	var out []string
	for _, k := range keys {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if strings.TrimSpace(os.Getenv(k)) == "" {
			out = append(out, k)
		}
	}
	return out
}

func dedupeKeys(keys []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, k := range keys {
		k = strings.TrimSpace(k)
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, k)
	}
	return out
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

// applyEnv copies keys into the process env when not already set to a
// non-empty value. Empty placeholders from systemd/pam must not block imports
// from shell rc or dotenv.
func applyEnv(env map[string]string) {
	for k, v := range env {
		if k == "" {
			continue
		}
		if cur, ok := os.LookupEnv(k); ok && strings.TrimSpace(cur) != "" {
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
