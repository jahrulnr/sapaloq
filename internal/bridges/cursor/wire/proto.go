// Package wire implements Cursor api2 Connect+proto framing (subset ported from cursor-proto-lab).
package wire

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	wireVarint = 0
	wireLen    = 2

	roleUser      = 1
	roleAssistant = 2
	modeChat      = 1
	modeAgent     = 2

	fieldRequest         = 1
	fieldMessages        = 1
	fieldUnknown2        = 2
	fieldInstruction     = 3
	fieldUnknown4        = 4
	fieldModel           = 5
	fieldWebTool         = 8
	fieldUnknown13       = 13
	fieldCursorSetting   = 15
	fieldUnknown19       = 19
	fieldConversationID  = 23
	fieldMetadata        = 26
	fieldIsAgentic       = 27
	fieldMessageIDs      = 30
	fieldLargeContext    = 35
	fieldUnknown38       = 38
	fieldUnifiedMode     = 46
	fieldUnknown47       = 47
	fieldShouldDisable   = 48
	fieldThinkingLevel   = 49
	fieldUnknown51       = 51
	fieldUnknown53       = 53
	fieldUnifiedModeName = 54

	fieldMsgContent     = 1
	fieldMsgRole        = 2
	fieldMsgID          = 13
	fieldMsgIsAgentic   = 29
	fieldMsgUnifiedMode = 47

	fieldModelName  = 1
	fieldModelEmpty = 4

	fieldInstructionText = 1

	fieldSettingPath      = 1
	fieldSettingUnknown3  = 3
	fieldSettingUnknown6  = 6
	fieldSettingUnknown8  = 8
	fieldSettingUnknown9  = 9
	fieldSetting6Field1   = 1
	fieldSetting6Field2   = 2

	fieldMetaPlatform   = 1
	fieldMetaArch       = 2
	fieldMetaVersion    = 3
	fieldMetaCWD        = 4
	fieldMetaTimestamp  = 5

	fieldMsgIDID      = 1
	fieldMsgIDRole    = 3

	fieldToolCall       = 1
	fieldResponse       = 2
	fieldToolID         = 3
	fieldToolName       = 9
	fieldToolRawArgs    = 10
	fieldToolMCPParams  = 27
	fieldMCPToolsList   = 1
	fieldMCPNestedName  = 1
	fieldMCPNestedParam = 3
	fieldResponseText   = 1
	fieldThinking       = 25
	fieldThinkingText   = 1
)

type ExtractedPart struct {
	Text      string
	Thinking  string
	ToolCall  *ToolCallPart
	DecodeErr string
}

type ToolCallPart struct {
	ID        string
	Name      string
	Arguments string
}

func encodeVarint(value uint64) []byte {
	var out []byte
	for value >= 0x80 {
		out = append(out, byte((value&0x7F)|0x80))
		value >>= 7
	}
	out = append(out, byte(value&0x7F))
	return out
}

func decodeVarint(buf []byte, offset int) (uint64, int, error) {
	var result uint64
	var shift uint
	pos := offset
	for pos < len(buf) {
		b := buf[pos]
		result |= uint64(b&0x7F) << shift
		pos++
		if b&0x80 == 0 {
			return result, pos, nil
		}
		shift += 7
		if shift > 63 {
			return 0, pos, fmt.Errorf("varint overflow")
		}
	}
	return 0, pos, fmt.Errorf("truncated varint")
}

func encodeField(fieldNum int, wireType int, value any) []byte {
	tag := uint64((fieldNum << 3) | wireType)
	var out []byte
	out = append(out, encodeVarint(tag)...)
	switch wireType {
	case wireVarint:
		out = append(out, encodeVarint(asUint64(value))...)
	case wireLen:
		var data []byte
		switch v := value.(type) {
		case string:
			data = []byte(v)
		case []byte:
			data = v
		default:
			data = []byte{}
		}
		out = append(out, encodeVarint(uint64(len(data)))...)
		out = append(out, data...)
	}
	return out
}

func asUint64(value any) uint64 {
	switch v := value.(type) {
	case uint64:
		return v
	case uint32:
		return uint64(v)
	case int:
		return uint64(v)
	case int32:
		return uint64(v)
	default:
		return 0
	}
}

func concat(parts ...[]byte) []byte {
	return bytes.Join(parts, nil)
}

type fieldValue struct {
	wireType int
	value    any
}

func decodeMessage(data []byte) map[int][]fieldValue {
	fields := map[int][]fieldValue{}
	pos := 0
	for pos < len(data) {
		tag, next, err := decodeVarint(data, pos)
		if err != nil {
			break
		}
		fieldNum := int(tag >> 3)
		wireType := int(tag & 0x07)
		pos = next
		var value any
		switch wireType {
		case wireVarint:
			v, np, err := decodeVarint(data, pos)
			if err != nil {
				return fields
			}
			value = v
			pos = np
		case wireLen:
			length, np, err := decodeVarint(data, pos)
			if err != nil {
				return fields
			}
			pos = np
			end := pos + int(length)
			if end > len(data) {
				return fields
			}
			value = data[pos:end]
			pos = end
		default:
			return fields
		}
		fields[fieldNum] = append(fields[fieldNum], fieldValue{wireType: wireType, value: value})
	}
	return fields
}

func WrapConnectFrame(payload []byte, compress bool) []byte {
	final := payload
	flags := byte(0)
	if compress {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		_, _ = zw.Write(payload)
		_ = zw.Close()
		final = buf.Bytes()
		flags = 0x01
	}
	frame := make([]byte, 5+len(final))
	frame[0] = flags
	frame[1] = byte(len(final) >> 24)
	frame[2] = byte(len(final) >> 16)
	frame[3] = byte(len(final) >> 8)
	frame[4] = byte(len(final))
	copy(frame[5:], final)
	return frame
}

func ParseConnectFrame(buf []byte) (flags byte, payload []byte, consumed int, ok bool) {
	if len(buf) < 5 {
		return 0, nil, 0, false
	}
	flags = buf[0]
	length := int(buf[1])<<24 | int(buf[2])<<16 | int(buf[3])<<8 | int(buf[4])
	if len(buf) < 5+length {
		return 0, nil, 0, false
	}
	payload = buf[5 : 5+length]
	if flags == 0x01 {
		gr, err := gzip.NewReader(bytes.NewReader(payload))
		if err == nil {
			decompressed, err := io.ReadAll(gr)
			_ = gr.Close()
			if err == nil {
				payload = decompressed
			}
		}
	}
	return flags, payload, 5 + length, true
}

func encodeInstruction(text string) []byte {
	if text == "" {
		return nil
	}
	return encodeField(fieldInstructionText, wireLen, text)
}

func encodeModel(model string) []byte {
	return concat(
		encodeField(fieldModelName, wireLen, model),
		encodeField(fieldModelEmpty, wireLen, []byte{}),
	)
}

func encodeCursorSetting() []byte {
	unknown6 := concat(
		encodeField(fieldSetting6Field1, wireLen, []byte{}),
		encodeField(fieldSetting6Field2, wireLen, []byte{}),
	)
	return concat(
		encodeField(fieldSettingPath, wireLen, `cursor\aisettings`),
		encodeField(fieldSettingUnknown3, wireLen, []byte{}),
		encodeField(fieldSettingUnknown6, wireLen, unknown6),
		encodeField(fieldSettingUnknown8, wireVarint, 1),
		encodeField(fieldSettingUnknown9, wireVarint, 1),
	)
}

func encodeMetadata() []byte {
	cwd, _ := os.Getwd()
	if cwd == "" {
		cwd = "/"
	}
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x64"
	}
	return concat(
		encodeField(fieldMetaPlatform, wireLen, runtime.GOOS),
		encodeField(fieldMetaArch, wireLen, arch),
		encodeField(fieldMetaVersion, wireLen, runtime.Version()),
		encodeField(fieldMetaCWD, wireLen, cwd),
		encodeField(fieldMetaTimestamp, wireLen, time.Now().UTC().Format(time.RFC3339)),
	)
}

func encodeMessageID(id string, role int) []byte {
	return concat(
		encodeField(fieldMsgIDID, wireLen, id),
		encodeField(fieldMsgIDRole, wireVarint, uint64(role)),
	)
}

func encodeConversationMessage(content string, role int, messageID string, isLast bool) []byte {
	return concat(
		encodeField(fieldMsgContent, wireLen, content),
		encodeField(fieldMsgRole, wireVarint, uint64(role)),
		encodeField(fieldMsgID, wireLen, messageID),
		encodeField(fieldMsgIsAgentic, wireVarint, 0),
		encodeField(fieldMsgUnifiedMode, wireVarint, uint64(modeChat)),
	)
}

func encodeRequest(messages []ChatMessage, model string) []byte {
	var parts [][]byte
	var messageIDs []struct {
		id   string
		role int
	}
	for i, msg := range messages {
		role := roleUser
		if msg.Role == "assistant" {
			role = roleAssistant
		}
		msgID := uuid.NewString()
		isLast := i == len(messages)-1
		parts = append(parts, encodeField(fieldMessages, wireLen, encodeConversationMessage(msg.Content, role, msgID, isLast)))
		messageIDs = append(messageIDs, struct {
			id   string
			role int
		}{id: msgID, role: role})
	}
	parts = append(parts,
		encodeField(fieldUnknown2, wireVarint, 1),
		encodeField(fieldInstruction, wireLen, encodeInstruction("")),
		encodeField(fieldUnknown4, wireVarint, 1),
		encodeField(fieldModel, wireLen, encodeModel(model)),
		encodeField(fieldWebTool, wireLen, ""),
		encodeField(fieldUnknown13, wireVarint, 1),
		encodeField(fieldCursorSetting, wireLen, encodeCursorSetting()),
		encodeField(fieldUnknown19, wireVarint, 1),
		encodeField(fieldConversationID, wireLen, uuid.NewString()),
		encodeField(fieldMetadata, wireLen, encodeMetadata()),
		encodeField(fieldIsAgentic, wireVarint, 0),
	)
	for _, mid := range messageIDs {
		parts = append(parts, encodeField(fieldMessageIDs, wireLen, encodeMessageID(mid.id, mid.role)))
	}
	parts = append(parts,
		encodeField(fieldLargeContext, wireVarint, 0),
		encodeField(fieldUnknown38, wireVarint, 0),
		encodeField(fieldUnifiedMode, wireVarint, uint64(modeChat)),
		encodeField(fieldUnknown47, wireLen, ""),
		encodeField(fieldShouldDisable, wireVarint, 1),
		encodeField(fieldThinkingLevel, wireVarint, 0),
		encodeField(fieldUnknown51, wireVarint, 0),
		encodeField(fieldUnknown53, wireVarint, 1),
		encodeField(fieldUnifiedModeName, wireLen, "Ask"),
	)
	return concat(parts...)
}

type ChatMessage struct {
	Role    string
	Content string
}

func BuildChatBody(messages []ChatMessage, model string) []byte {
	request := encodeField(fieldRequest, wireLen, encodeRequest(messages, model))
	return WrapConnectFrame(request, false)
}

func extractToolCall(data []byte) *ToolCallPart {
	fields := decodeMessage(data)
	part := &ToolCallPart{}
	if vals, ok := fields[fieldToolID]; ok && len(vals) > 0 {
		if b, ok := vals[0].value.([]byte); ok {
			full := string(b)
			part.ID = strings.SplitN(full, "\n", 2)[0]
		}
	}
	if vals, ok := fields[fieldToolName]; ok && len(vals) > 0 {
		if b, ok := vals[0].value.([]byte); ok {
			part.Name = string(b)
		}
	}
	if vals, ok := fields[fieldToolMCPParams]; ok && len(vals) > 0 {
		if b, ok := vals[0].value.([]byte); ok {
			mcp := decodeMessage(b)
			if tools, ok := mcp[fieldMCPToolsList]; ok && len(tools) > 0 {
				if tb, ok := tools[0].value.([]byte); ok {
					tool := decodeMessage(tb)
					if names, ok := tool[fieldMCPNestedName]; ok && len(names) > 0 {
						if nb, ok := names[0].value.([]byte); ok {
							part.Name = string(nb)
						}
					}
					if params, ok := tool[fieldMCPNestedParam]; ok && len(params) > 0 {
						if pb, ok := params[0].value.([]byte); ok {
							part.Arguments = string(pb)
						}
					}
				}
			}
		}
	}
	if part.Arguments == "" {
		if vals, ok := fields[fieldToolRawArgs]; ok && len(vals) > 0 {
			if b, ok := vals[0].value.([]byte); ok {
				part.Arguments = string(b)
			}
		}
	}
	if part.ID == "" || part.Name == "" {
		return nil
	}
	if part.Arguments == "" {
		part.Arguments = "{}"
	}
	return part
}

func extractTextAndThinking(responseData []byte) (string, string) {
	nested := decodeMessage(responseData)
	var text, thinking string
	if vals, ok := nested[fieldResponseText]; ok && len(vals) > 0 {
		if b, ok := vals[0].value.([]byte); ok {
			text = string(b)
		}
	}
	if vals, ok := nested[fieldThinking]; ok && len(vals) > 0 {
		if b, ok := vals[0].value.([]byte); ok {
			thinkingMsg := decodeMessage(b)
			if tvals, ok := thinkingMsg[fieldThinkingText]; ok && len(tvals) > 0 {
				if tb, ok := tvals[0].value.([]byte); ok {
					thinking = string(tb)
				}
			}
		}
	}
	return text, thinking
}

func ExtractFromPayload(payload []byte) ExtractedPart {
	fields := decodeMessage(payload)
	if vals, ok := fields[fieldToolCall]; ok && len(vals) > 0 {
		if b, ok := vals[0].value.([]byte); ok {
			if tc := extractToolCall(b); tc != nil {
				return ExtractedPart{ToolCall: tc}
			}
		}
	}
	if vals, ok := fields[fieldResponse]; ok && len(vals) > 0 {
		if b, ok := vals[0].value.([]byte); ok {
			text, thinking := extractTextAndThinking(b)
			if text != "" || thinking != "" {
				return ExtractedPart{Text: text, Thinking: thinking}
			}
		}
	}
	return ExtractedPart{}
}

func hashed64Hex(input, salt string) string {
	sum := sha256.Sum256([]byte(input + salt))
	return hex.EncodeToString(sum[:])
}

func sessionIDFromToken(token string) string {
	return uuid.NewSHA1(uuid.NameSpaceDNS, []byte(token)).String()
}

func cursorChecksum(machineID string) string {
	timestamp := time.Now().Unix() / 1_000_000
	bytes := []byte{
		byte(timestamp >> 40),
		byte(timestamp >> 32),
		byte(timestamp >> 24),
		byte(timestamp >> 16),
		byte(timestamp >> 8),
		byte(timestamp),
	}
	key := byte(165)
	for i := range bytes {
		bytes[i] = byte((int(bytes[i])^int(key))+i) & 0xFF
		key = bytes[i]
	}
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	var encoded strings.Builder
	for i := 0; i < len(bytes); i += 3 {
		a := bytes[i]
		b := byte(0)
		c := byte(0)
		if i+1 < len(bytes) {
			b = bytes[i+1]
		}
		if i+2 < len(bytes) {
			c = bytes[i+2]
		}
		encoded.WriteByte(alphabet[a>>2])
		encoded.WriteByte(alphabet[((a&3)<<4)|(b>>4)])
		if i+1 < len(bytes) {
			encoded.WriteByte(alphabet[((b&15)<<2)|(c>>6)])
		}
		if i+2 < len(bytes) {
			encoded.WriteByte(alphabet[c&63])
		}
	}
	return encoded.String() + machineID
}

func BuildHeaders(accessToken, machineID string, ghostMode bool) map[string]string {
	clean := accessToken
	if idx := strings.Index(accessToken, "::"); idx >= 0 {
		clean = accessToken[idx+2:]
	}
	if machineID == "" {
		machineID = hashed64Hex(clean, "machineId")
	}
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x64"
	}
	ghost := "false"
	if ghostMode {
		ghost = "true"
	}
	return map[string]string{
		"authorization":               "Bearer " + clean,
		"connect-accept-encoding":     "gzip",
		"connect-protocol-version":    "1",
		"content-type":                "application/connect+proto",
		"user-agent":                  "connect-es/1.6.1",
		"x-amzn-trace-id":             "Root=" + uuid.NewString(),
		"x-client-key":                hashed64Hex(clean, ""),
		"x-cursor-checksum":           cursorChecksum(machineID),
		"x-cursor-client-version":     "3.1.0",
		"x-cursor-client-type":        "ide",
		"x-cursor-client-os":          runtime.GOOS,
		"x-cursor-client-arch":        arch,
		"x-cursor-client-device-type": "desktop",
		"x-cursor-config-version":     uuid.NewString(),
		"x-cursor-timezone":           "UTC",
		"x-ghost-mode":                ghost,
		"x-request-id":                uuid.NewString(),
		"x-session-id":                sessionIDFromToken(clean),
	}
}

func RandomTraceID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return uuid.NewString()
}
