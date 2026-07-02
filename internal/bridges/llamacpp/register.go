package llamacpp

import (
	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/config"
)

// Register attaches llama-cpp to the runtime registry.
func Register(reg *bridge.Registry, entry config.LLMBridge) error {
	b, err := New(entry)
	if err != nil {
		return err
	}
	reg.Register(b)
	return nil
}
