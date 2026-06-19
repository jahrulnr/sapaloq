package wire

import (
	"strings"
	"testing"
)

type wireScenario struct {
	id      string
	build   func() []byte
	wantErr string
	assert  func(t *testing.T, thinking, text string, parts int)
}

func TestWireScenarios(t *testing.T) {
	cases := []wireScenario{
		{
			id: "connect-unauthenticated",
			build: func() []byte {
				payload := []byte(`{"error":{"code":"unauthenticated","message":"User not authenticated"}}`)
				return EncodeConnectFrame(ConnectFlagEndStream, payload)
			},
			wantErr: "unauthenticated",
			assert: func(t *testing.T, thinking, text string, parts int) {
				if parts != 0 {
					t.Fatalf("parts = %d", parts)
				}
			},
		},
		{
			id: "auto-thinking-only",
			build: func() []byte {
				return EncodeConnectFrame(0, BuildResponsePayload("", "planning grep read_file schemas"))
			},
			assert: func(t *testing.T, thinking, text string, parts int) {
				if thinking == "" {
					t.Fatal("expected thinking")
				}
				if text != "" {
					t.Fatalf("text = %q", text)
				}
			},
		},
		{
			id: "auto-response-delta",
			build: func() []byte {
				return EncodeConnectFrame(0, BuildResponsePayload("visible answer", ""))
			},
			assert: func(t *testing.T, thinking, text string, parts int) {
				if text != "visible answer" {
					t.Fatalf("text = %q", text)
				}
			},
		},
		{
			id: "dual-channel-no-collapse",
			build: func() []byte {
				return EncodeConnectFrame(0, BuildResponsePayload("user-visible", "pre-tag native tools"))
			},
			assert: func(t *testing.T, thinking, text string, parts int) {
				if thinking == "" || text == "" {
					t.Fatalf("thinking=%q text=%q", thinking, text)
				}
				if thinking == text {
					t.Fatal("thinking collapsed into response")
				}
			},
		},
		{
			id: "empty-protobuf-extract",
			build: func() []byte {
				return EncodeConnectFrame(0, []byte{0x08, 0x01})
			},
			assert: func(t *testing.T, thinking, text string, parts int) {
				if parts != 0 {
					t.Fatalf("parts = %d", parts)
				}
			},
		},
		{
			id: "multi-frame-stream",
			build: func() []byte {
				return append(
					EncodeConnectFrame(0, BuildResponsePayload("", "step one")),
					EncodeConnectFrame(0, BuildResponsePayload("final", ""))...,
				)
			},
			assert: func(t *testing.T, thinking, text string, parts int) {
				if thinking != "step one" || text != "final" {
					t.Fatalf("thinking=%q text=%q", thinking, text)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			raw := tc.build()
			var thinking, text string
			var parts int
			err := ParseStreamBody(raw, func(part ExtractedPart) {
				parts++
				thinking += part.Thinking
				text += part.Text
			})
			if tc.wantErr != "" {
				if err == nil {
					t.Fatal("expected error")
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if tc.assert != nil {
				tc.assert(t, thinking, text, parts)
			}
		})
	}
}

func TestParseConnectJSONErrorIgnoresProtobuf(t *testing.T) {
	if err := ParseConnectJSONError(BuildResponsePayload("hi", "")); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
}
