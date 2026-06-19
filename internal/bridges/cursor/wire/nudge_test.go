package wire

import (
	"testing"
)

// TestBuildNudgeRequestBodyShape confirms the Connect-RPC unary envelope
// is exactly 5 bytes: 1-byte flag (0) + 4-byte big-endian length (0).
func TestBuildNudgeRequestBodyShape(t *testing.T) {
	body := BuildNudgeRequestBody()
	if len(body) != 5 {
		t.Fatalf("BuildNudgeRequestBody length = %d, want 5 (1 flag + 4 length)", len(body))
	}
	if body[0] != 0x00 {
		t.Fatalf("BuildNudgeRequestBody[0] = %#x, want 0x00 (raw proto flag)", body[0])
	}
	if body[1] != 0x00 || body[2] != 0x00 || body[3] != 0x00 || body[4] != 0x00 {
		t.Fatalf("BuildNudgeRequestBody length prefix = %v, want all zeros", body[1:5])
	}
}

// TestNudgeServiceConstants are smoke checks for the endpoint constants
// — easy to catch typos at build time, no need to do live requests.
func TestNudgeServiceConstants(t *testing.T) {
	if NudgeServicePath != "/aiserver.v1.AiService/GetDefaultModelNudgeData" {
		t.Fatalf("NudgeServicePath = %q, want /aiserver.v1.AiService/GetDefaultModelNudgeData", NudgeServicePath)
	}
	if NudgeServiceHost == "" {
		t.Fatal("NudgeServiceHost must not be empty")
	}
	if NudgeServicePath == "" {
		t.Fatal("NudgeServicePath must not be empty")
	}
}
