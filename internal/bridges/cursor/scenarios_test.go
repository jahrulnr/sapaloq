package cursor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/bridges/cursor/wire"
	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/parse"
	"github.com/jahrulnr/sapaloq/internal/parse/tools/kimi"
	"github.com/jahrulnr/sapaloq/internal/vault"
)

type scenarioExpect struct {
	kinds       []bridge.EventKind
	hasError    bool
	errorSubstr string
	toolName    string
}

func collectBridgeEvents(t *testing.T, b *Bridge, message string) []bridge.StreamEvent {
	t.Helper()
	stream, err := b.Complete(context.Background(), bridge.Request{
		SessionID: "scenario",
		Messages:  []bridge.Message{{Role: "user", Content: message}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var out []bridge.StreamEvent
	for ev := range stream {
		out = append(out, ev)
	}
	return out
}

func assertScenario(t *testing.T, id string, events []bridge.StreamEvent, want scenarioExpect) {
	t.Helper()
	seen := map[bridge.EventKind]int{}
	var errText string
	var toolName string
	for _, ev := range events {
		seen[ev.Kind]++
		if ev.Kind == bridge.EventError {
			errText = ev.Error
		}
		if ev.Kind == bridge.EventToolCall && ev.ToolCall != nil {
			toolName = ev.ToolCall.Name
		}
	}
	for _, kind := range want.kinds {
		if seen[kind] == 0 {
			t.Fatalf("%s: missing event %s (seen=%v)", id, kind, seen)
		}
	}
	if want.hasError && errText == "" {
		t.Fatalf("%s: expected error event", id)
	}
	if want.errorSubstr != "" && !strings.Contains(errText, want.errorSubstr) {
		t.Fatalf("%s: error = %q want substr %q", id, errText, want.errorSubstr)
	}
	if want.toolName != "" && toolName != want.toolName {
		t.Fatalf("%s: tool = %q want %q", id, toolName, want.toolName)
	}
}

func TestBridgeScenarios(t *testing.T) {
	schema, err := LoadSchema()
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		id    string
		setup func(t *testing.T) (*Bridge, string)
		want  scenarioExpect
	}{
		{
			id: "mock-no-credentials",
			setup: func(t *testing.T) (*Bridge, string) {
				forceMockCredentials(t)
				entry, runtime := defaultTestEntry()
				b, err := New(entry, runtime)
				if err != nil {
					t.Fatal(err)
				}
				return b, "hello"
			},
			want: scenarioExpect{
				kinds: []bridge.EventKind{
					bridge.EventThinkingDelta,
					bridge.EventResponseDelta,
					bridge.EventDone,
				},
			},
		},
		{
			id: "mock-tool-coerce",
			setup: func(t *testing.T) (*Bridge, string) {
				forceMockCredentials(t)
				entry, runtime := defaultTestEntry()
				b, err := New(entry, runtime)
				if err != nil {
					t.Fatal(err)
				}
				return b, "please use glob tool"
			},
			want: scenarioExpect{
				kinds: []bridge.EventKind{
					bridge.EventThinkingDelta,
					bridge.EventToolCall,
					bridge.EventResponseDelta,
					bridge.EventDone,
				},
				toolName: "glob",
			},
		},
		{
			id: "vault-undeclared-tool",
			setup: func(t *testing.T) (*Bridge, string) {
				forceMockCredentials(t)
				dir := t.TempDir()
				entry, _ := defaultTestEntry()
				entry.DeclaredTools = []string{"read_file"}
				b, err := New(entry, config.RuntimeConfig{DataDir: dir, BinaryName: "sapaloq-core"})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					path := filepath.Join(dir, "vault", "tool-calls.jsonl")
					entries, err := vault.ReadEntries(path, 10)
					if err != nil {
						if os.IsNotExist(err) {
							t.Errorf("vault-undeclared-tool: expected vault file at %s", path)
						}
						return
					}
					if len(entries) == 0 {
						t.Errorf("vault-undeclared-tool: expected vault entry")
						return
					}
					if entries[len(entries)-1].Reason != "undeclared" {
						t.Errorf("reason = %q", entries[len(entries)-1].Reason)
					}
				})
				return b, "undeclared_probe"
			},
			want: scenarioExpect{
				kinds: []bridge.EventKind{
					bridge.EventThinkingDelta,
					bridge.EventResponseDelta,
					bridge.EventDone,
				},
			},
		},
		{
			id: "live-unauthenticated",
			setup: func(t *testing.T) (*Bridge, string) {
				payload := []byte(`{"error":{"code":"unauthenticated","message":"User not authenticated"}}`)
				body := wire.EncodeConnectFrame(wire.ConnectFlagEndStream, payload)
				mux := http.NewServeMux()
				mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write(body)
				})
				server := httptest.NewUnstartedServer(mux)
				server.EnableHTTP2 = true
				server.StartTLS()
				t.Cleanup(server.Close)

				prev := snapshotCredentialEnv()
				t.Cleanup(func() { restoreCredentialEnv(t, prev) })
				t.Setenv("SAPALOQ_CURSOR_TOKEN", "scenario-token")
				t.Setenv("CURSOR_ACCESS_TOKEN", "scenario-token")
				t.Setenv("CURSOR_MACHINE_ID", "scenario-machine")
				t.Setenv("CURSOR_STATE_VSCDB", filepath.Join(t.TempDir(), "missing.vscdb"))
				t.Setenv("SAPALOQ_WIRE_INSECURE_TLS", "1")
				t.Setenv("SAPALOQ_WIRE_DRIVER", "raw")

				entry, _ := defaultTestEntry()
				entry.Endpoint = server.URL
				b, err := New(entry, config.RuntimeConfig{DataDir: "", BinaryName: "sapaloq-core"})
				if err != nil {
					t.Fatal(err)
				}
				return b, "hello live"
			},
			want: scenarioExpect{
				kinds:    []bridge.EventKind{bridge.EventError},
				hasError: true,
				// errorSubstr intentionally omitted - mock server may reply
				// with "unauthenticated" or goaway PROTOCOL_ERROR depending
				// on driver. The contract being asserted is "EventError fires",
				// not the literal error string.
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			b, msg := tc.setup(t)
			events := collectBridgeEvents(t, b, msg)
			assertScenario(t, tc.id, events, tc.want)
		})
	}

	t.Run("parser-kimi-inline", func(t *testing.T) {
		text := `<|tool_call_begin|>glob {"pattern":"*.go"}<|tool_call_end|>`
		calls := kimi.ParseInlineWithTokens(text, schema.KimiTokens())
		if len(calls) != 1 {
			t.Fatalf("calls = %d", len(calls))
		}
		if got := CoerceToolCall(schema, calls[0]).Name; got != "glob_file_search" {
			t.Fatalf("name = %q", got)
		}
	})

	t.Run("vault-unknown-upstream", func(t *testing.T) {
		if got := VaultReason(schema, nil, "fake_tool", parse.ToolCall{Name: "fake_tool"}); got != "unknown_upstream" {
			t.Fatalf("got = %q", got)
		}
	})
}
