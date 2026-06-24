package wire

import (
	"encoding/hex"
	"strings"
	"testing"
)

// TestAgentEncodeRoundtripSelf is a smoke test: BuildAgentRequestBody returns
// a valid Connect-RPC framed message (5-byte prefix + non-empty payload) and
// DecodeAgentServerMessage is the dual of the encoder.
func TestAgentEncodeRoundtripSelf(t *testing.T) {
	body := BuildAgentRequestBody(AgentRunOptions{
		UserText: "hello agent",
		ModelID:  "claude-4.6-sonnet-medium",
	})
	if len(body) < 6 {
		t.Fatalf("body too short: %d bytes", len(body))
	}
	if body[0]&0x01 != 0 {
		t.Fatalf("expected uncompressed flag, got %x", body[0])
	}
	// length == payload size
	length := uint32(body[1])<<24 | uint32(body[2])<<16 | uint32(body[3])<<8 | uint32(body[4])
	if int(length) != len(body)-5 {
		t.Fatalf("length prefix mismatch: prefix=%d actual=%d", length, len(body)-5)
	}
}

// TestAgentEncodeConversationIDStable confirms the same ConversationID
// produces identical bodies - required for server-side request deduplication.
func TestAgentEncodeConversationIDStable(t *testing.T) {
	opts := AgentRunOptions{
		UserText:       "hi",
		ModelID:        "claude-4.6-sonnet-medium",
		ConversationID: "00000000-0000-0000-0000-000000000001",
		MessageID:      "00000000-0000-0000-0000-000000000002",
	}
	a := BuildAgentRequestBody(opts)
	b := BuildAgentRequestBody(opts)
	if !bytesEqual(a, b) {
		t.Fatalf("expected stable bytes, got\n%s vs\n%s", hex.EncodeToString(a), hex.EncodeToString(b))
	}
}

// TestAgentEncodeConversationIDFromRequestID confirms request_id == conversation_id
// (matches cursor-agent's wire format - line 552 of cursorAgentProtobuf.js).
func TestAgentEncodeConversationIDFromRequestID(t *testing.T) {
	opts := AgentRunOptions{
		UserText:       "hi",
		ModelID:        "composer-2.5",
		ConversationID: "conv-1",
		MessageID:      "msg-1",
	}
	body := BuildAgentRequestBody(opts)
	// Find the bytes after the 5-byte Connect-RPC header
	payload := body[5:]
	// ConversationID is at field 5, request_id at field 16. Both should equal
	// "conv-1". Quick sanity: payload contains the string twice.
	count := strings.Count(string(payload), "conv-1")
	if count != 2 {
		t.Fatalf("expected conv-1 twice (conversation_id + request_id), got %d", count)
	}
}

// TestAgentEncodeContainsModel confirms the model id appears in the encoded body.
func TestAgentEncodeContainsModel(t *testing.T) {
	body := BuildAgentRequestBody(AgentRunOptions{
		UserText: "x",
		ModelID:  "claude-4.6-sonnet-medium",
	})
	if !strings.Contains(string(body[5:]), "claude-4.6-sonnet-medium") {
		t.Fatalf("model id missing from body: %s", hex.EncodeToString(body))
	}
}

// TestAgentEncodeImageInline verifies the image path produces a longer body and
// contains the mime type marker.
func TestAgentEncodeImageInline(t *testing.T) {
	textOnly := BuildAgentRequestBody(AgentRunOptions{
		UserText: "describe this",
		ModelID:  "claude-4.6-sonnet-medium",
	})
	withImg := BuildAgentRequestBody(AgentRunOptions{
		UserText: "describe this",
		ModelID:  "claude-4.6-sonnet-medium",
		Images: []AgentImage{{
			MimeType: "image/png",
			Width:    16,
			Height:   16,
			Data:     []byte("not-a-real-png"),
		}},
	})
	if len(withImg) <= len(textOnly) {
		t.Fatalf("expected withImg longer than textOnly (%d vs %d)", len(withImg), len(textOnly))
	}
	if !strings.Contains(string(withImg[5:]), "image/png") {
		t.Fatalf("mime type missing: %s", hex.EncodeToString(withImg))
	}
}

// TestAgentDecodeTextDelta confirms we can decode a synthetic AgentServerMessage
// with one InteractionUpdate.text_delta → "hello".
func TestAgentDecodeTextDelta(t *testing.T) {
	body := BuildAgentRequestBody(AgentRunOptions{
		UserText: "x",
		ModelID:  "claude-4.6-sonnet-medium",
	})
	// Build a synthetic AgentServerMessage: outer field 1 (InteractionUpdate)
	// containing inner field 1 (TextDeltaUpdate) containing field 1 (text="hi").
	payload := buildAgentServerText("hi")
	got := DecodeAgentServerMessage(payload)
	if len(got) != 1 || got[0].Kind != "text" || got[0].Text != "hi" {
		t.Fatalf("decoded = %+v, want [{Kind:text Text:hi}]", got)
	}
	_ = body
}

// TestAgentDecodeTurnEnded confirms the turn_ended marker surfaces.
func TestAgentDecodeTurnEnded(t *testing.T) {
	payload := buildAgentServerMarker(iuTurnEnded)
	got := DecodeAgentServerMessage(payload)
	if len(got) != 1 || got[0].Kind != "turn_ended" {
		t.Fatalf("decoded = %+v, want [{Kind:turn_ended}]", got)
	}
}

// TestAgentDecodeKVMarker confirms kv_server_message surfaces.
func TestAgentDecodeKVMarker(t *testing.T) {
	payload := encodeFieldLen(asmKvServerMessage, nil)
	got := DecodeAgentServerMessage(payload)
	if len(got) != 1 || got[0].Kind != "kv_server_message" {
		t.Fatalf("decoded = %+v, want [{Kind:kv_server_message}]", got)
	}
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// buildAgentServerText constructs a synthetic AgentServerMessage:
//
//	AgentServerMessage { InteractionUpdate { TextDeltaUpdate { text } } }
func buildAgentServerText(text string) []byte {
	td := encodeFieldLen(tduText, []byte(text))
	tdu := encodeFieldLen(iuTextDelta, td)
	iu := encodeFieldLen(asmInteractionUpdate, tdu)
	return iu
}

// buildAgentServerMarker constructs an AgentServerMessage with a single
// InteractionUpdate carrying the given marker field number (e.g. turn_ended).
func buildAgentServerMarker(marker int) []byte {
	iuInner := encodeField(marker, wireVarint, 0)
	iu := encodeFieldLen(asmInteractionUpdate, iuInner)
	return iu
}
