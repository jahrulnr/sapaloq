package provider

import "testing"

func TestParseToolCallLeak(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		wantOK bool
		want   string
	}{
		{
			name:   "valid name+arguments",
			in:     `noise {"name":"search","arguments":{"q":"x"}} more`,
			wantOK: true,
			want:   "search",
		},
		{
			name:   "valid with parameters alias",
			in:     `noise {"name":"echo","parameters":{}} more`,
			wantOK: true,
			want:   "echo",
		},
		{
			name:   "no tool JSON",
			in:     "just a normal response",
			wantOK: false,
		},
		{
			name:   "empty name rejected",
			in:     `{"name":"","arguments":{}}`,
			wantOK: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc2, ok := ParseToolCallLeak(tc.in)
			if ok != tc.wantOK {
				t.Fatalf("ok=%v want %v", ok, tc.wantOK)
			}
			if ok && tc2.Name != tc.want {
				t.Errorf("name=%q want %q", tc2.Name, tc.want)
			}
		})
	}
}

func TestHasReasoningLeak(t *testing.T) {
	if !HasReasoningLeak("hello <|channel|>analysis<|message|>x<|end|>") {
		t.Error("channel tag must be detected")
	}
	if !HasReasoningLeak("hello ¹thinkx⁄think⁄") {
		t.Error("legacy tag must be detected")
	}
	if HasReasoningLeak("plain text") {
		t.Error("plain text must not be flagged")
	}
}

func TestStripReasoningFromText(t *testing.T) {
	thinking, cleaned := StripReasoningFromText("before<|channel|>analysis<|message|>deep<|end|>after")
	if thinking != "deep" {
		t.Errorf("thinking: %q", thinking)
	}
	if cleaned != "beforeafter" {
		t.Errorf("cleaned: %q", cleaned)
	}
	thinking, cleaned = StripReasoningFromText("plain")
	if thinking != "" || cleaned != "plain" {
		t.Errorf("plain text must pass through, got thinking=%q cleaned=%q", thinking, cleaned)
	}
}
