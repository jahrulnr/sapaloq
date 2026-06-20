// Package skills loads small, file-driven "skill" snippets from the user's
// config dir and matches them against a turn's user message so the orchestrator
// can inject bounded, relevant guidance into the prompt.
//
// A skill is a Markdown file with a minimal YAML frontmatter block:
//
//	---
//	id: sapaloq-scribe
//	triggers: [catat, note this, simpan catatan]
//	priority: 10
//	maxBodyLines: 40
//	---
//	# When capturing notes
//	- Resolve destination via storage.intents before writing.
//
// Skills are READ-ONLY context. They never grant tools or execute anything.
package skills

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Skill is one parsed skill file.
type Skill struct {
	ID           string
	Triggers     []string
	Priority     int
	MaxBodyLines int
	Body         string
	Path         string
}

// Load walks dir for *.md skill files and parses each one. A missing dir is
// inert (returns nil, nil) so the feature simply does nothing when unconfigured.
// Files without an id, or that fail to parse, are skipped (never fatal).
func Load(dir string) ([]Skill, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Skill
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".md") {
			continue
		}
		path := filepath.Join(dir, name)
		sk, ok := parseFile(path)
		if !ok {
			continue
		}
		out = append(out, sk)
	}
	return out, nil
}

func parseFile(path string) (Skill, bool) {
	f, err := os.Open(path)
	if err != nil {
		return Skill{}, false
	}
	defer f.Close()

	sk := Skill{Path: path}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var bodyLines []string
	inFrontmatter := false
	frontmatterDone := false
	sawFrontmatterOpen := false
	lineNo := 0

	for scanner.Scan() {
		line := scanner.Text()
		lineNo++
		trimmed := strings.TrimSpace(line)

		if lineNo == 1 && trimmed == "---" {
			inFrontmatter = true
			sawFrontmatterOpen = true
			continue
		}
		if inFrontmatter {
			if trimmed == "---" {
				inFrontmatter = false
				frontmatterDone = true
				continue
			}
			parseFrontmatterLine(&sk, trimmed)
			continue
		}
		if frontmatterDone || !sawFrontmatterOpen {
			bodyLines = append(bodyLines, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return Skill{}, false
	}

	sk.Body = strings.TrimSpace(strings.Join(bodyLines, "\n"))
	if strings.TrimSpace(sk.ID) == "" {
		return Skill{}, false
	}
	return sk, true
}

// parseFrontmatterLine handles the tiny subset of YAML we support:
//
//	id: foo
//	priority: 10
//	maxBodyLines: 40
//	triggers: [a, b, c]
//	triggers:
//	  - a
//	  - b
//
// The list form is parsed inline ([..]); the dashed multi-line list is handled
// by recognizing a "triggers:" header followed by "- item" lines. To keep the
// parser stateless/forgiving we treat any standalone "- item" line inside the
// frontmatter as a trigger continuation.
func parseFrontmatterLine(sk *Skill, line string) {
	if line == "" {
		return
	}
	if strings.HasPrefix(line, "- ") {
		// dashed list continuation (assume triggers list)
		val := strings.TrimSpace(strings.TrimPrefix(line, "- "))
		val = strings.Trim(val, `"'`)
		if val != "" {
			sk.Triggers = append(sk.Triggers, val)
		}
		return
	}
	key, val, ok := strings.Cut(line, ":")
	if !ok {
		return
	}
	key = strings.TrimSpace(strings.ToLower(key))
	val = strings.TrimSpace(val)
	switch key {
	case "id":
		sk.ID = strings.Trim(val, `"'`)
	case "priority":
		if n, err := strconv.Atoi(val); err == nil {
			sk.Priority = n
		}
	case "maxbodylines":
		if n, err := strconv.Atoi(val); err == nil {
			sk.MaxBodyLines = n
		}
	case "triggers":
		sk.Triggers = append(sk.Triggers, parseInlineList(val)...)
	}
}

// parseInlineList parses `[a, "b c", d]` into ["a","b c","d"]. An empty or
// header-only value (`triggers:` with the list on following dashed lines)
// returns nil.
func parseInlineList(val string) []string {
	val = strings.TrimSpace(val)
	if val == "" {
		return nil
	}
	val = strings.TrimPrefix(val, "[")
	val = strings.TrimSuffix(val, "]")
	parts := strings.Split(val, ",")
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		p = strings.Trim(p, `"'`)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Match returns the skills whose trigger phrases appear (case-insensitive
// substring) in userMsg, preserving the input order. The caller is responsible
// for sorting/capping.
func Match(skills []Skill, userMsg string) []Skill {
	msg := strings.ToLower(strings.TrimSpace(userMsg))
	if msg == "" {
		return nil
	}
	var out []Skill
	for _, sk := range skills {
		if skillMatches(sk, msg) {
			out = append(out, sk)
		}
	}
	return out
}

func skillMatches(sk Skill, lowerMsg string) bool {
	for _, t := range sk.Triggers {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" {
			continue
		}
		if strings.Contains(lowerMsg, t) {
			return true
		}
	}
	return false
}

// SortByRelevance orders skills by priority (desc) then id (asc) for a stable,
// deterministic selection, and caps the result at max (<=0 means no cap).
func SortByRelevance(skills []Skill, max int) []Skill {
	sorted := make([]Skill, len(skills))
	copy(sorted, skills)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Priority != sorted[j].Priority {
			return sorted[i].Priority > sorted[j].Priority
		}
		return sorted[i].ID < sorted[j].ID
	})
	if max > 0 && len(sorted) > max {
		sorted = sorted[:max]
	}
	return sorted
}

// Render returns a bounded block for one skill: a "### <id>" heading followed by
// the body trimmed to the smaller of the skill's MaxBodyLines and globalMaxLines
// (counting non-empty lines). A non-positive cap means "use the other cap".
func (s Skill) Render(globalMaxLines int) string {
	cap := s.MaxBodyLines
	if cap <= 0 || (globalMaxLines > 0 && globalMaxLines < cap) {
		cap = globalMaxLines
	}
	var b strings.Builder
	b.WriteString("### ")
	b.WriteString(s.ID)
	body := strings.TrimSpace(s.Body)
	if body == "" {
		return b.String()
	}
	lines := strings.Split(body, "\n")
	kept := 0
	for _, ln := range lines {
		if cap > 0 && kept >= cap {
			break
		}
		b.WriteString("\n")
		b.WriteString(ln)
		if strings.TrimSpace(ln) != "" {
			kept++
		}
	}
	return b.String()
}
