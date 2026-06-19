// Package wire — Cursor Agent API encoder/decoder for
// agent.v1.AgentService/Run. This is the endpoint cursor-agent uses for all
// chat + composer + auto requests (including vision). It speaks to
// `agentn.global.api5.cursor.sh` (or the privacy-preserving equivalent when
// ghostMode is on) and is a client-streaming Connect-RPC.
//
// Field numbers are pinned from the agent.proto descriptor shipped in
// cursor-agent's bundle (cross-checked against the Node reference at
// 9router/open-sse/utils/cursorAgentProtobuf.js).
package wire

import (
	"encoding/binary"
	"strings"

	"github.com/google/uuid"
)

// AgentRunRequest field numbers.
const (
	arrConversationState  = 1
	arrAction             = 2
	arrMCPTools           = 4
	arrConversationID     = 5
	arrRequestedModel     = 9
	arrUnknown12          = 12
	arrRequestID          = 16
	arrMCPToolsInner      = 1 // repeated McpToolDefinition at field 1 of McpTools
)

// AgentClientMessage field numbers (oneof).
const (
	acmRunRequest       = 1
	acmExecClientMessage = 2
	acmKvClientMessage   = 3
)

// ConversationStateStructure field numbers.
const (
	cssRootPrompt = 1
	cssTurns      = 8
)

// ConversationAction field numbers.
const (
	caUserMessageAction = 1
)

// UserMessageAction field numbers.
const (
	umaUserMessage = 1
)

// UserMessage field numbers.
const (
	umText           = 1
	umMessageID      = 2
	umSelectedContext = 3
	umMode           = 4
)

// SelectedContext field numbers.
const (
	scSelectedImages = 1
)

// SelectedImage field numbers.
const (
	siUUID      = 2
	siDimension = 4
	siMimeType  = 7
	siData      = 8
)

// Dimension sub-message fields.
const (
	dimWidth  = 1
	dimHeight = 2
)

// RequestedModel field numbers.
const (
	rmModelID    = 1
	rmParameters = 3
)

// ModelParameter sub-message fields.
const (
	rmpID    = 1
	rmpValue = 2
)

// McpToolDefinition field numbers.
const (
	mtdName              = 1
	mtdDescription       = 2
	mtdInputSchema       = 3
	mtdProviderIdentifier = 4
	mtdToolName          = 5
)

// AgentServerMessage field numbers (oneof).
const (
	asmInteractionUpdate = 1
	asmExecServerMessage = 2
	asmKvServerMessage   = 4
)

// InteractionUpdate field numbers.
const (
	iuTextDelta         = 1
	iuToolCallStarted   = 2
	iuToolCallCompleted = 3
	iuThinkingDelta     = 4
	iuThinkingCompleted = 5
	iuTokenDelta        = 8
	iuHeartbeat         = 13
	iuTurnEnded         = 14
)

// TextDeltaUpdate field numbers.
const (
	tduText = 1
)

// ExecServerMessage envelope fields.
const (
	esmID                   = 1
	esmExecID               = 15
	esmRequestContextArgs   = 10
	esmReadArgs             = 7
	esmShellArgs            = 2
)

// ExecClientMessage envelope fields.
const (
	ecmID                 = 1
	ecmExecID             = 15
	ecmRequestContextRes  = 10
)

// RequestContextResult sub-fields.
const (
	rcrSuccess        = 1
	rcsRequestContext = 1
	rcsTools          = 2
)

// KvServerMessage envelope fields.
const (
	ksmID              = 1
	ksmGetBlobArgs     = 2
	ksmSetBlobArgs     = 3
)

// AgentImage is a decoded image ready to inline-encode in the request.
type AgentImage struct {
	UUID     string
	MimeType string
	Width    int
	Height   int
	Data     []byte
}

// AgentRunOptions controls request encoding. Mirror of the JS encodeAgentRunRequest input.
type AgentRunOptions struct {
	UserText       string
	ModelID        string
	ConversationID string
	MessageID      string
	Tools          []AgentTool
	Images         []AgentImage
}

// AgentTool is an OpenAI-style function tool declaration that the agent
// endpoint will surface via MCP.
type AgentTool struct {
	Name        string
	Description string
	Parameters  map[string]any // JSON Schema
}

// BuildAgentRequestBody wraps encodeAgentRunRequest in a Connect-RPC frame.
func BuildAgentRequestBody(opts AgentRunOptions) []byte {
	return WrapConnectFrame(encodeAgentRunRequest(opts), false)
}

func encodeAgentRunRequest(opts AgentRunOptions) []byte {
	convID := strings.TrimSpace(opts.ConversationID)
	if convID == "" {
		convID = uuid.NewString()
	}
	msgID := strings.TrimSpace(opts.MessageID)
	if msgID == "" {
		msgID = uuid.NewString()
	}

	// selected_context — empty by default, populated when images present.
	// Without the empty placeholder the server accepts the request but never
	// streams a response (matches kaitranntt behavior).
	var selectedCtxParts []byte
	if len(opts.Images) > 0 {
		var imageParts []byte
		for _, img := range opts.Images {
			imageParts = append(imageParts, encodeFieldLen(scSelectedImages, encodeSelectedImageBody(img))...)
		}
		selectedCtxParts = imageParts
	}
	selectedCtx := encodeFieldLen(umSelectedContext, selectedCtxParts)

	// UserMessage { text, message_id, selected_context, mode=1 }
	userMsgParts := concat(
		encodeField(umText, wireLen, opts.UserText),
		encodeField(umMessageID, wireLen, msgID),
		selectedCtx,
		encodeField(umMode, wireVarint, 1),
	)
	userMessage := encodeFieldLen(umaUserMessage, userMsgParts)
	userMessageAction := encodeFieldLen(caUserMessageAction, userMessage)
	action := encodeFieldLen(arrAction, userMessageAction)

	// RequestedModel { model_id }
	reqModel := encodeFieldLen(arrRequestedModel, encodeField(rmModelID, wireLen, opts.ModelID))

	// mcp_tools placeholder is required even when empty.
	mcpTools := encodeFieldLen(arrMCPTools, encodeMCPTools(opts.Tools))

	// AgentRunRequest field order matches cursor-agent wire format.
	arr := concat(
		encodeFieldLen(arrConversationState, nil), // empty ConversationStateStructure
		action,
		mcpTools,
		encodeField(arrConversationID, wireLen, convID),
		reqModel,
		encodeField(arrUnknown12, wireVarint, 0),
		encodeField(arrRequestID, wireLen, convID),
	)
	return encodeFieldLen(acmRunRequest, arr)
}

func encodeSelectedImageBody(img AgentImage) []byte {
	uuidStr := img.UUID
	if uuidStr == "" {
		uuidStr = uuid.NewString()
	}
	parts := concat(encodeField(siUUID, wireLen, uuidStr))
	if img.Width > 0 && img.Height > 0 {
		dim := concat(
			encodeField(dimWidth, wireVarint, uint64(img.Width)),
			encodeField(dimHeight, wireVarint, uint64(img.Height)),
		)
		parts = concat(parts, encodeFieldLen(siDimension, dim))
	}
	if img.MimeType != "" {
		parts = concat(parts, encodeField(siMimeType, wireLen, img.MimeType))
	}
	parts = concat(parts, encodeField(siData, wireLen, img.Data))
	return parts
}

func encodeMCPTools(tools []AgentTool) []byte {
	if len(tools) == 0 {
		// Empty placeholder required.
		return encodeFieldLen(arrMCPToolsInner, nil)
	}
	var parts []byte
	for _, t := range tools {
		parts = append(parts, encodeFieldLen(arrMCPToolsInner, encodeMCPToolDefinitionBody(t))...)
	}
	return parts
}

func encodeMCPToolDefinitionBody(t AgentTool) []byte {
	schemaBytes, _ := jsonSchemaToProtobufValueBytes(t.Parameters)
	parts := concat(
		encodeField(mtdName, wireLen, t.Name),
		encodeField(mtdDescription, wireLen, t.Description),
		encodeField(mtdInputSchema, wireLen, schemaBytes),
	)
	if t.Name != "" {
		parts = concat(parts, encodeField(mtdToolName, wireLen, t.Name))
	}
	return parts
}

// jsonSchemaToProtobufValueBytes encodes a JSON Schema as google.protobuf.Value
// bytes. We only handle object (struct) schemas which is enough for OpenAI
// tool parameters.
func jsonSchemaToProtobufValueBytes(schema map[string]any) ([]byte, error) {
	if schema == nil {
		schema = map[string]any{"type": "object"}
	}
	val, err := encodeProtobufValue(schema)
	if err != nil {
		return nil, err
	}
	return val, nil
}

// BuildSyntheticAgentText constructs a synthetic AgentServerMessage:
//   AgentServerMessage { InteractionUpdate { TextDeltaUpdate { text } } }
// Exported so cross-package tests can build canned responses for httptest
// mock servers.
func BuildSyntheticAgentText(text string) []byte {
	td := encodeFieldLen(tduText, []byte(text))
	tdu := encodeFieldLen(iuTextDelta, td)
	iu := encodeFieldLen(asmInteractionUpdate, tdu)
	return iu
}
const (
	valNull   = 1
	valNumber = 2
	valString = 3
	valBool   = 4
	valStruct = 5
	valList   = 6
)

// proto3 map entry field numbers.
const (
	mapKey   = 1
	mapValue = 2
)

// Struct/list field numbers.
const (
	structFields = 1
	listValues   = 1
)

func encodeProtobufValue(v any) ([]byte, error) {
	switch x := v.(type) {
	case nil:
		return encodeField(valNull, wireVarint, 0), nil
	case bool:
		if x {
			return encodeField(valBool, wireVarint, 1), nil
		}
		return encodeField(valBool, wireVarint, 0), nil
	case float64:
		var buf [8]byte
		binary.LittleEndian.PutUint64(buf[:], uint64(x))
		return append(encodeVarint(uint64((valNumber<<3)|1)), buf[:]...), nil
	case int:
		return encodeProtobufValue(float64(x))
	case string:
		return encodeField(valString, wireLen, x), nil
	case []any:
		var parts []byte
		for _, item := range x {
			b, err := encodeProtobufValue(item)
			if err != nil {
				return nil, err
			}
			parts = append(parts, encodeFieldLen(listValues, b)...)
		}
		return encodeFieldLen(valList, parts), nil
	case map[string]any:
		var parts []byte
		for k, item := range x {
			valBytes, err := encodeProtobufValue(item)
			if err != nil {
				return nil, err
			}
			entry := concat(
				encodeField(mapKey, wireLen, k),
				encodeFieldLen(mapValue, valBytes),
			)
			parts = append(parts, encodeFieldLen(structFields, entry)...)
		}
		return encodeFieldLen(valStruct, parts), nil
	}
	return encodeField(valNull, wireVarint, 0), nil
}

// AgentDecoded is one of the events decoded from an AgentServerMessage frame.
type AgentDecoded struct {
	Kind     string
	Text     string
	Thinking string
	Tokens   int
}

// DecodeAgentServerMessage parses one Connect-RPC payload (post-decompress)
// into AgentDecoded events. Only text/thinking/turn_end/kv markers are
// surfaced — exec channel is out of scope for the MVP driver.
func DecodeAgentServerMessage(payload []byte) []AgentDecoded {
	var out []AgentDecoded
	top := decodeMessage(payload)
	if msgs, ok := top[asmKvServerMessage]; ok {
		for range msgs {
			out = append(out, AgentDecoded{Kind: "kv_server_message"})
		}
	}
	updates, ok := top[asmInteractionUpdate]
	if !ok {
		return out
	}
	for _, u := range updates {
		body, _ := u.value.([]byte)
		if u.wireType != wireLen || body == nil {
			continue
		}
		inner := decodeMessage(body)
		for _, f := range inner[iuTextDelta] {
			if f.wireType != wireLen {
				continue
			}
			b, _ := f.value.([]byte)
			text := readStringField(b, tduText)
			if text != "" {
				out = append(out, AgentDecoded{Kind: "text", Text: text})
			}
		}
		for _, f := range inner[iuThinkingDelta] {
			if f.wireType != wireLen {
				continue
			}
			b, _ := f.value.([]byte)
			text := readStringField(b, tduText)
			if text != "" {
				out = append(out, AgentDecoded{Kind: "thinking", Thinking: text})
			}
		}
		if len(inner[iuThinkingCompleted]) > 0 {
			out = append(out, AgentDecoded{Kind: "thinking_complete"})
		}
		if len(inner[iuToolCallStarted]) > 0 {
			out = append(out, AgentDecoded{Kind: "tool_call_started"})
		}
		if len(inner[iuToolCallCompleted]) > 0 {
			out = append(out, AgentDecoded{Kind: "tool_call_completed"})
		}
		for _, f := range inner[iuTokenDelta] {
			if f.wireType != wireLen {
				continue
			}
			b, _ := f.value.([]byte)
			tokens := readVarintField(b, 1)
			out = append(out, AgentDecoded{Kind: "token_delta", Tokens: tokens})
		}
		if len(inner[iuHeartbeat]) > 0 {
			out = append(out, AgentDecoded{Kind: "heartbeat"})
		}
		if len(inner[iuTurnEnded]) > 0 {
			out = append(out, AgentDecoded{Kind: "turn_ended"})
		}
	}
	return out
}

// DecodeAgentExecRequestContext returns the execMsgId+execId from the first
// ExecServerMessage requesting context (sent right after the init RunRequest).
func DecodeAgentExecRequestContext(payload []byte) (execMsgID int, execID string, ok bool) {
	for _, f := range decodeMessage(payload)[asmExecServerMessage] {
		if f.wireType != wireLen {
			continue
		}
		body, _ := f.value.([]byte)
		inner := decodeMessage(body)
		for _, x := range inner[esmID] {
			if x.wireType == wireVarint {
				if v, ok := x.value.(uint64); ok {
					execMsgID = int(v)
				}
			}
		}
		for _, x := range inner[esmExecID] {
			if x.wireType == wireLen {
				if b, ok := x.value.([]byte); ok {
					execID = string(b)
				}
			}
		}
		for _, x := range inner[esmRequestContextArgs] {
			if x.wireType == wireLen {
				ok = true
			}
		}
	}
	return
}

func readStringField(buf []byte, fieldNum int) string {
	for _, f := range decodeMessage(buf)[fieldNum] {
		if f.wireType != wireLen {
			continue
		}
		if b, ok := f.value.([]byte); ok {
			return string(b)
		}
	}
	return ""
}

func readVarintField(buf []byte, fieldNum int) int {
	for _, f := range decodeMessage(buf)[fieldNum] {
		if f.wireType != wireVarint {
			continue
		}
		if v, ok := f.value.(uint64); ok {
			return int(v)
		}
	}
	return 0
}

// BuildRequestContextResponse acks the request-context with the declared MCP
// tools (matches cursor-agent's expected shape).
func BuildRequestContextResponse(execMsgID int, execID string, tools []AgentTool) []byte {
	var toolsParts []byte
	for _, t := range tools {
		toolsParts = append(toolsParts, encodeFieldLen(rcsTools, encodeMCPToolDefinitionBody(t))...)
	}
	rc := encodeFieldLen(rcsRequestContext, toolsParts)
	success := encodeFieldLen(rcrSuccess, rc)
	ecm := encodeFieldLen(acmExecClientMessage, concat(
		encodeField(ecmID, wireVarint, uint64(execMsgID)),
		encodeField(ecmExecID, wireLen, execID),
		encodeFieldLen(ecmRequestContextRes, success),
	))
	return WrapConnectFrame(ecm, false)
}

// AgentNonPrivacyHost is the Agent API endpoint for non-privacy (ghostMode
// off) sessions. Telemetry/usage is sent to cursor here.
const AgentNonPrivacyHost = "agentn.global.api5.cursor.sh"

// AgentPrivacyHost is the Agent API endpoint for privacy-preserving
// (ghostMode on) sessions. No telemetry is sent to cursor.
const AgentPrivacyHost = "agent.global.api5.cursor.sh"

// AgentHost returns the appropriate Agent API hostname for the given
// ghostMode flag. Privacy mode (ghostMode=true) routes through
// `agent.global.api5.cursor.sh`; non-privacy through
// `agentn.global.api5.cursor.sh`.
//
// Mirrors 9router/src/lib/oauth/constants/oauth.js which defines both
// `agentEndpoint` and `agentNonPrivacyEndpoint` and picks based on the
// user's privacy setting.
func AgentHost(ghostMode bool) string {
	if ghostMode {
		return AgentPrivacyHost
	}
	return AgentNonPrivacyHost
}

// AgentAgentPath is the RPC path cursor's AgentService exposes.
const AgentAgentPath = "/agent.v1.AgentService/Run"

// AgentAgentHost is an alias for AgentNonPrivacyHost kept for backwards
// compatibility with code that referenced the original constant name.
const AgentAgentHost = AgentNonPrivacyHost

// encodeFieldLen is a helper for length-delimited nested messages. It is the
// analogue of encodeField(fieldNum, wireLen, ...) but takes pre-encoded bytes
// directly so callers don't have to allocate an any.
func encodeFieldLen(fieldNum int, body []byte) []byte {
	tag := uint64((fieldNum << 3) | wireLen)
	out := encodeVarint(tag)
	out = append(out, encodeVarint(uint64(len(body)))...)
	out = append(out, body...)
	return out
}
