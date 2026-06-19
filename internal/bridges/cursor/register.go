package cursor

import (
	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/config"
)

// Register attaches the cursor-bridge to the runtime registry. The caller
// passes the active provider entry plus the runtime config (used for vault
// paths). Caller is responsible for only calling this when entry.Driver ==
// "cursor-bridge".
func Register(reg *bridge.Registry, entry config.LLMBridge, runtime config.RuntimeConfig) error {
	b, err := New(entry, runtime)
	if err != nil {
		return err
	}
	reg.Register(b)
	return nil
}
