package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/debug"
	"github.com/jahrulnr/sapaloq/internal/parse"
)

// maxLineBytes bounds a single JSONL line. An agent_message text and a
// command_execution's aggregated_output can be large, so we give the scanner a
// generous buffer rather than the bufio default (64 KiB), which would otherwise
// surface as a spurious "token too long" error mid-stream.
const maxLineBytes = 8 << 20 // 8 MiB

// scanResult reports what the stream contained, so the caller (runTurn) can
// finalize a single terminal event from the event stream rather than the
// process exit code (contract §4 — the event stream is authoritative).
type scanResult struct {
	threadID     string // captured from thread.started (used to persist resume mapping)
	turnFailed   bool   // an error / turn.failed / item:error was seen
	sawCompleted bool   // turn.completed was seen
	lastErrorMsg string // explained message from the last error line (for the terminal)
	scanErr      error  // a non-EOF scanner read error (abnormal stream break)
}

// scanStream reads JSONL from r line-by-line, maps each known event to a
// bridge.StreamEvent, and sends the non-terminal ones on out via the ctx-aware
// send helper. It is tolerant: malformed JSON lines and unknown event / item
// types are skipped without crashing (contract §3.3, §8).
//
// It does NOT emit the terminal (done/error) event itself — terminal semantics
// depend on the whole stream plus the process exit, so runTurn owns the single
// terminal event using the returned scanResult.
//
// A returned ok=false means ctx was cancelled mid-stream; the caller stops.
func scanStream(ctx context.Context, sessionID string, r io.Reader, out chan<- bridge.StreamEvent) (scanResult, bool) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBytes)

	var res scanResult
	for sc.Scan() {
		ev, ok := decodeLine(sc.Bytes())
		if !ok {
			// Tolerant: skip non-JSON noise / typeless objects (contract §3.3).
			continue
		}

		// Track terminal state from the event stream itself.
		switch ev.Type {
		case typeThreadStarted:
			res.threadID = ev.ThreadID
		case typeTurnCompleted:
			res.sawCompleted = true
		case typeError, typeTurnFailed:
			res.turnFailed = true
		case typeItemCompleted, typeItemStarted:
			if ev.Item != nil && ev.Item.Type == itemError {
				res.turnFailed = true
			}
		}

		for _, se := range mapEvent(sessionID, ev) {
			// runTurn owns the single terminal error event, so suppress
			// mid-stream error kinds here to avoid double emission — but
			// remember their explained message for the terminal.
			if se.Kind == bridge.EventError {
				if se.Error != "" {
					res.lastErrorMsg = se.Error
				}
				continue
			}
			if !send(ctx, out, se) {
				return res, false // ctx cancelled: stop immediately, no leak.
			}
		}
	}
	res.scanErr = sc.Err()
	return res, true
}

// mapEvent translates a single decoded Codex event into zero or more
// bridge.StreamEvents. It is pure (no I/O, no terminal bookkeeping) so it can be
// unit-tested in isolation. Returning a slice keeps the contract that one Codex
// line may legitimately produce several user-facing events.
func mapEvent(sessionID string, ev codexEvent) []bridge.StreamEvent {
	switch ev.Type {
	case typeThreadStarted:
		// thread.started carries the session id used for `resume`. Surfaced as a
		// status event so the orchestrator/log can see the captured thread_id;
		// the runner persists SessionID->thread_id from scanResult.threadID.
		return []bridge.StreamEvent{statusEvent(sessionID, StatusSession)}

	case typeTurnStarted:
		return []bridge.StreamEvent{statusEvent(sessionID, StatusWorking)}

	case typeItemStarted, typeItemCompleted:
		return mapItem(sessionID, ev)

	case typeTurnCompleted:
		// turn.completed is the authoritative success terminal. runTurn emits the
		// EventDone after the stream drains so it cannot be emitted twice or out
		// of order; usage is logged separately (see runTurn).
		return nil

	case typeError:
		// Top-level error: the turn is failing. Event-authoritative — surface an
		// error regardless of the eventual exit code (contract §4).
		return []bridge.StreamEvent{errorEvent(sessionID, explainCodexError(ev.Message))}

	case typeTurnFailed:
		msg := ""
		if ev.Error != nil {
			msg = ev.Error.Message
		}
		return []bridge.StreamEvent{errorEvent(sessionID, explainCodexError(msg))}

	default:
		// Unknown top-level event type: forward-compatible skip, no crash.
		debug.Verbosef("codex-bridge: skipping unknown event type %q", ev.Type)
		return nil
	}
}

// mapItem handles item.started / item.completed by dispatching on item.type.
// Unknown item types are skipped (forward-compat), never fatal.
func mapItem(sessionID string, ev codexEvent) []bridge.StreamEvent {
	if ev.Item == nil {
		return nil
	}
	it := ev.Item
	switch it.Type {
	case itemAgentMessage:
		// The visible answer. Only emit on completion; item.started for a
		// message (if it ever appears) carries no final text.
		if ev.Type == typeItemCompleted && it.Text != "" {
			return []bridge.StreamEvent{responseEvent(sessionID, it.Text)}
		}
		return nil

	case itemReasoning:
		// Reasoning is NOT observed on 0.141.0 + ChatGPT/gpt-5.5 (contract §3.3)
		// but the parser must tolerate it and map it to a thinking event when
		// present (other models/accounts may surface reasoning summaries).
		if it.Text != "" {
			return []bridge.StreamEvent{thinkingEvent(sessionID, it.Text)}
		}
		return nil

	case itemCommandExecution:
		return mapCommandExecution(sessionID, ev.Type, it)

	case itemError:
		// Item-level error (e.g. deprecated config). Surface as an error event;
		// scanStream also flags the turn failed so the terminal stays error.
		return []bridge.StreamEvent{errorEvent(sessionID, explainCodexError(it.Message))}

	default:
		// Unknown item.type: skip without crashing (contract §3.3).
		debug.Verbosef("codex-bridge: skipping unknown item.type %q", it.Type)
		return nil
	}
}

// mapCommandExecution turns a command_execution item into a tool-call event on
// start and a status event on completion. The aggregated output is logged
// (truncated) rather than dumped verbatim into the chat stream.
func mapCommandExecution(sessionID, evType string, it *codexItem) []bridge.StreamEvent {
	switch it.Status {
	case cmdStatusInProgress:
		args, _ := json.Marshal(map[string]string{"command": it.Command})
		ev := bridge.NewEvent(bridge.EventToolCall)
		ev.SessionID = sessionID
		ev.ToolCall = &parse.ToolCall{
			ID:        it.ID,
			Name:      "command_execution",
			Arguments: args,
			Source:    "codex",
		}
		return []bridge.StreamEvent{ev}

	case cmdStatusCompleted:
		exit := "?"
		if it.ExitCode != nil {
			exit = fmt.Sprintf("%d", *it.ExitCode)
		}
		debug.Debugf("codex-bridge: command_execution done id=%s exit=%s output=%q",
			it.ID, exit, truncate(it.AggregatedOutput, 512))
		// A tool_done status lets the UI mark the tool finished without dumping
		// the raw aggregated_output into the chat. The exit code travels in the
		// status string so a consumer can render it.
		return []bridge.StreamEvent{statusEvent(sessionID, fmt.Sprintf("%s:exit=%s", StatusToolDone, exit))}

	default:
		return nil
	}
}

// truncate bounds a string for logging so a large aggregated_output never floods
// the debug log.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…(truncated)"
}

// --- event constructors: keep StreamEvent creation in one place. ---

func thinkingEvent(sessionID, text string) bridge.StreamEvent {
	ev := bridge.NewEvent(bridge.EventThinkingDelta)
	ev.SessionID = sessionID
	ev.Delta = text
	return ev
}

func responseEvent(sessionID, text string) bridge.StreamEvent {
	ev := bridge.NewEvent(bridge.EventResponseDelta)
	ev.SessionID = sessionID
	ev.Delta = text
	return ev
}

func statusEvent(sessionID, status string) bridge.StreamEvent {
	ev := bridge.NewEvent(bridge.EventStatus)
	ev.SessionID = sessionID
	ev.Status = status
	return ev
}

func errorEvent(sessionID, msg string) bridge.StreamEvent {
	ev := bridge.NewEvent(bridge.EventError)
	ev.SessionID = sessionID
	ev.Error = msg
	return ev
}

// send delivers ev on out unless ctx is done first. Mirrors the cursor bridge's
// ctx-aware send helper so a cancelled context stops streaming immediately. The
// event constructors already stamp At via bridge.NewEvent, so we re-stamp only
// if a caller passed a zero-time event.
func send(ctx context.Context, out chan<- bridge.StreamEvent, ev bridge.StreamEvent) bool {
	if ev.At.IsZero() {
		ev.At = time.Now().UTC()
	}
	select {
	case <-ctx.Done():
		return false
	case out <- ev:
		return true
	}
}

// finalizeTerminal emits exactly one terminal event from the drained
// scanResult, applying the event-authoritative rule (contract §4 / design §9):
//   - turnFailed (any error/turn.failed/item:error seen) => EventError, even if
//     the process later exits 0;
//   - else sawCompleted (turn.completed seen, clean scan) => EventDone;
//   - else (no terminal event, e.g. the process was killed/crashed) =>
//     EventError describing the abnormal end (with exit code + last stderr).
//
// abnormal supplies the process exit cause for the no-terminal case; pass nil
// for a replay/mock with no process behind it.
func finalizeTerminal(ctx context.Context, sessionID string, res scanResult, out chan<- bridge.StreamEvent, abnormal func() string) {
	switch {
	case res.turnFailed:
		send(ctx, out, errorEvent(sessionID, terminalErrorMessage(ctx, res.lastErrorMsg)))
	case res.sawCompleted && res.scanErr == nil:
		send(ctx, out, doneEvent(sessionID))
	default:
		send(ctx, out, errorEvent(sessionID, abnormalMessage(res.scanErr, abnormal)))
	}
}

func doneEvent(sessionID string) bridge.StreamEvent {
	ev := bridge.NewEvent(bridge.EventDone)
	ev.SessionID = sessionID
	return ev
}

// terminalErrorMessage builds the message for the single terminal error event.
// A cancelled context takes precedence; otherwise we reuse the explained text
// captured from the last error/turn.failed line so the terminal stays
// actionable.
func terminalErrorMessage(ctx context.Context, lastErrorMsg string) string {
	if ctx.Err() != nil {
		return fmt.Sprintf("codex turn cancelled: %v", ctx.Err())
	}
	if lastErrorMsg != "" {
		return lastErrorMsg
	}
	return "codex turn failed (see error event in stream)"
}

func abnormalMessage(scanErr error, abnormal func() string) string {
	var extra string
	if abnormal != nil {
		extra = abnormal()
	}
	switch {
	case scanErr != nil && extra != "":
		return fmt.Sprintf("codex stream ended abnormally with no terminal event: %v; %s", scanErr, extra)
	case scanErr != nil:
		return fmt.Sprintf("codex stream ended abnormally with no terminal event: %v", scanErr)
	case extra != "":
		return fmt.Sprintf("codex stream ended abnormally with no terminal event: %s", extra)
	default:
		return "codex stream ended abnormally with no terminal event"
	}
}

// statusName lets tests refer to a status sub-kind without the exit suffix.
func statusName(s string) string {
	if i := strings.IndexByte(s, ':'); i >= 0 {
		return s[:i]
	}
	return s
}
