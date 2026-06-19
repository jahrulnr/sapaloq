package provider

import (
	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/config"
)

// Register attaches the multi-provider bridge to the runtime registry. The
// caller passes the active provider entry from LLMBridgeRoot.ActiveProvider.
// Caller is responsible for only calling this when entry.Driver ==
// "provider-bridge".
func Register(reg *bridge.Registry, entry config.LLMBridge) error {
	b, err := New(entry)
	if err != nil {
		return err
	}
	reg.Register(b)
	return nil
}
