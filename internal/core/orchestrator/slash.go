package orchestrator

import (
	"regexp"
	"sort"
	"strings"

	"github.com/jahrulnr/sapaloq/internal/config"
)

type SlashToken struct {
	Text  string
	Start int
	End   int
}

func FindSlashTokens(message string) []SlashToken {
	var out []SlashToken
	for i, r := range message {
		if r != '/' {
			continue
		}
		if i > 0 {
			prev := message[i-1]
			if prev != ' ' && prev != '\n' && prev != '\t' {
				continue
			}
		}
		end := i + 1
		for end < len(message) && !isSpace(message[end]) {
			end++
		}
		out = append(out, SlashToken{Text: message[i:end], Start: i, End: end})
	}
	return out
}

func MatchRegistry(message string, commands config.CommandsConfig) (config.CommandEntry, bool) {
	entries := append([]config.CommandEntry(nil), commands.WithDefaults().Registry...)
	sort.SliceStable(entries, func(i, j int) bool { return len(entries[i].Prefix) > len(entries[j].Prefix) })
	for _, token := range FindSlashTokens(message) {
		for _, entry := range entries {
			if !entry.Enabled {
				continue
			}
			pattern := entry.Pattern
			if !strings.HasPrefix(pattern, "^") {
				pattern = "^" + pattern
			}
			re, err := regexp.Compile(pattern)
			if err != nil {
				continue
			}
			if re.MatchString(token.Text) {
				return entry, true
			}
		}
	}
	return config.CommandEntry{}, false
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\n' || b == '\t' || b == '\r'
}
