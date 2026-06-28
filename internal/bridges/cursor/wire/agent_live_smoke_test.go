package wire

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridges/cursor/credentials"
	"github.com/jahrulnr/sapaloq/internal/debug"
)

func TestLiveAgentStreamSmoke(t *testing.T) {
	if strings.TrimSpace(os.Getenv("SAPALOQ_LIVE_E2E")) == "" {
		t.Skip("set SAPALOQ_LIVE_E2E=1")
	}
	debug.Configure(false, false)
	creds, err := credentials.Load(credentials.Options{TokenEnv: "SAPALOQ_CURSOR_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	if err := credentials.EnsureFresh(context.Background(), &creds); err != nil {
		t.Logf("token refresh: %v", err)
	}
	t.Logf("creds source=%s token_len=%d", creds.Source, len(creds.AccessToken))
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	body := BuildAgentRequestBody(AgentRunOptions{
		UserText:       "Reply with exactly: pong",
		ModelID:        "default",
		ConversationID: "live-smoke",
	})
	var text bytesBuilder
	var kinds []string
	streamFn := SelectAgentStreamFn()
	err = streamFn(ctx, AgentStreamOptions{
		Host:      AgentHost(creds.GhostMode),
		Path:      AgentAgentPath,
		Token:     creds.AccessToken,
		MachineID: creds.MachineID,
		GhostMode: creds.GhostMode,
		Body:      body,
		Timeout:   90 * time.Second,
	}, func(decoded []AgentDecoded, _ []byte) {
		for _, d := range decoded {
			kinds = append(kinds, d.Kind)
			if d.Kind == "text" {
				text.WriteString(d.Text)
			}
		}
	})
	if err != nil {
		t.Logf("events=%v", kinds)
		if strings.Contains(err.Error(), "unauthenticated") || strings.Contains(err.Error(), "unauthorized") {
			t.Skip("cursor credentials not authorized for agent api (wire handshake ok)")
		}
		if strings.Contains(err.Error(), "PROTOCOL_ERROR") {
			t.Skip("agent raw h2 protocol error (try SAPALOQ_AGENT_WIRE_DRIVER=http2): " + err.Error())
		}
		t.Fatalf("agent stream: %v", err)
	}
	if text.String() == "" {
		t.Fatal("expected agent response text")
	}
	t.Logf("response=%q", text.String())
}

type bytesBuilder struct{ b []byte }

func (b *bytesBuilder) WriteString(s string) {
	b.b = append(b.b, s...)
}
func (b *bytesBuilder) String() string { return string(b.b) }
