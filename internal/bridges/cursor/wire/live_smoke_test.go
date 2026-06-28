package wire

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridges/cursor/credentials"
)

func TestLiveRawStreamSurfacesUnauthenticated(t *testing.T) {
	if strings.TrimSpace(os.Getenv("SAPALOQ_LIVE_E2E")) == "" {
		t.Skip("set SAPALOQ_LIVE_E2E=1")
	}
	creds, err := credentials.Load(credentials.Options{TokenEnv: "SAPALOQ_CURSOR_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	err = StreamChatRaw(ctx, StreamOptions{
		Token:     creds.AccessToken,
		MachineID: creds.MachineID,
		Model:     "default",
		Messages:  []ChatMessage{{Role: "user", Content: "Reply with exactly: pong"}},
		GhostMode: creds.GhostMode,
		Timeout:   90 * time.Second,
	}, func(part ExtractedPart) {})
	if err == nil {
		t.Fatal("expected chat stream error from Go wire against api2")
	}
	if !strings.Contains(err.Error(), "unauthenticated") {
		t.Fatalf("err = %v want unauthenticated", err)
	}
}
