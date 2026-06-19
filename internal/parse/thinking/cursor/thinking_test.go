package cursor

import "testing"

func TestParseCursorThinkingPreservesPreTag(t *testing.T) {
	got := ParseCursorThinking("think first</think>answer")
	if got.Thinking != "think first" || got.Response != "answer" {
		t.Fatalf("unexpected parse: %#v", got)
	}
	if memory := StripForMemory("think first</think>answer"); memory != "answer" {
		t.Fatalf("memory = %q", memory)
	}
}

func TestFinalTag(t *testing.T) {
	got := ParseCursorThinking("pre</think>draft<|final|>final")
	if got.Final != "final" {
		t.Fatalf("final = %q", got.Final)
	}
}
