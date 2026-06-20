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
	if len(images) > 0 && !o.visionAllowed(snap.entry.Key, snap.entry.Model) {
		return all, fmt.Errorf("model %s is marked as not supporting image input", snap.entry.Model)
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
				hadError = true
				if len(images) > 0 && looksLikeVisionUnsupported(ev.Error) {
					o.setVisionSupport(snap.entry.Key, snap.entry.Model, false)
				}
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
		if hadError {
			return all, nil
		}
		if len(images) > 0 {
			o.setVisionSupport(snap.entry.Key, snap.entry.Model, true)
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
		cleanMessages = append(cleanMessages,
			bridge.Message{Role: "assistant", Content: response.String()},
			bridge.Message{Role: "user", Content: "[Tool results]\n" + strings.Join(toolResults, "\n\n") + "\nContinue the original request using these results. Do not repeat the tool call unless another tool action is required."},
		)
		// Re-extract images from the freshly appended tool-results message so a
		// read_image tool call (which returns inline-image markdown) becomes real
		// vision input on the next turn — the same channel widget attachments use.
		cleanMessages, images = extractImages(cleanMessages)
		if len(images) > 0 && !o.visionAllowed(snap.entry.Key, snap.entry.Model) {
			return all, fmt.Errorf("model %s is marked as not supporting image input", snap.entry.Model)
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

func looksLikeVisionUnsupported(message string) bool {
	lower := strings.ToLower(message)
	return strings.Contains(lower, "image") && (strings.Contains(lower, "not support") ||
		strings.Contains(lower, "unsupported") ||
		strings.Contains(lower, "text-only") ||
		strings.Contains(lower, "multimodal"))
}
