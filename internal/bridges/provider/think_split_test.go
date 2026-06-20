package provider

import "testing"

// drain runs the splitter over a sequence of chunks, then flushes, and
// concatenates the thinking and response output separately.
func drain(chunks []string) (thinking, response string) {
	var s thinkSplitter
	collect := func(segs []thinkSegment) {
		for _, seg := range segs {
			if seg.thinking {
				thinking += seg.text
			} else {
				response += seg.text
			}
		}
	}
	for _, c := range chunks {
		collect(s.push(c))
	}
	collect(s.flush())
	return thinking, response
}

func TestThinkSplitterNoTags(t *testing.T) {
	think, resp := drain([]string{"hello ", "world"})
	if think != "" {
		t.Fatalf("expected no thinking, got %q", think)
	}
	if resp != "hello world" {
		t.Fatalf("response = %q", resp)
	}
}

func TestThinkSplitterSingleChunk(t *testing.T) {
	think, resp := drain([]string{"<think>reasoning here</think>Hai hai!"})
	if think != "reasoning here" {
		t.Fatalf("thinking = %q", think)
	}
	if resp != "Hai hai!" {
		t.Fatalf("response = %q", resp)
	}
}

func TestThinkSplitterTagAcrossChunks(t *testing.T) {
	// Open tag split as "<thi" + "nk>", close tag split as "</thi" + "nk>".
	think, resp := drain([]string{"<thi", "nk>plan", " more", "</thi", "nk>answer"})
	if think != "plan more" {
		t.Fatalf("thinking = %q", think)
	}
	if resp != "answer" {
		t.Fatalf("response = %q", resp)
	}
}

func TestThinkSplitterMultipleBlocks(t *testing.T) {
	think, resp := drain([]string{"a<think>x</think>b", "<think>y</think>c"})
	if think != "xy" {
		t.Fatalf("thinking = %q", think)
	}
	if resp != "abc" {
		t.Fatalf("response = %q", resp)
	}
}

func TestThinkSplitterUnclosedFlushesAsThinking(t *testing.T) {
	think, resp := drain([]string{"intro <think>still thinking"})
	if think != "still thinking" {
		t.Fatalf("thinking = %q", think)
	}
	if resp != "intro " {
		t.Fatalf("response = %q", resp)
	}
}

func TestThinkSplitterDanglingPartialTagIsText(t *testing.T) {
	// A "<" that never becomes a tag must surface as visible text on flush.
	think, resp := drain([]string{"price < ", "5 dollars"})
	if think != "" {
		t.Fatalf("thinking = %q", think)
	}
	if resp != "price < 5 dollars" {
		t.Fatalf("response = %q", resp)
	}
}
