package provider

import (
	"strings"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/bridge"
)

func TestEstimateTokens(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"abcd", 1},                       // 4 chars → 1 token
		{"abcdefgh", 2},                   // 8 chars → 2 tokens
		{"abc", 1},                        // 3 chars → rounds up to 1
		{strings.Repeat("x", 4000), 1000}, // 4000 chars → 1000 tokens
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := EstimateTokens(bridge.Message{Content: tc.in}); got != tc.want {
				t.Errorf("content %q want %d, got %d", tc.in, tc.want, got)
			}
		})
	}
}

func TestFitMessagesToContextNoop(t *testing.T) {
	msgs := []bridge.Message{
		{Role: "user", Content: "hi"},
	}
	// Window <= 0 disables truncation
	for _, w := range []int{0, -1} {
		got := FitMessagesToContext(msgs, w)
		if len(got) != 1 {
			t.Errorf("window=%d must return input unchanged, got %d", w, len(got))
		}
	}
}

func TestFitMessagesToContextAlreadyFits(t *testing.T) {
	msgs := []bridge.Message{
		{Role: "system", Content: "you are helpful"},
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
	}
	got := FitMessagesToContext(msgs, 1000)
	if len(got) != 3 {
		t.Errorf("short conversation must not be truncated, got %d", len(got))
	}
}

func TestFitMessagesToContextDropsOldest(t *testing.T) {
	// 5 user messages, each 4000 chars (1000 tokens) = 5000 tokens total.
	// Window = 2500 should keep only the last 2-3 messages + system.
	msgs := []bridge.Message{
		{Role: "system", Content: "you are helpful"}, // 5 tokens
	}
	for i := 0; i < 5; i++ {
		msgs = append(msgs, bridge.Message{
			Role:    "user",
			Content: strings.Repeat("x", 4000), // 1000 tokens each
		})
	}
	got := FitMessagesToContext(msgs, 2500)
	if len(got) >= len(msgs) {
		t.Errorf("expected truncation, got %d/%d messages", len(got), len(msgs))
	}
	// System message must always survive
	if got[0].Role != "system" {
		t.Errorf("system message must survive truncation, got %q", got[0].Role)
	}
	// Total must fit
	total := 0
	for _, m := range got {
		total += EstimateTokens(m)
	}
	if total > 2500 {
		t.Errorf("truncated set exceeds window: %d > 2500", total)
	}
}

func TestFitMessagesToContextPreservesRecentContent(t *testing.T) {
	// Truncation drops oldest messages first - recent user turns must
	// survive even when the budget is tight.
	msgs := []bridge.Message{
		{Role: "system", Content: "x"},
		{Role: "user", Content: "old question 1"},
		{Role: "assistant", Content: "old answer 1"},
		{Role: "user", Content: "FINAL_QUESTION"},
		{Role: "assistant", Content: "y"},
	}
	// Set window to keep system + last 2 turns (~6 tokens) but drop the
	// middle turns. With 1-char system, "FINAL_QUESTION" (4 tokens), and
	// "y" (1 token) we need at least 6 tokens of budget.
	got := FitMessagesToContext(msgs, 6)
	// The very latest messages must survive (we keep from the end).
	if got[len(got)-1].Content != "y" {
		t.Errorf("must keep the most recent message, got last=%q", got[len(got)-1].Content)
	}
	// And the FINAL_QUESTION user turn should still be in the kept set
	// (we didn't lose it because we drop from the front).
	found := false
	for _, m := range got {
		if strings.Contains(m.Content, "FINAL_QUESTION") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("FINAL_QUESTION must survive in truncated set, got %+v", got)
	}
	// The oldest "old question 1" must be dropped.
	for _, m := range got {
		if strings.Contains(m.Content, "old question 1") {
			t.Errorf("oldest message must be dropped, got %+v", got)
		}
	}
}

// TestFitMessagesToContextPreservesAllLeadingSystemMessages locks in the
// post-checkpoint requirement: the Ask path stacks several leading system
// messages (persona + runtime + negative guidance + prefetch + skills + the
// checkpoint summary), and FitMessagesToContext must keep ALL of them at the
// front when it trims the body - not just the first one. Dropping any
// scaffolding block would break the next turn (missing persona, missing
// checkpoint summary).
func TestFitMessagesToContextPreservesAllLeadingSystemMessages(t *testing.T) {
	systems := []bridge.Message{
		{Role: "system", Content: "persona"},
		{Role: "system", Content: "runtime context"},
		{Role: "system", Content: "negative guidance"},
		{Role: "system", Content: "[Checkpoint 1 summary]\n..."},
	}
	msgs := append([]bridge.Message{}, systems...)
	for i := 0; i < 5; i++ {
		msgs = append(msgs, bridge.Message{
			Role:    "user",
			Content: strings.Repeat("x", 4000), // 1000 tokens each
		})
	}
	got := FitMessagesToContext(msgs, 2500)
	// Every leading system message must survive, in order, at the front.
	if len(got) < len(systems) {
		t.Fatalf("all leading system messages must survive, got %d/%d", len(got), len(systems))
	}
	for i, want := range systems {
		if got[i].Role != "system" || got[i].Content != want.Content {
			t.Errorf("system[%d] = %+v, want %+v", i, got[i], want)
		}
	}
	// No non-system message may appear before the system prefix.
	for i := 0; i < len(systems); i++ {
		if got[i].Role != "system" {
			t.Errorf("position %d must be system, got %q", i, got[i].Role)
		}
	}
	// Total must still fit the window.
	total := 0
	for _, m := range got {
		total += EstimateTokens(m)
	}
	if total > 2500 {
		t.Errorf("truncated set exceeds window: %d > 2500", total)
	}
}

func TestFitMessagesToContextStrictRejectsOversizedCurrentTurn(t *testing.T) {
	msgs := []bridge.Message{
		{Role: "system", Content: "rules"},
		{Role: "user", Content: strings.Repeat("x", 8000)},
	}
	got, err := FitMessagesToContextStrict(msgs, 1000)
	if err == nil || got != nil {
		t.Fatalf("oversized current input must fail explicitly, got=%v err=%v", got, err)
	}
}

func TestFitMessagesToContextStrictRejectsOversizedSystemPrefix(t *testing.T) {
	msgs := []bridge.Message{
		{Role: "system", Content: strings.Repeat("s", 8000)},
		{Role: "user", Content: "current request"},
	}
	if _, err := FitMessagesToContextStrict(msgs, 1000); err == nil {
		t.Fatal("oversized system prefix must fail explicitly")
	}
}
