package llamacpp

import "testing"

func TestNormalizeEndpoint(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", "http://127.0.0.1:8080/v1/chat/completions"},
		{"http://127.0.0.1:8080", "http://127.0.0.1:8080/v1/chat/completions"},
		{"http://127.0.0.1:16285", "http://127.0.0.1:16285/v1/chat/completions"},
		{"http://127.0.0.1:16285/v1/chat/completions", "http://127.0.0.1:16285/v1/chat/completions"},
	}
	for _, tc := range cases {
		if got := NormalizeEndpoint(tc.in); got != tc.want {
			t.Fatalf("NormalizeEndpoint(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestHealthURL(t *testing.T) {
	got := HealthURL("http://127.0.0.1:8080")
	want := "http://127.0.0.1:8080/health"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestModelsLoadURL(t *testing.T) {
	got := ModelsLoadURL("http://127.0.0.1:16285")
	want := "http://127.0.0.1:16285/models/load"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
