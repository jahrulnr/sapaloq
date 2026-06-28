package wire

import (
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestSessionIDMatchesCursorBridge verifies that sessionIDFromToken uses
// UUID v5 over the DNS namespace, matching the reference cursor-bridge
// implementation in cursor-proto-lab/src/checksum/cursorChecksum.js.
func TestSessionIDMatchesCursorBridge(t *testing.T) {
	token := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.payload.signature"
	got := sessionIDFromToken(token)
	want := uuid.NewSHA1(uuid.NameSpaceDNS, []byte(token)).String()
	if got != want {
		t.Fatalf("session id mismatch\nwant=%s\ngot =%s", want, got)
	}
	if _, err := uuid.Parse(got); err != nil {
		t.Fatalf("invalid uuid: %v", err)
	}
	if got[14] != '5' {
		t.Fatalf("expected v5 uuid (version 5), got %q", got)
	}
}

// TestClientKeyMatchesCursorBridge verifies the x-client-key matches the
// cursor-bridge default (sha256 of token, no salt).
func TestClientKeyMatchesCursorBridge(t *testing.T) {
	token := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.payload.signature"
	got := hashed64Hex(token, "")
	if len(got) != 64 {
		t.Fatalf("len = %d", len(got))
	}
	if _, err := hex.DecodeString(got); err != nil {
		t.Fatalf("not hex: %v", err)
	}
}

// TestChecksumTimestampUnit verifies the checksum uses millisecond epoch / 1e6
// (cursor IDE / 9router), not Unix seconds / 1e6.
func TestChecksumTimestampUnit(t *testing.T) {
	fixed := time.Unix(1_729_789_702, 500_000_000) // 500ms into second
	want := uint64(fixed.UnixMilli() / 1_000_000)
	got := checksumTimestamp(fixed)
	if got != want {
		t.Fatalf("checksum timestamp = %d, want %d (ms/1e6)", got, want)
	}
	if got == uint64(fixed.Unix()/1_000_000) {
		t.Fatal("checksum must not use Unix seconds / 1e6")
	}
}

// cursor URL-safe alphabet (alphabet length, no padding) and appends machine id.
func TestChecksumShapeMatchesCursorBridge(t *testing.T) {
	checksum := cursorChecksum("test-machine-id")
	prefix := strings.TrimSuffix(checksum, "test-machine-id")
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	if strings.ContainsAny(prefix, "=+/") {
		t.Fatalf("checksum prefix not url-safe base64: %q", prefix)
	}
	for _, r := range prefix {
		if !strings.ContainsRune(alphabet, r) {
			t.Fatalf("checksum prefix has char %q outside alphabet: %q", r, prefix)
		}
	}
	if !strings.HasSuffix(checksum, "test-machine-id") {
		t.Fatalf("checksum should append machine id: %q", checksum)
	}
}

// TestBuildHeadersBearerAndGhost verifies cleanup of `::` prefix and ghost toggle.
func TestBuildHeadersBearerAndGhost(t *testing.T) {
	headers := BuildHeaders("prefix::the-real-token", "machine", true)
	if got := headers["authorization"]; got != "Bearer the-real-token" {
		t.Fatalf("authorization = %q", got)
	}
	if got := headers["x-ghost-mode"]; got != "true" {
		t.Fatalf("x-ghost-mode = %q", got)
	}
	if got := headers["x-cursor-client-version"]; got != "3.1.0" {
		t.Fatalf("x-cursor-client-version = %q", got)
	}
	if _, err := uuid.Parse(headers["x-session-id"]); err != nil {
		t.Fatalf("x-session-id not uuid v5: %v (%s)", err, headers["x-session-id"])
	}
}

// TestBuildHeadersSessionIDStable ensures session id is deterministic for a
// given token (matches uuid v5 determinism required by api2).
func TestBuildHeadersSessionIDStable(t *testing.T) {
	token := "stable-token-value"
	a := BuildHeaders(token, "machine", true)
	b := BuildHeaders(token, "machine", true)
	if a["x-session-id"] != b["x-session-id"] {
		t.Fatalf("session id not stable: %s vs %s", a["x-session-id"], b["x-session-id"])
	}
}
