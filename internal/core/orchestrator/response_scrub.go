package orchestrator

import "strings"

// responseScrubber removes internal scaffolding labels that a model sometimes
// echoes at the very START of its visible answer. The orchestrator feeds tool
// output back into the conversation prefixed with a literal "[Tool results]\n"
// (see runTurnLoop) and a "[Usage] turn N · tool-calls so far M" line; some
// models mimic that transcript framing and begin their reply with e.g.
// "[Tool Results]\nPantesan…" or "[Usage] …\n…". Those labels are internal
// scaffolding and must never reach the user.
//
// Because the visible response is streamed incrementally and a label can be
// split across the first few deltas (e.g. "[", "Tool Results]\nPan", "tesan…"),
// the scrubber buffers only the LEADING bytes of a turn until it can decide
// whether they begin with a known label. Once the decision is made (label
// dropped, or confirmed absent) it becomes a zero-copy pass-through for the
// rest of the turn — it never inspects or withholds anything past the start.
//
// Matching is anchored to start-of-turn (after optional leading whitespace)
// only, so a legitimate "[" appearing later in the answer is never touched.
type responseScrubber struct {
	pending strings.Builder // buffered leading bytes, still undecided
	done    bool            // true once the leading region is resolved
}

// scaffoldLabels are the start-of-answer labels the scrubber removes. They are
// matched case-insensitively. A label may be followed by an optional single
// newline (for the bracketed labels) or it consumes the rest of its line (for
// the "[Usage]" readout, which has trailing content on the same line).
//
// maxLabelProbe bounds how many leading bytes we ever buffer while undecided:
// it must exceed the longest label region we may need to see. The "[Usage]"
// readout runs to its end-of-line ("[Usage] turn N · tool-calls so far M",
// where "·" is a 2-byte rune), so the window is generous enough to contain a
// whole such line before we give up and pass through.
const maxLabelProbe = 96

// feed consumes one visible-text delta and returns the cleaned text to forward
// to the user (possibly empty while the scrubber is still buffering the lead).
func (s *responseScrubber) feed(delta string) string {
	if s.done {
		return delta
	}
	s.pending.WriteString(delta)
	cleaned, resolved := s.tryResolve(s.pending.String(), false)
	if !resolved {
		// Still undecided, but never buffer unbounded: once we have more than
		// maxLabelProbe bytes and still cannot match a label, there is no label
		// here — release everything and pass through from now on.
		if s.pending.Len() > maxLabelProbe {
			s.done = true
			out := s.pending.String()
			s.pending.Reset()
			return out
		}
		return ""
	}
	s.done = true
	s.pending.Reset()
	return cleaned
}

// flush releases any still-buffered leading bytes at end of turn. The label may
// not have completed (e.g. the whole turn was shorter than the probe window),
// so we resolve once more and, if still undecided, emit the buffer verbatim
// rather than swallow real content.
func (s *responseScrubber) flush() string {
	if s.done {
		return ""
	}
	s.done = true
	buf := s.pending.String()
	s.pending.Reset()
	if cleaned, resolved := s.tryResolve(buf, true); resolved {
		return cleaned
	}
	return buf
}

// tryResolve inspects the accumulated leading text and reports whether a
// keep/drop decision can be made yet. When resolved=true the returned string is
// the cleaned leading text (label removed if one matched, otherwise the input
// unchanged). When resolved=false the caller should keep buffering.
func (s *responseScrubber) tryResolve(buf string, atEnd bool) (cleaned string, resolved bool) {
	// Preserve and skip leading whitespace before the label probe, but keep it
	// so a label-less answer that starts with a newline is untouched.
	trimmedLeft := strings.TrimLeft(buf, " \t\r\n")
	prefixWS := buf[:len(buf)-len(trimmedLeft)]
	lower := strings.ToLower(trimmedLeft)

	// Bracketed labels: "[tool results]" / "[tool result]" optionally followed
	// by a single newline.
	for _, lbl := range []string{"[tool results]", "[tool result]"} {
		if strings.HasPrefix(lower, lbl) {
			rest := trimmedLeft[len(lbl):]
			// The label is followed by an OPTIONAL single newline. While
			// streaming we must not resolve before that newline could still
			// arrive, or the swallowed "\n" leaks through as the first visible
			// byte. So if the trailing bytes are empty or a bare "\r" (a
			// possible start of "\r\n"), keep buffering — unless the turn has
			// ended, in which case strip what we have.
			if !atEnd && (rest == "" || rest == "\r") {
				return "", false
			}
			rest = strings.TrimPrefix(rest, "\r\n")
			rest = strings.TrimPrefix(rest, "\n")
			return rest, true
		}
		// Could still BECOME this label as more bytes arrive.
		if len(lower) < len(lbl) && strings.HasPrefix(lbl, lower) {
			return "", false
		}
	}

	// "[usage]" readout consumes to end of its line (it has trailing content on
	// the same line, e.g. "[Usage] turn 3 · tool-calls so far 2").
	const usage = "[usage]"
	if strings.HasPrefix(lower, usage) {
		if nl := strings.IndexByte(trimmedLeft, '\n'); nl >= 0 {
			rest := trimmedLeft[nl+1:]
			return rest, true
		}
		// Label seen but its line hasn't ended yet. Keep buffering for the
		// newline (bounded by maxLabelProbe in feed). At end-of-turn there is
		// no more text, so the whole buffer was just the echoed usage label —
		// drop it entirely.
		if atEnd {
			return "", true
		}
		return "", false
	}
	if len(lower) < len(usage) && strings.HasPrefix(usage, lower) {
		return "", false
	}

	// Not a label and cannot become one: pass the original buffer through
	// (whitespace prefix preserved).
	_ = prefixWS
	return buf, true
}
