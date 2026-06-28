package cursor

import (
	"encoding/json"
	"strings"

	"github.com/jahrulnr/sapaloq/internal/parse"
)

// upstreamToDeclared maps Cursor upstream / native tool names (after provider
// aliases) to SapaLOQ orchestrator tool names. See docs/TOOL-MAPPING.md.
var upstreamToDeclared = map[string]string{
	"run_terminal_cmd":  "exec",
	"grep":              "search",
	"glob_file_search":  "glob",
	"write":             "write_file",
	"search_replace":    "edit_file",
	"apply_patch":       "edit_file",
	"strreplace":        "edit_file",
	"codebase_search":   "search",
	"file_search":       "glob",
	"read_lints":        "exec",
	"ask_question":      "request_clarification",
	"webfetch":          "web_fetch",
}

// productToDeclared maps Cursor IDE/CLI product tool names (PascalCase and
// folded) when they bypass provider aliases. Applied after CoerceToolCall.
var productToDeclared = map[string]string{
	"shell":          "exec",
	"grep":           "search",
	"glob":           "glob",
	"read":           "read_file",
	"write":          "write_file",
	"delete":         "delete_file",
	"strreplace":     "edit_file",
	"websearch":      "web_search",
	"webfetch":       "web_fetch",
	"semanticsearch": "search",
	"askquestion":    "request_clarification",
	"readlints":      "exec",
}

// ResolveToolCall applies provider alias coercion, upstream→declared mapping,
// and argument field rewrites. Use this at every bridge ingress before
// VaultReason / EventToolCall emission.
func ResolveToolCall(schema Schema, call parse.ToolCall) parse.ToolCall {
	call = CoerceToolCall(schema, call)
	call = MapToDeclared(call)
	call.Arguments = RemapDeclaredArguments(call.Name, call.Arguments)
	return call
}

func MapToDeclared(call parse.ToolCall) parse.ToolCall {
	key := foldToolName(call.Name)
	if mapped, ok := productToDeclared[key]; ok {
		call.Name = mapped
		return call
	}
	if mapped, ok := upstreamToDeclared[key]; ok {
		call.Name = mapped
		return call
	}
	return call
}

// RemapDeclaredArguments normalizes argument keys from Cursor/upstream shapes to
// the JSON schema SapaLOQ tools expect.
func RemapDeclaredArguments(toolName string, args json.RawMessage) json.RawMessage {
	if len(args) == 0 {
		return json.RawMessage("{}")
	}
	var obj map[string]any
	if err := json.Unmarshal(args, &obj); err != nil {
		return args
	}
	name := foldToolName(toolName)
	switch name {
	case "glob":
		copyStringField(obj, "pattern", "glob_pattern", "target_pattern", "globPattern")
		copyStringField(obj, "path", "target_directory", "targetDirectory")
	case "search":
		copyStringField(obj, "pattern", "query", "search_term", "searchTerm", "regex")
		copyStringField(obj, "path", "target_directory", "targetDirectory")
		copyStringField(obj, "glob", "include", "file_glob")
	case "read_file", "write_file", "create_file", "edit_file", "delete_file":
		copyStringField(obj, "path", "file_path", "filePath", "target_file")
	case "web_search":
		copyStringField(obj, "query", "search_term", "searchTerm", "q")
	case "web_fetch":
		copyStringField(obj, "url", "uri", "href")
	case "exec":
		copyStringField(obj, "command", "cmd", "shell_command")
		copyStringField(obj, "cwd", "working_directory", "workingDirectory", "workdir")
	case "request_clarification":
		copyStringField(obj, "question", "prompt", "message")
	case "list_dir":
		copyStringField(obj, "path", "target_directory", "targetDirectory", "directory")
	}
	b, err := json.Marshal(obj)
	if err != nil {
		return args
	}
	return b
}

func copyStringField(obj map[string]any, dst string, srcKeys ...string) {
	if s, _ := obj[dst].(string); strings.TrimSpace(s) != "" {
		return
	}
	for _, src := range srcKeys {
		if s, ok := obj[src].(string); ok && strings.TrimSpace(s) != "" {
			obj[dst] = strings.TrimSpace(s)
			return
		}
	}
}
