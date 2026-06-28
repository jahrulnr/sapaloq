// Command agentconfig prints one gateway config JSON line (Go headers + body).
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"

	"github.com/jahrulnr/sapaloq/internal/bridges/cursor/credentials"
	"github.com/jahrulnr/sapaloq/internal/bridges/cursor/wire"
)

func main() {
	creds, err := credentials.Load(credentials.Options{TokenEnv: "SAPALOQ_CURSOR_TOKEN"})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_ = credentials.EnsureFresh(context.Background(), &creds)
	body := wire.BuildAgentRequestBody(wire.AgentRunOptions{
		UserText:       "Reply with exactly: pong",
		ModelID:        "default",
		ConversationID: "live-smoke",
	})
	headers := wire.BuildAgentHeaders(creds.AccessToken, creds.MachineID, creds.GhostMode)
	headers[":method"] = "POST"
	headers[":path"] = wire.AgentAgentPath
	headers[":scheme"] = "https"
	headers[":authority"] = "agentn.global.api5.cursor.sh"
	cfg := map[string]any{
		"host":      "agentn.global.api5.cursor.sh",
		"path":      wire.AgentAgentPath,
		"headers":   headers,
		"bodyB64":   base64.StdEncoding.EncodeToString(body),
		"timeoutMs": 20000,
	}
	enc := json.NewEncoder(os.Stdout)
	_ = enc.Encode(cfg)
}
