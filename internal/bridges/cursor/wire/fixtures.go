package wire

// BuildResponsePayload encodes a minimal api2 stream message with optional text/thinking.
func BuildResponsePayload(text, thinking string) []byte {
	var inner []byte
	if text != "" {
		inner = append(inner, encodeField(fieldResponseText, wireLen, text)...)
	}
	if thinking != "" {
		thinkingBody := encodeField(fieldThinkingText, wireLen, thinking)
		inner = append(inner, encodeField(fieldThinking, wireLen, thinkingBody)...)
	}
	return encodeField(fieldResponse, wireLen, inner)
}
