package main

import "testing"

func TestPingCore(t *testing.T) {
	res, err := pingCore(defaultSocketPath())
	if err != nil {
		t.Skipf("mock-core not running: %v", err)
	}
	if !res.OK {
		t.Fatalf("expected ok, got %+v", res)
	}
	if res.RoundTripMs > 50 {
		t.Logf("warning: round trip %dms > 50ms target", res.RoundTripMs)
	}
}
