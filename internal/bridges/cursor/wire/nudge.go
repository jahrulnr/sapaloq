package wire

// Nudge endpoint constants. The default model nudge is a unary RPC used
// to discover the default model name and any UI nudges. It lives on
// `aiserver.v1.AiService/GetDefaultModelNudgeData` under api2.cursor.sh.
//
// We do not currently decode the response shape — Cursor's server picks a
// model id (e.g. "default") and our bridge falls back to the explicit
// model id supplied by the caller. This stub is here so callers (e.g. a
// future "auto-detect default model" flow, or telemetry-free mode
// detection) can call the endpoint with a hand-rolled request envelope.

const (
	// NudgeServicePath is the RPC path cursor's AiService exposes for the
	// default model nudge.
	NudgeServicePath = "/aiserver.v1.AiService/GetDefaultModelNudgeData"

	// NudgeServiceHost is the upstream hostname (same as chat).
	NudgeServiceHost = "api2.cursor.sh"
)

// BuildNudgeRequestBody returns a Connect-RPC framed unary request body for
// GetDefaultModelNudgeData. The request payload is empty (no required
// fields), so the body is the standard 5-byte envelope: 1 byte flag
// (0 = raw protobuf wire format) followed by 4 bytes big-endian uint32
// length (0 = no payload).
//
// Connect-RPC unary framing spec: https://connectrpc.com/docs/protocol/
//   1 byte  : envelope flag (0 = proto, 1 = JSON, 2 = JSON+compression, 6 = gzip+proto)
//   4 bytes : big-endian uint32 length of the message that follows
//   N bytes : the message itself (here: empty)
func BuildNudgeRequestBody() []byte {
	return []byte{
		0x00,                   // flag = 0 (raw protobuf, no compression)
		0x00, 0x00, 0x00, 0x00, // length = 0 (empty payload)
	}
}
