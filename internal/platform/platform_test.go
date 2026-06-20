package platform

import "testing"

func TestHas(t *testing.T) {
	caps := []Capability{CapNotify, CapDND}
	if !Has(caps, CapNotify) {
		t.Fatalf("expected CapNotify present")
	}
	if Has(caps, CapNotifyWatch) {
		t.Fatalf("CapNotifyWatch should be absent")
	}
	if Has(nil, CapNotify) {
		t.Fatalf("nil caps should have nothing")
	}
}

func TestResolveAdapterIDExplicit(t *testing.T) {
	got := ResolveAdapterID(Prefs{Adapter: "freedesktop"}, DetectEnv{Goos: "linux"})
	if got != AdapterFreedesktop {
		t.Fatalf("explicit adapter should win, got %q", got)
	}
}

func TestResolveAdapterIDNonLinuxIsHeadless(t *testing.T) {
	got := ResolveAdapterID(Prefs{Adapter: "auto"}, DetectEnv{Goos: "darwin", XDGCurrentDesktop: "GNOME"})
	if got != AdapterHeadless {
		t.Fatalf("non-linux auto should be headless, got %q", got)
	}
}

func TestResolveAdapterIDAutoGnome(t *testing.T) {
	got := ResolveAdapterID(
		Prefs{Adapter: "auto", DetectOrder: []string{"gnome", "freedesktop", "headless"}},
		DetectEnv{Goos: "linux", XDGCurrentDesktop: "ubuntu:GNOME"},
	)
	if got != AdapterGnome {
		t.Fatalf("GNOME desktop should resolve gnome, got %q", got)
	}
}

func TestResolveAdapterIDAutoFreedesktopForOtherDE(t *testing.T) {
	got := ResolveAdapterID(
		Prefs{Adapter: "auto", DetectOrder: []string{"gnome", "freedesktop", "headless"}},
		DetectEnv{Goos: "linux", XDGCurrentDesktop: "KDE"},
	)
	if got != AdapterFreedesktop {
		t.Fatalf("non-GNOME linux DE should resolve freedesktop, got %q", got)
	}
}

func TestResolveAdapterIDNoDEIsHeadless(t *testing.T) {
	got := ResolveAdapterID(Prefs{Adapter: "auto"}, DetectEnv{Goos: "linux"})
	if got != AdapterHeadless {
		t.Fatalf("linux with no DE should be headless, got %q", got)
	}
}

// fakeBackend lets us verify the factory/fallback wiring without real D-Bus.
type fakeBackend struct{ Desktop }

func TestDetectUsesRegisteredFactory(t *testing.T) {
	// Save + restore registry.
	old := factories
	factories = map[string]Factory{}
	defer func() { factories = old }()

	built := false
	RegisterFactory(AdapterFreedesktop, func() (Desktop, error) {
		built = true
		return fakeBackend{}, nil
	})
	d := Detect(
		Prefs{Adapter: "freedesktop", Fallback: true},
		DetectEnv{Goos: "linux", XDGCurrentDesktop: "KDE"},
		func() Desktop { return fakeBackend{} },
	)
	if !built || d == nil {
		t.Fatalf("expected registered factory to build the backend")
	}
}

func TestDetectFallsBackToHeadless(t *testing.T) {
	old := factories
	factories = map[string]Factory{}
	defer func() { factories = old }()

	// No factory registered for the resolved id → must fall back.
	usedFallback := false
	d := Detect(
		Prefs{Adapter: "freedesktop", Fallback: true},
		DetectEnv{Goos: "linux", XDGCurrentDesktop: "KDE"},
		func() Desktop { usedFallback = true; return fakeBackend{} },
	)
	if !usedFallback || d == nil {
		t.Fatalf("expected headless fallback when no factory is registered")
	}
}
