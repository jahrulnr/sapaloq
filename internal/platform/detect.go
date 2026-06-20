package platform

import (
	"os"
	"strings"
)

// Backend names recognized by detection.
const (
	AdapterGnome       = "gnome"
	AdapterFreedesktop = "freedesktop"
	AdapterHeadless    = "headless"
)

// Factory builds a concrete Desktop for a backend id. It returns (nil, err)
// when the backend can't be constructed in the current environment (e.g. no
// session bus), letting Detect fall back. Register real backends (freedesktop,
// gnome) via RegisterFactory from their sub-packages; the core stays OS-agnostic.
type Factory func() (Desktop, error)

var factories = map[string]Factory{}

// RegisterFactory associates a backend id with a constructor. Intended to be
// called from a backend sub-package's init or wiring code.
func RegisterFactory(id string, f Factory) {
	if id == "" || f == nil {
		return
	}
	factories[id] = f
}

// DetectEnv captures the environment signals detection uses. Exposed so it can
// be unit-tested deterministically without mutating process env.
type DetectEnv struct {
	XDGCurrentDesktop string
	DesktopSession    string
	Goos              string
}

// EnvFromOS reads the current process environment.
func EnvFromOS(goos string) DetectEnv {
	return DetectEnv{
		XDGCurrentDesktop: os.Getenv("XDG_CURRENT_DESKTOP"),
		DesktopSession:    os.Getenv("DESKTOP_SESSION"),
		Goos:              goos,
	}
}

// ResolveAdapterID decides which backend id to use from config + environment,
// WITHOUT constructing it. Pure function → unit-testable. It does not consult
// the factory registry; it only returns the preferred id given signals.
func ResolveAdapterID(cfg adapterPrefs, env DetectEnv) string {
	adapter := strings.ToLower(strings.TrimSpace(cfg.adapter()))
	if adapter != "" && adapter != "auto" {
		return adapter
	}
	// auto: non-Linux has no D-Bus desktop backend here → headless.
	if env.Goos != "linux" {
		return AdapterHeadless
	}
	de := strings.ToLower(env.XDGCurrentDesktop + " " + env.DesktopSession)
	order := cfg.detectOrder()
	if len(order) == 0 {
		order = []string{AdapterGnome, AdapterFreedesktop, AdapterHeadless}
	}
	for _, id := range order {
		switch strings.ToLower(id) {
		case AdapterGnome:
			if strings.Contains(de, "gnome") {
				return AdapterGnome
			}
		case AdapterFreedesktop:
			// freedesktop notifications work on most Linux DEs with a session
			// bus; treat it as the generic Linux choice when any DE is present.
			if strings.TrimSpace(de) != "" {
				return AdapterFreedesktop
			}
		case AdapterHeadless:
			return AdapterHeadless
		}
	}
	return AdapterHeadless
}

// adapterPrefs is the small slice of config detection needs. config.PlatformConfig
// satisfies this via an adapter at the call site to avoid an import cycle.
type adapterPrefs interface {
	adapter() string
	detectOrder() []string
}

// Prefs is a concrete adapterPrefs the orchestrator constructs from config.
type Prefs struct {
	Adapter     string
	DetectOrder []string
	Fallback    bool
}

func (p Prefs) adapter() string       { return p.Adapter }
func (p Prefs) detectOrder() []string { return p.DetectOrder }

// Detect resolves the preferred backend id and constructs it via the registry.
// If construction fails (or the backend isn't registered) and fallback is
// allowed, it returns the headless adapter via the provided headlessFn. The
// caller supplies headlessFn to avoid an import cycle with the headless package.
func Detect(p Prefs, env DetectEnv, headlessFn func() Desktop) Desktop {
	id := ResolveAdapterID(p, env)
	if id != AdapterHeadless {
		if f, ok := factories[id]; ok {
			if d, err := f(); err == nil && d != nil {
				return d
			}
		}
		// Not registered or failed to build.
		if !p.Fallback {
			// Honor a strict request only when a real backend exists; otherwise
			// we still must return something usable.
			if f, ok := factories[id]; ok {
				if d, err := f(); err == nil && d != nil {
					return d
				}
			}
		}
	}
	return headlessFn()
}
