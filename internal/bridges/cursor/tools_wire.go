package cursor

import (
	"encoding/json"

	"github.com/jahrulnr/sapaloq/internal/bridges/cursor/wire"
	"github.com/jahrulnr/sapaloq/internal/bridges/provider"
)

func buildWireMCPTools(declared []string) []wire.MCPToolDecl {
	if len(declared) == 0 {
		return nil
	}
	out := make([]wire.MCPToolDecl, 0, len(declared))
	seen := map[string]struct{}{}
	for _, name := range declared {
		name = trimToolName(name)
		if name == "" {
			continue
		}
		key := foldToolName(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		schema := provider.RegisteredToolSchema(name)
		out = append(out, wire.MCPToolDecl{
			Name:           name,
			Description:    provider.RegisteredToolDescription(name),
			ParametersJSON: string(schema),
		})
	}
	return out
}

func trimToolName(name string) string {
	for len(name) > 0 && (name[0] == ' ' || name[0] == '\t') {
		name = name[1:]
	}
	for len(name) > 0 {
		c := name[len(name)-1]
		if c != ' ' && c != '\t' {
			break
		}
		name = name[:len(name)-1]
	}
	return name
}

func declaredToolsForRequest(reqDeclared, entryDeclared []string) []string {
	src := reqDeclared
	if len(src) == 0 {
		src = entryDeclared
	}
	return append([]string(nil), src...)
}

func buildAgentTools(declared []string) []wire.AgentTool {
	mcp := buildWireMCPTools(declared)
	out := make([]wire.AgentTool, 0, len(mcp))
	for _, t := range mcp {
		var params map[string]any
		if t.ParametersJSON != "" {
			_ = json.Unmarshal([]byte(t.ParametersJSON), &params)
		}
		out = append(out, wire.AgentTool{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  params,
		})
	}
	return out
}
