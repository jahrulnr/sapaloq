package codex

import (
	"encoding/json"
	"strings"
)

// explainCodexError turns a raw Codex error payload into a single actionable
// line, mirroring cursor.explainStreamError.
//
// WHY: Codex surfaces upstream API failures as `error.message` that is often a
// JSON-encoded string (CONTRACT §3.5). Dumping that raw blob to a user is
// unhelpful, so we best-effort unwrap it and recognize the known-bad cases the
// contract documents. Unknown errors fall back to the trimmed raw message so we
// never lose information.
func explainCodexError(raw string) string {
	msg := unwrapErrorMessage(raw)

	// VERIFIED case (CONTRACT §7): reasoning.effort=minimal is incompatible with
	// the built-in tools. The bridge must never set this combination, but if it
	// leaks through we explain how to fix it.
	if strings.Contains(msg, "reasoning.effort") && strings.Contains(msg, "minimal") && strings.Contains(msg, "tools") {
		return "Codex rejected the turn: model_reasoning_effort=minimal is incompatible " +
			"with built-in tools (web_search/image_gen). Remove the effort override or disable tools."
	}

	if msg == "" {
		return "Codex turn failed with an empty error payload."
	}
	return "Codex turn failed: " + msg
}

// unwrapErrorMessage best-effort extracts a human-readable message from a Codex
// error payload. The payload may be (a) a plain string, or (b) a JSON-encoded
// string holding an OpenAI-style error envelope {"error":{"message":...}}.
func unwrapErrorMessage(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal([]byte(raw), &envelope) == nil {
		if envelope.Error.Message != "" {
			return envelope.Error.Message
		}
		if envelope.Message != "" {
			return envelope.Message
		}
	}

	return raw
}
