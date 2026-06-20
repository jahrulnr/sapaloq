package provider

import "strings"

// thinkSplitter separates inline <think>...</think> reasoning from the visible
// response across streamed delta chunks. Some providers (e.g. Claude served via
// OpenAI-compatible proxies) emit reasoning inside the normal `content` stream
// wrapped in <think> tags instead of using `reasoning_content`. Without this,
// the tags and reasoning leak into the answer bubble.
//
// The splitter is stateful: tags can straddle chunk boundaries, so a partial
// tag prefix at the end of a chunk is buffered and re-examined when the next
// chunk arrives.
type thinkSplitter struct {
	inThink bool
	// pending holds a trailing run of bytes that could be the start of an
	// open/close tag (e.g. "<thi") so we don't emit it as visible text yet.
	pending string
}

const (
	thinkOpen  = "<think>"
	thinkClose = "</think>"
)

// thinkSegment is one classified slice of streamed text.
type thinkSegment struct {
	text     string
	thinking bool
}

// push consumes a delta chunk and returns the classified segments produced so
// far. Bytes that might be a partial tag are buffered internally and surface on
// a later push (or on Flush).
func (s *thinkSplitter) push(chunk string) []thinkSegment {
	data := s.pending + chunk
	s.pending = ""
	var segs []thinkSegment

	for len(data) > 0 {
		if s.inThink {
			idx := strings.Index(data, thinkClose)
			if idx < 0 {
				// No close tag yet. Emit what's safe, buffer a possible partial
				// close tag tail.
				safe, tail := splitSafeTail(data, thinkClose)
				if safe != "" {
					segs = append(segs, thinkSegment{text: safe, thinking: true})
				}
				s.pending = tail
				return segs
			}
			if idx > 0 {
				segs = append(segs, thinkSegment{text: data[:idx], thinking: true})
			}
			data = data[idx+len(thinkClose):]
			s.inThink = false
			continue
		}

		idx := strings.Index(data, thinkOpen)
		if idx < 0 {
			safe, tail := splitSafeTail(data, thinkOpen)
			if safe != "" {
				segs = append(segs, thinkSegment{text: safe, thinking: false})
			}
			s.pending = tail
			return segs
		}
		if idx > 0 {
			segs = append(segs, thinkSegment{text: data[:idx], thinking: false})
		}
		data = data[idx+len(thinkOpen):]
		s.inThink = true
	}
	return segs
}

// flush emits any buffered tail at end-of-stream. A dangling partial tag is
// treated as ordinary text (visible if outside a think block, reasoning if
// inside one) so nothing is silently dropped.
func (s *thinkSplitter) flush() []thinkSegment {
	if s.pending == "" {
		return nil
	}
	seg := thinkSegment{text: s.pending, thinking: s.inThink}
	s.pending = ""
	return []thinkSegment{seg}
}

// splitSafeTail returns the portion of data that can be emitted now and a tail
// that might be the start of marker. The tail is the longest suffix of data
// that is a proper prefix of marker.
func splitSafeTail(data, marker string) (safe, tail string) {
	max := len(marker) - 1
	if max > len(data) {
		max = len(data)
	}
	for n := max; n > 0; n-- {
		if strings.HasPrefix(marker, data[len(data)-n:]) {
			return data[:len(data)-n], data[len(data)-n:]
		}
	}
	return data, ""
}
