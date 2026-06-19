package vault

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

func LogPath(dataDir string) string {
	return fmt.Sprintf("%s/vault/tool-calls.jsonl", dataDir)
}

func ReadEntries(path string, limit int) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var entries []Entry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var entry Entry
		if err := json.Unmarshal(line, &entry); err != nil {
			return entries, fmt.Errorf("parse vault line: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return entries, err
	}
	if limit > 0 && len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	return entries, nil
}

type Stats struct {
	Total   int            `json:"total"`
	ByReason map[string]int `json:"by_reason"`
	ByProvider map[string]int `json:"by_provider"`
	TopTools []ToolCount    `json:"top_tools"`
}

type ToolCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func StatsFor(entries []Entry) Stats {
	stats := Stats{
		Total:      len(entries),
		ByReason:   map[string]int{},
		ByProvider: map[string]int{},
	}
	tools := map[string]int{}
	for _, e := range entries {
		stats.ByReason[e.Reason]++
		stats.ByProvider[e.Provider]++
		name := e.ResolvedName
		if name == "" {
			name = e.RawName
		}
		tools[name]++
	}
	type pair struct {
		name  string
		count int
	}
	var ranked []pair
	for name, count := range tools {
		ranked = append(ranked, pair{name, count})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].count == ranked[j].count {
			return ranked[i].name < ranked[j].name
		}
		return ranked[i].count > ranked[j].count
	})
	limit := len(ranked)
	if limit > 10 {
		limit = 10
	}
	for i := 0; i < limit; i++ {
		stats.TopTools = append(stats.TopTools, ToolCount{Name: ranked[i].name, Count: ranked[i].count})
	}
	return stats
}
