package wire

import (
	"encoding/binary"
	"fmt"
	"math"
)

// Exec wire field numbers (cursor-agent / agent.proto).
const (
	esmWriteArgs           = 3
	esmDeleteArgs          = 4
	esmGrepArgs            = 5
	esmLsArgs              = 8
	esmDiagnosticsArgs     = 9
	esmMCPArgs             = 11
	esmShellStreamArgs     = 14
	esmBackgroundShellSpawn = 16
	esmFetchArgs           = 20
	esmWriteShellStdinArgs = 23

	ecmShellResult              = 2
	ecmWriteResult              = 3
	ecmDeleteResult             = 4
	ecmGrepResult               = 5
	ecmReadResult               = 7
	ecmLsResult                 = 8
	ecmDiagnosticsResult        = 9
	ecmMCPResult                = 11
	ecmBackgroundShellSpawnRes  = 16
	ecmFetchResult              = 20
	ecmWriteShellStdinResult    = 23

	argPath            = 1
	argShellCommand    = 1
	argShellWorkingDir = 2
	argFetchURL        = 1

	rejPath   = 1
	rejReason = 2

	srejCommand    = 1
	srejWorkingDir = 2
	srejReason     = 3

	errMessage = 1
	ferrURL    = 1
	ferrError  = 2

	resRejected = 2

	mcaName         = 1
	mcaArgs         = 2
	mcaToolCallID   = 3
	mcaToolName     = 5

	mcrSuccess = 1
	mcrError   = 2
	mcsContent = 1
	mcsIsError = 2
	mccText    = 1
	mtcText    = 1
)

const builtinToolRejectReason = "Tool not available in this environment. Use the MCP tools provided instead."

// ExecServerEvent is one ExecServerMessage variant from the Agent API stream.
type ExecServerEvent struct {
	Kind       string
	ExecMsgID  int
	ExecID     string
	Path       string
	Command    string
	WorkingDir string
	URL        string
	ToolName   string
	ToolCallID string
	Args       map[string]any
}

// DecodeExecServerEvent parses ExecServerMessage variants from one Connect payload.
func DecodeExecServerEvent(payload []byte) (ExecServerEvent, bool) {
	for _, f := range decodeMessage(payload)[asmExecServerMessage] {
		if f.wireType != wireLen {
			continue
		}
		body, _ := f.value.([]byte)
		ev := ExecServerEvent{}
		inner := decodeMessage(body)
		for _, x := range inner[esmID] {
			if x.wireType == wireVarint {
				if v, ok := x.value.(uint64); ok {
					ev.ExecMsgID = int(v)
				}
			}
		}
		for _, x := range inner[esmExecID] {
			if x.wireType == wireLen {
				if b, ok := x.value.([]byte); ok {
					ev.ExecID = string(b)
				}
			}
		}
		variantField, variantBytes, hasVariant := execVariantField(inner)
		if !hasVariant {
			continue
		}
		switch variantField {
		case esmRequestContextArgs:
			ev.Kind = "exec_request_context"
		case esmReadArgs:
			ev.Kind = "exec_read"
			ev.Path = readStringField(variantBytes, argPath)
		case esmWriteArgs:
			ev.Kind = "exec_write"
			ev.Path = readStringField(variantBytes, argPath)
		case esmDeleteArgs:
			ev.Kind = "exec_delete"
			ev.Path = readStringField(variantBytes, argPath)
		case esmLsArgs:
			ev.Kind = "exec_ls"
			ev.Path = readStringField(variantBytes, argPath)
		case esmGrepArgs:
			ev.Kind = "exec_grep"
		case esmDiagnosticsArgs:
			ev.Kind = "exec_diagnostics"
		case esmShellArgs:
			ev.Kind = "exec_shell"
			ev.Command = readStringField(variantBytes, argShellCommand)
			ev.WorkingDir = readStringField(variantBytes, argShellWorkingDir)
		case esmShellStreamArgs:
			ev.Kind = "exec_shell_stream"
			ev.Command = readStringField(variantBytes, argShellCommand)
			ev.WorkingDir = readStringField(variantBytes, argShellWorkingDir)
		case esmBackgroundShellSpawn:
			ev.Kind = "exec_bg_shell"
			ev.Command = readStringField(variantBytes, argShellCommand)
			ev.WorkingDir = readStringField(variantBytes, argShellWorkingDir)
		case esmFetchArgs:
			ev.Kind = "exec_fetch"
			ev.URL = readStringField(variantBytes, argFetchURL)
		case esmWriteShellStdinArgs:
			ev.Kind = "exec_write_shell_stdin"
		case esmMCPArgs:
			ev.Kind = "exec_mcp"
			ev.Args = decodeMCPArgs(variantBytes)
			ev.ToolName = readStringField(variantBytes, mcaToolName)
			if ev.ToolName == "" {
				ev.ToolName = readStringField(variantBytes, mcaName)
			}
			ev.ToolCallID = readStringField(variantBytes, mcaToolCallID)
		default:
			return ExecServerEvent{}, false
		}
		return ev, true
	}
	return ExecServerEvent{}, false
}

func execVariantField(inner map[int][]fieldValue) (fieldNum int, body []byte, ok bool) {
	best := 0
	for num, fields := range inner {
		if num == esmID || num == esmExecID {
			continue
		}
		for _, f := range fields {
			if f.wireType != wireLen {
				continue
			}
			if best == 0 || num < best {
				best = num
				body, _ = f.value.([]byte)
				ok = true
			}
		}
	}
	return best, body, ok
}

func decodeMCPArgs(buf []byte) map[string]any {
	out := map[string]any{}
	for _, f := range decodeMessage(buf)[mcaArgs] {
		if f.wireType != wireLen {
			continue
		}
		entry, _ := f.value.([]byte)
		key := ""
		var valueBytes []byte
		for fieldNum, entries := range decodeMessage(entry) {
			for _, e := range entries {
				if e.wireType != wireLen {
					continue
				}
				b, _ := e.value.([]byte)
				switch fieldNum {
				case mapKey:
					key = string(b)
				case mapValue:
					valueBytes = b
				}
			}
		}
		if key != "" && valueBytes != nil {
			out[key] = decodeProtobufValue(valueBytes)
		}
	}
	return out
}

func decodeProtobufValue(buf []byte) any {
	msg := decodeMessage(buf)
	for fieldNum, fields := range msg {
		for _, f := range fields {
			switch fieldNum {
			case valNull:
				return nil
			case valNumber:
				if f.wireType == 1 {
					if b, ok := f.value.([]byte); ok && len(b) >= 8 {
						return math.Float64frombits(binary.LittleEndian.Uint64(b))
					}
				}
				return float64(0)
			case valString:
				if f.wireType == wireLen {
					if b, ok := f.value.([]byte); ok {
						return string(b)
					}
				}
				return ""
			case valBool:
				if f.wireType == wireVarint {
					if v, ok := f.value.(uint64); ok {
						return v != 0
					}
				}
				return false
			case valStruct:
				if f.wireType == wireLen {
					if b, ok := f.value.([]byte); ok {
						return decodeProtobufStruct(b)
					}
				}
				return map[string]any{}
			case valList:
				if f.wireType == wireLen {
					if b, ok := f.value.([]byte); ok {
						return decodeProtobufList(b)
					}
				}
				return []any{}
			}
		}
	}
	return nil
}

func decodeProtobufStruct(buf []byte) map[string]any {
	out := map[string]any{}
	for _, f := range decodeMessage(buf)[structFields] {
		if f.wireType != wireLen {
			continue
		}
		entry, _ := f.value.([]byte)
		key := ""
		var valueBytes []byte
		for fieldNum, entries := range decodeMessage(entry) {
			for _, e := range entries {
				if e.wireType != wireLen {
					continue
				}
				b, _ := e.value.([]byte)
				switch fieldNum {
				case mapKey:
					key = string(b)
				case mapValue:
					valueBytes = b
				}
			}
		}
		if key != "" && valueBytes != nil {
			out[key] = decodeProtobufValue(valueBytes)
		}
	}
	return out
}

func decodeProtobufList(buf []byte) []any {
	var out []any
	for _, f := range decodeMessage(buf)[listValues] {
		if f.wireType != wireLen {
			continue
		}
		if b, ok := f.value.([]byte); ok {
			out = append(out, decodeProtobufValue(b))
		}
	}
	return out
}

func wrapExecClientMessage(execMsgID int, execID string, resultField int, resultPayload []byte) []byte {
	ecm := encodeFieldLen(acmExecClientMessage, concat(
		encodeField(ecmID, wireVarint, uint64(execMsgID)),
		encodeField(ecmExecID, wireLen, execID),
		encodeFieldLen(resultField, resultPayload),
	))
	return WrapConnectFrame(ecm, false)
}

func encodePathRejection(path, reason string) []byte {
	return concat(
		encodeField(rejPath, wireLen, path),
		encodeField(rejReason, wireLen, reason),
	)
}

func encodeShellRejection(command, workingDir, reason string) []byte {
	return concat(
		encodeField(srejCommand, wireLen, command),
		encodeField(srejWorkingDir, wireLen, workingDir),
		encodeField(srejReason, wireLen, reason),
	)
}

// BuildExecRejection returns a Connect frame rejecting a built-in exec request.
func BuildExecRejection(ev ExecServerEvent) ([]byte, bool) {
	reason := builtinToolRejectReason
	switch ev.Kind {
	case "exec_request_context", "exec_mcp":
		return nil, false
	case "exec_read":
		return wrapExecClientMessage(ev.ExecMsgID, ev.ExecID, ecmReadResult, encodeFieldLen(resRejected, encodePathRejection(ev.Path, reason))), true
	case "exec_write":
		return wrapExecClientMessage(ev.ExecMsgID, ev.ExecID, ecmWriteResult, encodeFieldLen(resRejected, encodePathRejection(ev.Path, reason))), true
	case "exec_delete":
		return wrapExecClientMessage(ev.ExecMsgID, ev.ExecID, ecmDeleteResult, encodeFieldLen(resRejected, encodePathRejection(ev.Path, reason))), true
	case "exec_ls":
		return wrapExecClientMessage(ev.ExecMsgID, ev.ExecID, ecmLsResult, encodeFieldLen(resRejected, encodePathRejection(ev.Path, reason))), true
	case "exec_grep":
		return wrapExecClientMessage(ev.ExecMsgID, ev.ExecID, ecmGrepResult, encodeFieldLen(resRejected, encodeField(errMessage, wireLen, reason))), true
	case "exec_diagnostics":
		return wrapExecClientMessage(ev.ExecMsgID, ev.ExecID, ecmDiagnosticsResult, nil), true
	case "exec_shell", "exec_shell_stream":
		return wrapExecClientMessage(ev.ExecMsgID, ev.ExecID, ecmShellResult, encodeFieldLen(resRejected, encodeShellRejection(ev.Command, ev.WorkingDir, reason))), true
	case "exec_bg_shell":
		return wrapExecClientMessage(ev.ExecMsgID, ev.ExecID, ecmBackgroundShellSpawnRes, encodeFieldLen(resRejected, encodeShellRejection(ev.Command, ev.WorkingDir, reason))), true
	case "exec_fetch":
		body := concat(encodeField(ferrURL, wireLen, ev.URL), encodeField(ferrError, wireLen, reason))
		return wrapExecClientMessage(ev.ExecMsgID, ev.ExecID, ecmFetchResult, encodeFieldLen(resRejected, body)), true
	case "exec_write_shell_stdin":
		return wrapExecClientMessage(ev.ExecMsgID, ev.ExecID, ecmWriteShellStdinResult, encodeFieldLen(resRejected, encodeField(errMessage, wireLen, reason))), true
	default:
		return nil, false
	}
}

// BuildExecMCPResult acks an MCP tool invocation with text content.
func BuildExecMCPResult(execMsgID int, execID, content string, isError bool) []byte {
	textContent := encodeFieldLen(mtcText, []byte(content))
	item := encodeFieldLen(mccText, textContent)
	successParts := encodeFieldLen(mcsContent, item)
	if isError {
		successParts = concat(successParts, encodeField(mcsIsError, wireVarint, 1))
	}
	success := encodeFieldLen(mcrSuccess, successParts)
	return wrapExecClientMessage(execMsgID, execID, ecmMCPResult, success)
}

// BuildExecMCPError acks an MCP tool invocation with an error message.
func BuildExecMCPError(execMsgID int, execID, errMsg string) []byte {
	errBody := encodeField(errMessage, wireLen, errMsg)
	errorVariant := encodeFieldLen(mcrError, errBody)
	return wrapExecClientMessage(execMsgID, execID, ecmMCPResult, errorVariant)
}

func execDedupKey(ev ExecServerEvent) string {
	return fmt.Sprintf("%s:%s:%d", ev.Kind, ev.ExecID, ev.ExecMsgID)
}
