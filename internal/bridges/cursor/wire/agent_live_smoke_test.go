package wire

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridges/cursor/credentials"
)

func TestLiveAgentStreamSmoke(t *testing.T) {
	if strings.TrimSpace(os.Getenv("SAPALOQ_LIVE_E2E")) == "" {
		t.Skip("set SAPALOQ_LIVE_E2E=1")
	}
	creds, err := credentials.Load(credentials.Options{TokenEnv: "SAPALOQ_CURSOR_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	body := BuildAgentRequestBody(AgentRunOptions{
		UserText:       "Reply with exactly: pong",
		ModelID:        "default",
		ConversationID: "live-smoke",
	})
	var text bytesBuilder
	streamFn := SelectAgentStreamFn()
	err = streamFn(ctx, AgentStreamOptions{
		Host:    AgentHost(creds.GhostMode),
		Path:    AgentAgentPath,
		Token:   creds.AccessToken,
		Body:    body,
		Timeout: 90 * time.Second,
	}, func(decoded []AgentDecoded, _ []byte) {
		for _, d := range decoded {
			if d.Kind == "text" {
				text.WriteString(d.Text)
			}
		}
	})
	if err != nil {
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
