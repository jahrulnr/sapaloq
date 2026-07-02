package gemini

import (
	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/config"
)

// Register attaches gemini-bridge to the runtime registry.
func Register(reg *bridge.Registry, entry config.LLMBridge) error {
	b, err := New(entry)
	if err != nil {
		return err
	}
	reg.Register(b)
	return nil
}
