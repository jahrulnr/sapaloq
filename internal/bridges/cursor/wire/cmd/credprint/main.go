// Command credprint loads Cursor credentials and prints JSON for live wire probes.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/jahrulnr/sapaloq/internal/bridges/cursor/credentials"
)

func main() {
	creds, err := credentials.Load(credentials.Options{TokenEnv: "SAPALOQ_CURSOR_TOKEN"})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_ = credentials.EnsureFresh(context.Background(), &creds)
	out := map[string]any{
		"accessToken": creds.AccessToken,
		"machineId":   creds.MachineID,
		"ghostMode":   creds.GhostMode,
	}
	enc := json.NewEncoder(os.Stdout)
	_ = enc.Encode(out)
}
