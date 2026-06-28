// Package tooldocs ships one Markdown file per orchestrator tool. The YAML
// frontmatter `description` field is wired into the upstream tool list (OpenAI
// function.description / Claude tool.description) so planners and agents see a
// complete contract at tool-selection time—not only JSON parameter hints.
package tooldocs

import (
	"embed"
	"strings"
)

//go:embed defaults/*.md
var defaultFS embed.FS

var descriptions map[string]string

func init() {
	descriptions = loadAll()
}

func loadAll() map[string]string {
	entries, err := defaultFS.ReadDir("defaults")
	if err != nil {
		return map[string]string{}
	}
	m := make(map[string]string, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		b, err := defaultFS.ReadFile("defaults/" + e.Name())
		if err != nil {
			continue
		}
		m[name] = parseDescription(string(b))
	}
	return m
}

// Description returns the wire-format tool description for name, or "" if unknown.
func Description(name string) string {
	if descriptions == nil {
		return ""
	}
	return descriptions[name]
}

// Names returns every tool name that has an embedded doc file.
func Names() []string {
	out := make([]string, 0, len(descriptions))
	for name := range descriptions {
		out = append(out, name)
	}
	return out
}

// parseDescription extracts the `description` value from YAML frontmatter.
func parseDescription(content string) string {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "---") {
		return content
	}
	rest := strings.TrimPrefix(content, "---")
	rest = strings.TrimLeft(rest, "\r\n")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return strings.TrimSpace(rest)
	}
	fm := rest[:end]
	lines := strings.Split(fm, "\n")
	var inBlock bool
	var buf strings.Builder
	for _, line := range lines {
		if !inBlock {
			if !strings.HasPrefix(line, "description:") {
				continue
			}
			val := strings.TrimSpace(strings.TrimPrefix(line, "description:"))
			if val == "" || val == "|" || val == ">" {
				inBlock = val == "|" || val == ">" || val == ""
				continue
			}
			return trimYAMLQuotes(val)
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if line != "" && line[0] != ' ' && line[0] != '\t' {
			break
		}
		if buf.Len() > 0 {
			buf.WriteByte(' ')
		}
		buf.WriteString(trimmed)
	}
	if s := strings.TrimSpace(buf.String()); s != "" {
		return s
	}
	return ""
}

func trimYAMLQuotes(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
