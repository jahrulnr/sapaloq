// Command headerdump prints BuildAgentHeaders JSON for parity checks vs Node.
package main

import (
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
	h := wire.BuildAgentHeaders(creds.AccessToken, creds.MachineID, creds.GhostMode)
	_ = json.NewEncoder(os.Stdout).Encode(h)
}
