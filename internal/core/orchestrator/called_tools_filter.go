package orchestrator

import "strings"

// calledToolsMarkers are the bracketed marker prefixes that must never reach
// the user's visible text stream:
//
//   - "[Called tools: " is the orchestrator's own anti double-spawn note (see
//     calledToolsNote) which some models then echo back as prose.
//   - "[Called tool: " (singular) is the same note re-emitted by models that
//     imitate the convention with the wrong grammatical number - observed with
//     opus echoing "[Called tool: sapaloq_stop]" as prose after a stop. Without
//     stripping it the echo leaks into the chat bubble as noise.
//   - "[Tool: " is the announce form some models (e.g. MiniMax-M3) emit in
//     their content alongside a real native tool_call. The bare label (no
//     trailing "{args}" object) is NOT a recoverable inline call - the
//     bridge's leak-scanner only recovers "[Tool: name]{args}" - so it leaks
//     to the user as noise. Worse, the model then SEES "[Tool: name]" in its
//     own prior turn, mistakes it for the correct tool-call syntax, and starts
//     emitting bare "[Tool: name]" labels instead of structured calls, which
//     execute nothing and spiral into a stuck loop (orch-task-…103).
//
// Stripping all three here, at the single point text deltas funnel to the user,
// removes the noise AND breaks the imitation loop (the model never sees the
// bad pattern echoed back).
var calledToolsMarkers = []string{"[Called tools: ", "[Called tool: ", "[Tool: "}

// markerMatch classifies a buffered "[…" fragment against calledToolsMarkers.
type markerMatch int

const (
	markerNone    markerMatch = iota // diverged from every marker → ordinary text
	markerPartial                    // still a viable prefix of some marker
	markerFull                       // equals some marker exactly → begin skipping
)

// classifyMarker reports whether pending is a full marker, a still-viable
// prefix of one, or has diverged from all of them.
func classifyMarker(pending string) markerMatch {
	for _, m := range calledToolsMarkers {
		if pending == m {
			return markerFull
		}
	}
	for _, m := range calledToolsMarkers {
		if strings.HasPrefix(m, pending) {
			return markerPartial
		}
	}
	return markerNone
}

// calledToolsFilter strips echoed "[Called tools: …]" notes out of the model's
// visible text stream before they reach the user (and before they are folded
// into the assistant message we persist).
//
// Why this exists: calledToolsNote injects a "[Called tools: name, …]" line
// into the assistant transcript so the model has in-context proof it actually
// called a tool (the anti double-spawn fix - the text-delta stream alone does
// not carry the tool_call). The unintended side effect is that some models,
// seeing that line in their own prior turn, *imitate* it and emit a fresh
// "[Called tools: write_file …, write_file …]" as plain prose on a later turn.
// That echo is not a real tool call (it carries no JSON arguments, so the
// leak-scanner cannot recover it) - it is pure noise that leaks to the user
// and into the progress log. We drop it here at the single point where text
// deltas are funnelled to the user.
//
// The marker frequently arrives split across several streamed deltas (the live
// trace showed "…paralel.[Called tools:", " write_file …", "]"), so the filter
// is stateful: it buffers a trailing fragment that could still become the
// marker and only releases or drops once it can decide. State is the pending
// buffer plus a frontier past the last byte already cleared for emit, so each
// feed only scans the new tail (no O(n²) rescans, no double emit).
type calledToolsFilter struct {
	// buf holds bytes not yet released to the caller: either the partial
	// prefix of a possible marker, or the inside of a marker being skipped.
	buf strings.Builder
	// skipping is true once we have seen the complete calledToolsMarker and
	// are discarding bytes up to (and including) its closing ']'.
	skipping bool
}

// feed accepts one content delta and returns the bytes that are safe to emit
// now. It withholds any trailing fragment that could still grow into a
// "[Called tools: …]" marker, and drops a marker span entirely once complete.
func (f *calledToolsFilter) feed(delta string) string {
	if delta == "" {
		return ""
	}
	var out strings.Builder
	for i := 0; i < len(delta); i++ {
		c := delta[i]
		if f.skipping {
			// Inside a marker: swallow everything until the closing ']'.
			if c == ']' {
				f.skipping = false
				f.buf.Reset()
			}
			continue
		}
		if c == '[' {
			// A '[' may start the marker; begin buffering from here so we can
			// confirm against calledToolsMarker as more bytes arrive.
			out.WriteString(f.flushPending())
			f.buf.WriteByte(c)
			continue
		}
		if f.buf.Len() > 0 {
			// We are mid-decision on a buffered "[…" fragment.
			f.buf.WriteByte(c)
			pending := f.buf.String()
			switch classifyMarker(pending) {
			case markerFull:
				// Full marker prefix matched: switch to skip mode and discard
				// the buffered prefix - the body + ']' are dropped too.
				f.skipping = true
				f.buf.Reset()
			case markerPartial:
				// Still a viable prefix of some marker; keep buffering.
			default:
				// Diverged from every marker: this was an ordinary '['. Release
				// the buffered bytes verbatim and resume normal passthrough.
				out.WriteString(pending)
				f.buf.Reset()
			}
			continue
		}
		out.WriteByte(c)
	}
	return out.String()
}

// flush is called once the stream ends. Any buffered bytes that never resolved
// into a complete marker were ordinary text (e.g. a trailing "[Cal") and must
// be released; an unterminated marker body (skipping with no closing ']') is
// intentionally discarded.
func (f *calledToolsFilter) flush() string {
	if f.skipping {
		f.buf.Reset()
		return ""
	}
	return f.flushPending()
}

// StripCalledToolsMarkers removes orchestrator-injected and model-echoed tool
// markers from text shown to the user. The markers remain in persisted turns
// so the model keeps in-context proof of which tools ran.
func StripCalledToolsMarkers(text string) string {
	if text == "" {
		return ""
	}
	var f calledToolsFilter
	var b strings.Builder
	b.WriteString(f.feed(text))
	b.WriteString(f.flush())
	return b.String()
}

// stripCalledToolsForDisplay removes tool markers and trims trailing whitespace
// left after the orchestrator's "\n\n[Called tools: …]" suffix.
func stripCalledToolsForDisplay(text string) string {
	return strings.TrimRight(StripCalledToolsMarkers(text), " \t\n\r")
}

// flushPending releases and clears any buffered partial-prefix bytes.
func (f *calledToolsFilter) flushPending() string {
	if f.buf.Len() == 0 {
		return ""
	}
	pending := f.buf.String()
	f.buf.Reset()
	return pending
}
