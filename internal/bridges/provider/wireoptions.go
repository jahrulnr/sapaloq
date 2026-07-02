package provider

import (
	"context"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/config"
)

// BuildWireOptions assembles WireOptions from a provider entry and request.
func BuildWireOptions(entry config.LLMBridge, req bridge.Request) (WireOptions, error) {
	b := &Bridge{entry: entry}
	return b.buildWireOptions(req)
}

// RunStream executes the wire layer and forwards normalized events to out.
func RunStream(ctx context.Context, entry config.LLMBridge, opts WireOptions, req bridge.Request, out chan<- bridge.StreamEvent) {
	b := &Bridge{entry: entry}
	b.runStream(ctx, opts, req, out)
}
