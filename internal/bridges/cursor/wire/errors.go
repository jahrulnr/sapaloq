package wire

import (
	"encoding/json"
	"fmt"
	"strings"
)

const ConnectFlagCompressed = 0x01
const ConnectFlagEndStream = 0x02

// EncodeConnectFrame builds a Connect RPC frame (5-byte header + payload).
func EncodeConnectFrame(flags byte, payload []byte) []byte {
	frame := make([]byte, 5+len(payload))
	frame[0] = flags
	length := len(payload)
	frame[1] = byte(length >> 24)
	frame[2] = byte(length >> 16)
	frame[3] = byte(length >> 8)
	frame[4] = byte(length)
	copy(frame[5:], payload)
	return frame
}

// ParseConnectJSONError detects Connect end-stream JSON error bodies from api2.
func ParseConnectJSONError(payload []byte) error {
	trimmed := strings.TrimSpace(string(payload))
	if trimmed == "" || trimmed[0] != '{' {
		return nil
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return nil
	}
	if body.Error.Code == "" && body.Error.Message == "" {
		return nil
	}
	msg := strings.TrimSpace(body.Error.Message)
	if msg == "" {
		msg = body.Error.Code
	}
	if body.Error.Code != "" && msg != body.Error.Code {
		return fmt.Errorf("cursor api error %s: %s", body.Error.Code, msg)
	}
	return fmt.Errorf("cursor api error: %s", msg)
}

// DispatchConnectPayload handles one Connect frame payload.
func DispatchConnectPayload(flags byte, payload []byte, onFrame FrameHandler) error {
	if flags&ConnectFlagEndStream != 0 {
		if err := ParseConnectJSONError(payload); err != nil {
			return err
		}
	}
	if err := ParseConnectJSONError(payload); err != nil {
		return err
	}
	part := ExtractFromPayload(payload)
	if part.Text != "" || part.Thinking != "" || part.ToolCall != nil || part.DecodeErr != "" {
		onFrame(part)
	}
	return nil
}

// ParseStreamBody decodes a concatenated Connect RPC byte stream (offline scenario tests).
func ParseStreamBody(raw []byte, onFrame FrameHandler) error {
	buf := raw
	for len(buf) > 0 {
		_, payload, consumed, ok := ParseConnectFrame(buf)
		if !ok {
			break
		}
		flags := buf[0]
		if err := DispatchConnectPayload(flags, payload, onFrame); err != nil {
			return err
		}
		buf = buf[consumed:]
	}
	return nil
}
