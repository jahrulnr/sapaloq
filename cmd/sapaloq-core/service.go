package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/jahrulnr/sapaloq/internal/config"
)

// serviceUnitName is the systemd --user unit that supervises `sapaloq-core run`.
const serviceUnitName = "sapaloq.service"

// runService manages the systemd --user unit that keeps sapaloq-core running.
//
//	install    write the unit, daemon-reload, enable + start (idempotent)
//	uninstall  stop + disable, remove the unit, daemon-reload (config is kept)
//	start      start the unit (manual, e.g. after a stop)
//	stop       stop the unit (manual)
//	status     systemctl --user status passthrough
func runService(cfg config.Config, cfgPath string, args []string) {
	if len(args) == 0 {
		exitf("usage: sapaloq-core service <install|uninstall|start|stop|status>")
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		exitf("service: systemctl not found — systemd --user is required.\n" +
			"On a non-systemd host, run `sapaloq-core run` directly (e.g. from your\n" +
			"session autostart) instead of using the service subcommand.")
	}

	switch args[0] {
	case "install":
		serviceInstall(cfg, cfgPath)
	case "uninstall", "remove":
		serviceUninstall()
	case "start":
		mustSystemctl("start", serviceUnitName)
		fmt.Println("sapaloq.service started")
	case "stop":
		mustSystemctl("stop", serviceUnitName)
		fmt.Println("sapaloq.service stopped")
	case "status":
		// Status is informational; surface output and exit code verbatim.
		runSystemctlPassthrough("status", "--no-pager", serviceUnitName)
	default:
		exitf("unknown service command %q\n\nusage: sapaloq-core service <install|uninstall|start|stop|status>", args[0])
	}
}

func serviceInstall(cfg config.Config, cfgPath string) {
	exe, err := os.Executable()
	if err != nil {
		exitf("service: cannot resolve own binary path: %v", err)
	}
	if resolved, lerr := filepath.EvalSymlinks(exe); lerr == nil {
		exe = resolved
	}

	unitPath, err := userUnitPath()
	if err != nil {
		exitf("service: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		exitf("service: create unit dir: %v", err)
	}
	if err := os.WriteFile(unitPath, []byte(renderUnit(exe, cfgPath)), 0o644); err != nil {
		exitf("service: write unit: %v", err)
	}
	fmt.Printf("wrote %s\n", unitPath)

	mustSystemctl("daemon-reload")
	mustSystemctl("enable", serviceUnitName)
	mustSystemctl("restart", serviceUnitName) // restart == start when stopped; reloads on re-install
	fmt.Println("sapaloq.service installed, enabled and started")
	fmt.Println("Tip: `loginctl enable-linger $USER` keeps it running without an active login session.")
}

func serviceUninstall() {
	// Best-effort teardown: a missing/stopped unit must not abort the cleanup.
	_ = runSystemctl("stop", serviceUnitName)
	_ = runSystemctl("disable", serviceUnitName)

	unitPath, err := userUnitPath()
	if err != nil {
		exitf("service: %v", err)
	}
	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		exitf("service: remove unit: %v", err)
	}
	mustSystemctl("daemon-reload")
	fmt.Printf("removed %s\n", unitPath)
	fmt.Println("sapaloq.service uninstalled (config and data under ~/.config/sapaloq are kept)")
}

// userUnitPath resolves ~/.config/systemd/user/sapaloq.service, honouring
// XDG_CONFIG_HOME when set.
func userUnitPath() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home dir: %w", err)
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "systemd", "user", serviceUnitName), nil
}

// renderUnit builds the systemd unit. ExecStart points at the absolute binary
// path so the unit keeps working regardless of PATH or install location.
// SAPALOQ_CONFIG is pinned only when a non-default config path is in effect, so
// the service resolves the same config the CLI used to install it.
func renderUnit(exePath, cfgPath string) string {
	var b strings.Builder
	b.WriteString("[Unit]\n")
	b.WriteString("Description=SapaLOQ core (orchestrator + IPC)\n")
	b.WriteString("After=network.target\n\n")
	b.WriteString("[Service]\n")
	b.WriteString("Type=simple\n")
	if env := os.Getenv("SAPALOQ_CONFIG"); env != "" {
		b.WriteString(fmt.Sprintf("Environment=SAPALOQ_CONFIG=%s\n", cfgPath))
	}
	b.WriteString(fmt.Sprintf("ExecStart=%s run\n", exePath))
	b.WriteString("Restart=on-failure\n")
	b.WriteString("RestartSec=2\n\n")
	b.WriteString("[Install]\n")
	b.WriteString("WantedBy=default.target\n")
	return b.String()
}

func runSystemctl(args ...string) error {
	cmd := exec.Command("systemctl", append([]string{"--user"}, args...)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func mustSystemctl(args ...string) {
	if err := runSystemctl(args...); err != nil {
		exitf("service: systemctl --user %s failed: %v", strings.Join(args, " "), err)
	}
}

// runSystemctlPassthrough runs systemctl and exits with its exit code, so
// `service status` behaves like calling systemctl directly.
func runSystemctlPassthrough(args ...string) {
	cmd := exec.Command("systemctl", append([]string{"--user"}, args...)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		exitf("service: %v", err)
	}
}
