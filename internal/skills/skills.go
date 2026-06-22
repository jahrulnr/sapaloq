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
// To interoperate with the widely-used Anthropic/OpenAI "Agent Skills" layout
// (one folder per skill containing a SKILL.md with name/description
// frontmatter), Load ALSO descends one level into subdirectories and parses
// their SKILL.md, and parseFile accepts `name`/`description` as aliases:
//
//	frontend-design/SKILL.md
//	  ---
//	  name: frontend-design
//	  description: Create distinctive, production-grade frontend interfaces...
//	  ---
//	  # Frontend Design ...
//
// `name` fills ID when `id` is absent; `description` is shown above the body and
// — when no explicit `triggers` are given — is mined for trigger keywords so the
// skill still fires on a relevant message. This lets a standard skill folder be
// dropped into ~/SapaLOQ/skills and work as a default without rewriting it.
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
	// Description is the optional one-line "what + when to use" summary from
	// name/description-style frontmatter. It is rendered above the body and, in
	// the absence of explicit triggers, mined for matching keywords.
	Description string

	// name holds the raw `name:` frontmatter value (Anthropic/OpenAI layout). It
	// is kept separate from ID so finalizeSkill can prefer an explicit `id:` and
	// still mine triggers from the original name. Unexported: parse-time only.
	name string
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
			// Anthropic/OpenAI layout: one folder per skill with a SKILL.md
			// inside. Descend one level and parse it; a folder without a
			// readable SKILL.md is simply skipped (never fatal).
			path := filepath.Join(dir, e.Name(), "SKILL.md")
			if sk, ok := parseFile(path); ok {
				out = append(out, sk)
			}
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
			// Recover from a malformed frontmatter that was never closed with a
			// "---": a real Markdown heading (e.g. "# Skill Creator") clearly
			// starts the body, so end the frontmatter here and keep the line as
			// body instead of losing the entire document. A "## name:" style
			// heading is still a key line (handled by parseFrontmatterLine), so
			// only treat headings WITHOUT a colon as the body boundary.
			if isMarkdownHeading(trimmed) && !strings.Contains(trimmed, ":") {
				inFrontmatter = false
				frontmatterDone = true
				bodyLines = append(bodyLines, line)
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
	finalizeSkill(&sk)
	if strings.TrimSpace(sk.ID) == "" {
		return Skill{}, false
	}
	return sk, true
}

// finalizeSkill fills derived fields after raw frontmatter parsing so that a
// standard name/description-style skill (no id, no triggers) still loads and
// fires:
//   - ID falls back to the skill's name, then to its containing folder name.
//   - When no explicit triggers were declared, mine them from name +
//     description so skills.Match can select the skill on a relevant message.
func finalizeSkill(sk *Skill) {
	if strings.TrimSpace(sk.ID) == "" {
		if n := strings.TrimSpace(sk.name); n != "" {
			sk.ID = n
		} else if base := skillFolderName(sk.Path); base != "" {
			sk.ID = base
		}
	}
	if len(sk.Triggers) == 0 {
		sk.Triggers = deriveTriggers(sk.ID, sk.name, sk.Description)
	}
}

// isMarkdownHeading reports whether a line is an ATX Markdown heading ("# ...").
func isMarkdownHeading(line string) bool {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "#") {
		return false
	}
	rest := strings.TrimLeft(line, "#")
	return strings.HasPrefix(rest, " ")
}

// skillFolderName returns the parent folder name for a ".../<folder>/SKILL.md"
// path, or "" for a top-level "<dir>/<name>.md" file. Used as the last-resort
// ID for a folder-style skill whose frontmatter omits both id and name.
func skillFolderName(path string) string {
	if path == "" {
		return ""
	}
	if !strings.EqualFold(filepath.Base(path), "SKILL.md") {
		return ""
	}
	return filepath.Base(filepath.Dir(path))
}

// triggerStopwords are low-signal words excluded from mined triggers so a
// generic description doesn't match nearly every message.
var triggerStopwords = map[string]struct{}{
	"the": {}, "and": {}, "for": {}, "with": {}, "use": {}, "used": {}, "when": {},
	"this": {}, "that": {}, "are": {}, "any": {}, "you": {}, "your": {}, "they": {},
	"its": {}, "from": {}, "into": {}, "via": {}, "per": {}, "all": {}, "new": {},
	"create": {}, "creating": {}, "creates": {}, "update": {}, "updating": {},
	"existing": {}, "should": {}, "specialized": {}, "knowledge": {}, "guide": {},
	"effective": {}, "skill": {}, "skills": {}, "workflows": {}, "tool": {},
	"integrations": {}, "support": {}, "including": {}, "other": {}, "high": {},
	"quality": {}, "production": {}, "grade": {}, "distinctive": {},
}

// deriveTriggers mines short, lowercased keyword triggers from a skill's name
// and description when no explicit triggers were declared. The name (and its
// hyphen/space tokens) are always included; description words are kept when
// they are reasonably specific (length >= 4, not a stopword). The result is
// deduped and bounded to keep matching cheap and precise.
func deriveTriggers(id, name, description string) []string {
	seen := make(map[string]struct{})
	var out []string
	add := func(tok string) {
		tok = strings.ToLower(strings.TrimSpace(strings.Trim(tok, ".,;:()[]\"'`")))
		if tok == "" {
			return
		}
		if _, dup := seen[tok]; dup {
			return
		}
		seen[tok] = struct{}{}
		out = append(out, tok)
	}

	// The name and its tokens are the strongest signal — always include them.
	for _, base := range []string{name, id} {
		base = strings.TrimSpace(base)
		if base == "" {
			continue
		}
		add(base)
		for _, part := range strings.FieldsFunc(base, func(r rune) bool { return r == '-' || r == '_' || r == ' ' }) {
			if len(part) >= 3 {
				add(part)
			}
		}
	}

	// Specific words from the description, capped so a long description doesn't
	// produce an over-broad trigger set.
	const maxFromDescription = 12
	added := 0
	for _, w := range strings.Fields(strings.ToLower(description)) {
		if added >= maxFromDescription {
			break
		}
		w = strings.Trim(w, ".,;:()[]\"'`/")
		if len(w) < 4 {
			continue
		}
		if _, stop := triggerStopwords[w]; stop {
			continue
		}
		before := len(out)
		add(w)
		if len(out) > before {
			added++
		}
	}
	return out
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
	// Tolerate a stray Markdown heading prefix on a key line (some skill files
	// have a slightly malformed frontmatter like "## name: skill-creator").
	line = strings.TrimLeft(line, "# ")
	key, val, ok := strings.Cut(line, ":")
	if !ok {
		return
	}
	key = strings.TrimSpace(strings.ToLower(key))
	val = strings.TrimSpace(val)
	switch key {
	case "id":
		sk.ID = strings.Trim(val, `"'`)
	case "name":
		sk.name = strings.Trim(val, `"'`)
	case "description":
		sk.Description = strings.Trim(val, `"'`)
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

// Render returns a bounded block for one skill: a "### <id>" heading, an
// optional one-line description (the "what + when to use" summary), then the
// body trimmed to the smaller of the skill's MaxBodyLines and globalMaxLines
// (counting non-empty lines). A non-positive cap means "use the other cap".
func (s Skill) Render(globalMaxLines int) string {
	cap := s.MaxBodyLines
	if cap <= 0 || (globalMaxLines > 0 && globalMaxLines < cap) {
		cap = globalMaxLines
	}
	var b strings.Builder
	b.WriteString("### ")
	b.WriteString(s.ID)
	if desc := strings.TrimSpace(s.Description); desc != "" {
		b.WriteString("\n")
		b.WriteString(desc)
	}
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
