package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/parse"
)

var inlineImageRE = regexp.MustCompile(`!\[([^\]]*)\]\((data:(image/[^;,]+)(?:;base64)?,[^)]+)\)`)
var attachmentMetaRE = regexp.MustCompile(`<!--sapaloq-attachment:[A-Za-z0-9+/=]+-->`)

// runConversation drives one Ask turn. If thinkingOut is non-nil, reasoning
// (EventThinkingDelta) text is accumulated into it so the caller can persist it
// as a show-only "thinking" turn — separate from the assistant answer (`all`).
func (o *Orchestrator) runConversation(ctx context.Context, snap providerSnapshot, out chan<- bridge.StreamEvent, sessionID, fallbackTask string, messages []bridge.Message, thinkingOut *strings.Builder) (strings.Builder, error) {
	var all strings.Builder
	cleanMessages, images := extractImages(messages)
	// One-shot: once we've dropped images and retried text-only we never
	// re-attach them in this run, so a model that can't see images degrades
	// gracefully instead of looping on the same 400.
	visionDowngraded := false
	// If this model is already known text-only (marked false in a prior run or
	// in config), don't bother sending the image — drop it and proceed on the
	// text placeholder so the user still gets an answer instead of an error.
	if len(images) > 0 && !o.visionAllowed(snap.entry.Key, snap.entry.Model) {
		images = nil
		visionDowngraded = true
	}
	runtimeCfg := snap.cfg.Orchestrator.WithDefaults()
	budget := runtimeCfg.Continuation
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(budget.MaxWallTimeMinutes)*time.Minute)
	defer cancel()
	lastOutcome := ""
	noProgressTurns := 0
	lastToolSignature := ""
	identicalToolCalls := 0
	toolCalls := 0
	lastCompactedMessageCount := 0
	// Bounds how many times an upstream context-overflow 400 can trigger a
	// forced compaction + retry, so a conversation that can't shrink enough
	// surfaces the error instead of looping forever.
	forcedCompactions := 0
	const maxForcedCompactions = 3

	for inferenceTurn := 1; inferenceTurn <= budget.MaxInferenceTurns; inferenceTurn++ {
		if err := runCtx.Err(); err != nil {
			o.emit(context.Background(), out, bridge.StreamEvent{Kind: bridge.EventDone, SessionID: sessionID, At: time.Now().UTC()})
			if ctx.Err() != nil {
				return all, nil
			}
			return all, fmt.Errorf("continuation wall-time budget exhausted after %d minutes", budget.MaxWallTimeMinutes)
		}
		if shouldCompactConversation(cleanMessages, snap.entry.ContextWindow, runtimeCfg.Compaction.BackgroundThreshold) &&
			len(cleanMessages) > lastCompactedMessageCount+2 {
			blocking := conversationTokenRatio(cleanMessages, snap.entry.ContextWindow) >= runtimeCfg.Compaction.BlockingThreshold
			if blocking {
				o.emit(runCtx, out, statusEvent(sessionID, "compacting"))
			}
			cleanMessages = compactConversationMessages(cleanMessages, fallbackTask, runtimeCfg.Compaction.PreserveRecentFraction)
			lastCompactedMessageCount = len(cleanMessages)
			if blocking {
				o.emit(runCtx, out, statusEvent(sessionID, "working"))
			}
			if !runtimeCfg.Compaction.ResumeAfterCompaction {
				return all, fmt.Errorf("continuation paused after compaction by configuration")
			}
		}

		o.emit(runCtx, out, statusEvent(sessionID, "working"))
		stream, err := snap.br.Complete(runCtx, bridge.Request{
			SessionID:     sessionID,
			Messages:      cleanMessages,
			Model:         snap.entry.Model,
			DeclaredTools: askTools,
			Images:        images,
		})
		if err != nil {
			if runCtx.Err() != nil && ctx.Err() != nil {
				o.emit(context.Background(), out, bridge.StreamEvent{Kind: bridge.EventDone, SessionID: sessionID, At: time.Now().UTC()})
				return all, nil
			}
			return all, err
		}
		var response strings.Builder
		var toolResults []string
		stop := false
		hadError := false
		retryTextOnly := false
		retryCompacted := false
		lastErr := ""
		for ev := range stream {
			if ev.SessionID == "" {
				ev.SessionID = sessionID
			}
			switch ev.Kind {
			case bridge.EventResponseDelta:
				response.WriteString(ev.Delta)
				all.WriteString(ev.Delta)
				o.emit(runCtx, out, ev)
			case bridge.EventToolCall:
				o.emit(runCtx, out, ev)
				if ev.ToolCall != nil {
					toolCalls++
					if toolCalls > budget.MaxToolCalls {
						return all, fmt.Errorf("tool-call budget exhausted after %d calls", budget.MaxToolCalls)
					}
					signature := toolCallSignature(*ev.ToolCall)
					if signature == lastToolSignature {
						identicalToolCalls++
					} else {
						lastToolSignature = signature
						identicalToolCalls = 1
					}
					if identicalToolCalls > budget.MaxIdenticalToolCalls {
						return all, fmt.Errorf("loop detected: identical tool call repeated %d times", identicalToolCalls)
					}
					result := o.handleAskTool(runCtx, snap, out, sessionID, fallbackTask, *ev.ToolCall)
					if result.handled {
						toolResults = append(toolResults, result.text)
					}
					stop = stop || result.stop
				}
			case bridge.EventError:
				if runCtx.Err() != nil {
					o.emit(context.Background(), out, bridge.StreamEvent{Kind: bridge.EventDone, SessionID: sessionID, At: time.Now().UTC()})
					if ctx.Err() != nil {
						return all, nil
					}
					return all, fmt.Errorf("continuation wall-time budget exhausted after %d minutes", budget.MaxWallTimeMinutes)
				}
				lastErr = ev.Error
				// A vision-rejection on an image-bearing request is recoverable:
				// mark the model text-only (memory + config) and retry this same
				// turn without the image so the run never gets stuck. Any other
				// error surfaces as before.
				if len(images) > 0 && !visionDowngraded && imageRejection(ev.Error) {
					o.setVisionSupport(snap.entry.Key, snap.entry.Model, false)
					o.persistVisionSupport(snap.entry.Key, snap.entry.Model, false)
					retryTextOnly = true
					o.emit(runCtx, out, statusEvent(sessionID, "model can't see images — retrying without the attachment"))
					break
				}
				// A context/token-overflow 400 means our (guessed) context window
				// was too large. Force a compaction pass and retry instead of
				// failing — providers rarely expose the real limit, so the 400 is
				// the only reliable signal.
				if looksLikeContextOverflow(ev.Error) && forcedCompactions < maxForcedCompactions {
					retryCompacted = true
					o.emit(runCtx, out, statusEvent(sessionID, "context too large — compacting and retrying"))
					break
				}
				hadError = true
				o.emit(runCtx, out, ev)
			case bridge.EventThinkingDelta:
				// Accumulate reasoning so it can be persisted as a show-only
				// "thinking" turn (survives restart), then forward it live.
				if thinkingOut != nil {
					thinkingOut.WriteString(ev.Delta)
				}
				o.emit(runCtx, out, ev)
			case bridge.EventDone:
				// A bridge-level done ends one inference turn. The orchestrator
				// emits one final done after all tool continuations.
			default:
				o.emit(runCtx, out, ev)
			}
		}
		if retryTextOnly {
			// Drain any trailing events from the broken stream, then strip the
			// images and re-run this inference turn text-only. The accumulated
			// `all`/`response` from this aborted turn is discarded by reusing
			// the same cleanMessages without appending an assistant message.
			for range stream {
			}
			visionDowngraded = true
			cleanMessages, _ = extractImages(cleanMessages)
			images = nil
			inferenceTurn--
			continue
		}
		if retryCompacted {
			// Drain the broken stream, then force one compaction pass and re-run
			// this same turn against the shrunken history. If compaction can't
			// shrink further (already minimal), recovery is impossible — surface
			// the original overflow error rather than loop.
			for range stream {
			}
			compacted := compactConversationMessages(cleanMessages, fallbackTask, runtimeCfg.Compaction.PreserveRecentFraction)
			if len(compacted) >= len(cleanMessages) {
				return all, fmt.Errorf("context overflow and conversation already minimal: %s", lastErr)
			}
			forcedCompactions++
			cleanMessages = compacted
			lastCompactedMessageCount = len(cleanMessages)
			cleanMessages, images = extractImages(cleanMessages)
			if len(images) > 0 && (visionDowngraded || !o.visionAllowed(snap.entry.Key, snap.entry.Model)) {
				images = nil
			}
			o.emit(runCtx, out, statusEvent(sessionID, "working"))
			inferenceTurn--
			continue
		}
		if hadError {
			return all, nil
		}
		if len(images) > 0 {
			o.setVisionSupport(snap.entry.Key, snap.entry.Model, true)
			o.persistVisionSupport(snap.entry.Key, snap.entry.Model, true)
		}
		if stop || len(toolResults) == 0 {
			o.emit(runCtx, out, bridge.StreamEvent{Kind: bridge.EventDone, SessionID: sessionID, At: time.Now().UTC()})
			return all, nil
		}
		outcome := fmt.Sprintf("%x", sha256.Sum256([]byte(response.String()+"\x00"+strings.Join(toolResults, "\x00"))))
		if outcome == lastOutcome {
			noProgressTurns++
		} else {
			lastOutcome = outcome
			noProgressTurns = 0
		}
		if noProgressTurns >= budget.MaxNoProgressTurns {
			return all, fmt.Errorf("loop detected: no observable progress for %d inference turns", noProgressTurns)
		}
		toolResultsBody := "[Tool results]\n" + strings.Join(toolResults, "\n\n")
		// Persist tool results as a "tool" turn so they count toward context
		// usage and auto-compaction. These messages ARE sent to the model (they
		// are appended to cleanMessages below and replayed via contextMessages,
		// which maps role "tool" → "assistant"), so leaving them unrecorded made
		// ContextUsage under-count and auto-compact trigger too late. Use the
		// outer ctx (not the cancelable runCtx) so a wall-time timeout does not
		// drop the audit/accounting record.
		if o.chat != nil {
			_ = o.chat.AppendTurn(ctx, sessionID, "tool", toolResultsBody, estimateTextTokens(toolResultsBody))
		}
		cleanMessages = append(cleanMessages,
			bridge.Message{Role: "assistant", Content: response.String()},
			bridge.Message{Role: "user", Content: toolResultsBody + "\nContinue the original request using these results. Do not repeat the tool call unless another tool action is required."},
		)
		// Re-extract images from the freshly appended tool-results message so a
		// read_image tool call (which returns inline-image markdown) becomes real
		// vision input on the next turn — the same channel widget attachments use.
		cleanMessages, images = extractImages(cleanMessages)
		// If the model is already known text-only (this run downgraded it, or a
		// prior run/config marked it), drop the images and keep going on text
		// instead of stalling — the markdown is already a text placeholder.
		if len(images) > 0 && (visionDowngraded || !o.visionAllowed(snap.entry.Key, snap.entry.Model)) {
			images = nil
		}
	}
	return all, fmt.Errorf("inference-turn budget exhausted after %d turns", budget.MaxInferenceTurns)
}

func toolCallSignature(call parse.ToolCall) string {
	return call.Name + "\x00" + strings.TrimSpace(string(call.Arguments))
}

func conversationTokenRatio(messages []bridge.Message, contextWindow int) float64 {
	if contextWindow <= 0 {
		contextWindow = defaultContextWindow
	}
	total := 0
	for _, message := range messages {
		total += estimateTextTokens(message.Content)
	}
	return float64(total) / float64(contextWindow)
}

func shouldCompactConversation(messages []bridge.Message, contextWindow int, threshold float64) bool {
	if threshold <= 0 {
		threshold = 0.70
	}
	return len(messages) > 8 && conversationTokenRatio(messages, contextWindow) >= threshold
}

func compactConversationMessages(messages []bridge.Message, originalTask string, preserveRecentFraction float64) []bridge.Message {
	if len(messages) <= 6 {
		return messages
	}
	if preserveRecentFraction <= 0 || preserveRecentFraction >= 1 {
		preserveRecentFraction = 0.30
	}
	system := messages[0]
	body := messages[1:]
	keep := int(math.Ceil(float64(len(body)) * preserveRecentFraction))
	if keep < 4 {
		keep = 4
	}
	if keep >= len(body) {
		return messages
	}
	cut := len(body) - keep
	var checkpoint strings.Builder
	checkpoint.WriteString("[Mid-run compacted checkpoint]\n")
	checkpoint.WriteString("Original task: ")
	checkpoint.WriteString(truncateForCheckpoint(originalTask, 600))
	checkpoint.WriteString("\nCompleted context:\n")
	for _, message := range body[:cut] {
		text := strings.TrimSpace(message.Content)
		if text == "" {
			continue
		}
		checkpoint.WriteString("- ")
		checkpoint.WriteString(message.Role)
		checkpoint.WriteString(": ")
		checkpoint.WriteString(truncateForCheckpoint(text, 360))
		checkpoint.WriteByte('\n')
	}
	checkpoint.WriteString("Resume the same task from the recent messages below. Do not restart completed work.")
	compacted := make([]bridge.Message, 0, keep+2)
	compacted = append(compacted, system)
	compacted = append(compacted, bridge.Message{Role: "system", Content: checkpoint.String()})
	compacted = append(compacted, body[cut:]...)
	return compacted
}

func truncateForCheckpoint(text string, limit int) string {
	text = inlineImageRE.ReplaceAllString(text, "[image attachment preserved separately]")
	text = strings.Join(strings.Fields(text), " ")
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "…"
}

func extractImages(messages []bridge.Message) ([]bridge.Message, []bridge.Image) {
	cleaned := make([]bridge.Message, 0, len(messages))
	var images []bridge.Image
	lastUser := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			lastUser = i
			break
		}
	}
	for index, message := range messages {
		content := attachmentMetaRE.ReplaceAllString(message.Content, "")
		content = inlineImageRE.ReplaceAllStringFunc(content, func(match string) string {
			parts := inlineImageRE.FindStringSubmatch(match)
			if len(parts) < 4 {
				return match
			}
			dataURI := parts[2]
			mime := parts[3]
			if validDataImage(dataURI) {
				if index != lastUser {
					return "[Prior image attachment: " + strings.TrimSpace(parts[1]) + "]"
				}
				images = append(images, bridge.Image{DataURI: dataURI, MimeType: mime})
				name := strings.TrimSpace(parts[1])
				if name == "" {
					name = "attached image"
				}
				return "[Image attachment: " + name + "]"
			}
			return match
		})
		cleaned = append(cleaned, bridge.Message{Role: message.Role, Content: content})
	}
	return cleaned, images
}

func validDataImage(dataURI string) bool {
	comma := strings.IndexByte(dataURI, ',')
	if comma < 0 {
		return false
	}
	if strings.Contains(dataURI[:comma], ";base64") {
		_, err := base64.StdEncoding.DecodeString(dataURI[comma+1:])
		return err == nil
	}
	return true
}

func (o *Orchestrator) visionAllowed(provider, model string) bool {
	o.visionMu.RLock()
	supported, known := o.vision[provider+"\x00"+model]
	o.visionMu.RUnlock()
	return !known || supported
}

func (o *Orchestrator) setVisionSupport(provider, model string, supported bool) {
	o.visionMu.Lock()
	o.vision[provider+"\x00"+model] = supported
	o.visionMu.Unlock()
}

// seedVisionFromConfig pre-populates the in-memory vision cache from any
// provider entry that has an explicit supportsImages value, so a model proven
// text-only in a previous session is honoured immediately on startup.
func (o *Orchestrator) seedVisionFromConfig(cfg config.Config) {
	for _, p := range cfg.LLMBridge.Providers {
		if p.SupportsImages == nil {
			continue
		}
		o.setVisionSupport(p.Key, p.Model, *p.SupportsImages)
	}
}

// persistVisionSupport records a discovered vision capability into config.json
// (provider entry matched by key, falling back to model) so a model proven
// text-only is never re-probed across restarts. Best-effort: a write failure
// only loses persistence, not the in-memory cache, and never aborts the run.
// Guarded by visionMu so concurrent runs don't race the config read/write.
func (o *Orchestrator) persistVisionSupport(providerKey, model string, supported bool) {
	if o.cfgPath == "" {
		return
	}
	o.visionMu.Lock()
	defer o.visionMu.Unlock()
	raw, err := config.LoadRaw(o.cfgPath)
	if err != nil {
		return
	}
	if !setProviderVisionFlag(raw, providerKey, model, supported) {
		return
	}
	if err := config.SaveRaw(o.cfgPath, raw, "orchestrator:vision-probe"); err != nil {
		return
	}
	if reloaded, rErr := config.ReloadFromRaw(o.cfgPath); rErr == nil {
		o.mu.Lock()
		o.cfg = reloaded
		o.mu.Unlock()
	}
}

// setProviderVisionFlag walks the raw config map to llmBridge.providers[] and
// sets supportsImages on the entry whose "key" matches providerKey (or, when
// that is empty/unmatched, whose "model" matches). Returns true when an entry
// was updated. Kept generic (map[string]any) so it survives unknown fields and
// preserves the rest of the file verbatim.
func setProviderVisionFlag(raw map[string]any, providerKey, model string, supported bool) bool {
	root, ok := raw["llmBridge"].(map[string]any)
	if !ok {
		return false
	}
	list, ok := root["providers"].([]any)
	if !ok {
		return false
	}
	matchByModel := -1
	for i, item := range list {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if providerKey != "" {
			if k, _ := entry["key"].(string); k == providerKey {
				entry["supportsImages"] = supported
				return true
			}
		}
		if m, _ := entry["model"].(string); m == model && matchByModel < 0 {
			matchByModel = i
		}
	}
	if matchByModel >= 0 {
		if entry, ok := list[matchByModel].(map[string]any); ok {
			entry["supportsImages"] = supported
			return true
		}
	}
	return false
}

// imageRejection reports whether an upstream error on an image-bearing request
// most likely means the model can't accept images — so we should mark it
// text-only and retry without the attachment. Two signals:
//  1. An explicit vision-unsupported phrase (works regardless of status code,
//     and for providers that return the rejection inside a 200 body).
//  2. An HTTP 400 (the OpenAI-compatible class for a malformed/invalid request)
//     that is NOT one of the unrelated 4xx classes (auth, rate limit, quota,
//     billing, context-length). On an image request, a 400 that isn't one of
//     those is overwhelmingly the image itself.
//
// The caller only invokes this when images were actually attached, which is the
// primary false-positive guard.
func imageRejection(message string) bool {
	if looksLikeVisionUnsupported(message) {
		return true
	}
	lower := strings.ToLower(message)
	if !strings.Contains(lower, "status 400") && !strings.Contains(lower, "400 ") {
		return false
	}
	return !mentionsUnrelated4xx(lower)
}

// looksLikeContextOverflow reports whether an upstream error means the request
// exceeded the model's context/token budget. The configured contextWindow is
// only a guess (providers rarely expose the real limit), so a too-large guess
// slips past our proactive compaction and surfaces here as a 400. When we see
// this we force a compaction pass and retry rather than failing the turn.
func looksLikeContextOverflow(message string) bool {
	lower := strings.ToLower(message)
	for _, kw := range []string{
		"context length", "context_length", "maximum context", "context window",
		"too many tokens", "tokens exceed", "exceed the", "reduce the length",
		"string too long", "maximum_tokens", "max_tokens",
	} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// mentionsUnrelated4xx detects 4xx causes that have nothing to do with vision,
// so a 400 carrying one of them is NOT treated as an image rejection.
func mentionsUnrelated4xx(lower string) bool {
	for _, kw := range []string{
		"rate limit", "ratelimit", "rate_limit", "429",
		"quota", "billing", "payment", "insufficient", "credit",
		"expired", "invalid api key", "invalid_api_key", "unauthorized",
		"authentication", "permission", "401", "403",
		"context length", "context_length", "maximum context", "too many tokens",
		"reduce the length", "context window",
	} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

func looksLikeVisionUnsupported(message string) bool {
	lower := strings.ToLower(message)
	// "text-only" / "multimodal" are strong vision signals on their own; they
	// almost never appear in unrelated errors. Phrasings around "image" need a
	// rejection verb to avoid matching incidental mentions.
	if strings.Contains(lower, "text-only") || strings.Contains(lower, "multimodal") {
		return true
	}
	return (strings.Contains(lower, "image") || strings.Contains(lower, "vision")) &&
		(strings.Contains(lower, "not support") ||
			strings.Contains(lower, "unsupported") ||
			strings.Contains(lower, "not allowed") ||
			strings.Contains(lower, "cannot process") ||
			strings.Contains(lower, "can't process"))
}
