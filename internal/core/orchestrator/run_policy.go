package orchestrator

import (
	"context"
	"strings"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/parse/artifacts"
)

// foregroundPolicyDecision is the product-policy outcome before stop persist.
type foregroundPolicyDecision struct {
	stop          bool
	retryTurn     bool
	injectDelta   string
	resetThinking bool
}

func (o *Orchestrator) applyForegroundRunPolicy(
	cfg turnConfig,
	sessionID, fallbackTask string,
	toolResults []string,
	toolCallsThisTurn, toolCalls int,
	pendingTools []scheduledTool,
	inBridgeStopThisTurn bool,
	stop bool,
	response string,
	noiseRetries, maxNoiseRetries int,
) foregroundPolicyDecision {
	var d foregroundPolicyDecision
	if !cfg.foregroundOrchestrator || len(toolResults) > 0 {
		return d
	}
	sig := o.sessionSignals(sessionID)
	if sig.runningTasks > 0 || sig.awaitingClarification {
		return d
	}
	visible := strings.TrimSpace(StripCalledToolsMarkers(response))
	stopOnly := foregroundStopOnlyTurn(toolCallsThisTurn, pendingTools, inBridgeStopThisTurn)
	if visible != "" || (toolCallsThisTurn > 0 && !stopOnly) {
		return d
	}
	if artifacts.IsConversationalPing(fallbackTask) {
		d.injectDelta = artifacts.FallbackAskGreeting()
		d.resetThinking = true
		d.stop = true
		return d
	}
	if !stop && toolCallsThisTurn == 0 && toolCalls == 0 {
		if noiseRetries < maxNoiseRetries {
			d.retryTurn = true
			return d
		}
		d.injectDelta = artifacts.FallbackAskNoiseRetry()
		d.resetThinking = true
		d.stop = true
	}
	return d
}

func emitForegroundPolicyInjection(
	out turnSink,
	runCtx context.Context,
	sessionID string,
	d foregroundPolicyDecision,
	response, all *strings.Builder,
) {
	if d.injectDelta == "" {
		return
	}
	response.WriteString(d.injectDelta)
	all.WriteString(d.injectDelta)
	out.emit(runCtx, bridge.StreamEvent{
		Kind: bridge.EventResponseDelta, SessionID: sessionID, Delta: d.injectDelta, At: time.Now().UTC(),
	})
}
