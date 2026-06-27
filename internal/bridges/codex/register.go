package codex

import (
	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/config"
)

// Register attaches the codex-bridge to the runtime registry. The caller passes
// the active provider entry plus the runtime config (used for the thread-store
// path under the vault dir). Caller is responsible for only invoking this when
// entry.Driver == "codex-bridge". Shape mirrors cursor.Register.
func Register(reg *bridge.Registry, entry config.LLMBridge, runtime config.RuntimeConfig) error {
	b, err := New(entry, runtime)
	if err != nil {
		return err
	}
	reg.Register(b)
	return nil
}
