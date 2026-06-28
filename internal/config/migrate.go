package config

import (
	"fmt"
	"strconv"
	"strings"
)

// CurrentSchemaVersion is the schema version this build of SapaLOQ writes and
// understands. Bump it whenever the on-disk config structure changes AND add a
// matching upgrade step in migrationSteps so older configs are upgraded in
// place. Old JSON formats are always preserved in code (the upgrade steps are
// additive and idempotent) so a config written by any prior version still loads.
const CurrentSchemaVersion = "1.5.0"

// migrationStep upgrades a raw config map from one schema version to the next.
// from is the version the step applies to; to is the version it produces. Steps
// operate on the decoded map[string]any so they can add/rename/move keys without
// being constrained by the current Go struct shape.
type migrationStep struct {
	from string
	to   string
	// apply mutates raw in place. It must be idempotent and tolerant of a
	// partially-populated map (a hand-edited or minimal config).
	apply func(raw map[string]any)
}

// migrationSteps is the ordered upgrade chain. To add a new schema version,
// append a step whose `from` matches the previous CurrentSchemaVersion and whose
// `to` matches the new CurrentSchemaVersion, then bump CurrentSchemaVersion.
var migrationSteps = []migrationStep{
	{
		from: "1.0.0",
		to:   "1.1.0",
		// 1.0.0 → 1.1.0: introduced the replaceable `prompts` block. Absent
		// blocks are handled by WithDefaults() at load time, so this step only
		// records the version bump (no structural rewrite is required -
		// PromptsConfig defaults itself in). Kept as an explicit step so the
		// chain documents the transition and stays future-proof.
	},
	{
		from: "1.1.0",
		to:   "1.2.0",
		apply: func(raw map[string]any) {
			renameNestedKey(raw, "skills", "directory", "dir")
			renameNestedKey(raw, "prompts", "rolesPath", "dir")
			if prompts, ok := raw["prompts"].(map[string]any); ok {
				if _, exists := prompts["enabled"]; !exists {
					prompts["enabled"] = true
				}
				delete(prompts, "rolesOverlayPath")
				delete(prompts, "assembleOnSpawn")
				delete(prompts, "maxRolePromptTokens")
				delete(prompts, "includeOverlayByDefault")
			}
			if skills, ok := raw["skills"].(map[string]any); ok {
				delete(skills, "indexOnBoot")
				delete(skills, "allowAgentCreate")
			}
			if events, ok := raw["events"].(map[string]any); ok {
				if oldPath, ok := events["busPath"]; ok {
					bus, _ := events["bus"].(map[string]any)
					if bus == nil {
						bus = map[string]any{}
						events["bus"] = bus
					}
					if _, exists := bus["walPath"]; !exists {
						bus["walPath"] = oldPath
					}
					delete(events, "busPath")
				}
			}
		},
	},
	{
		from: "1.2.0",
		to:   "1.3.0",
		apply: func(raw map[string]any) {
			// Tool surface flattened: drop the workspace_/system_/terminal_
			// prefixes (a feature-not-security design). Rewrite any role
			// allowedTools so configs written before the rename keep working.
			renames := map[string]string{
				"workspace_read_file":   "read_file",
				"workspace_write_file":  "write_file",
				"workspace_create_file": "create_file",
				"workspace_edit_file":   "edit_file",
				"workspace_delete_file": "delete_file",
				"workspace_search":      "search",
				"workspace_list_dir":    "list_dir",
				"workspace_glob":        "glob",
				"system_exec":           "exec",
				"terminal_run":          "exec",
			}
			subAgents, ok := raw["subAgents"].(map[string]any)
			if !ok {
				return
			}
			roles, ok := subAgents["roles"].(map[string]any)
			if !ok {
				return
			}
			for _, r := range roles {
				role, ok := r.(map[string]any)
				if !ok {
					continue
				}
				list, ok := role["allowedTools"].([]any)
				if !ok {
					continue
				}
				seen := map[string]bool{}
				var out []any
				for _, item := range list {
					name, _ := item.(string)
					if repl, found := renames[name]; found {
						name = repl
					}
					if name == "" || seen[name] {
						continue
					}
					seen[name] = true
					out = append(out, name)
				}
				role["allowedTools"] = out
			}
		},
	},
	{
		from: "1.3.0",
		to:   "1.4.0",
		apply: func(raw map[string]any) {
			// Split immutable config from runtime data. Only rewrite shipped
			// legacy defaults; explicit custom paths remain user-owned.
			replaceLegacyPath(raw, []string{"runtime", "dataDir"}, "~/.config/sapaloq", "~/SapaLOQ")
			replaceLegacyPath(raw, []string{"skills", "dir"}, "~/.config/sapaloq/skills", "~/SapaLOQ/skills")
			replaceLegacyPath(raw, []string{"prompts", "dir"}, "~/.config/sapaloq/prompts", "~/SapaLOQ/prompts")
			replaceLegacyPath(raw, []string{"events", "bus", "socketPath"}, "~/.config/sapaloq/run/sapaloq.sock", "~/SapaLOQ/run/sapaloq.sock")
			replaceLegacyPath(raw, []string{"events", "bus", "walPath"}, "~/.config/sapaloq/state/events.jsonl", "~/SapaLOQ/state/events.jsonl")
		},
	},
	{
		from: "1.4.0",
		to:   "1.5.0",
		apply: func(raw map[string]any) {
			ensureSubAgentMandatoryTools(raw)
		},
	},
}

func replaceLegacyPath(raw map[string]any, path []string, oldValue, newValue string) {
	current := raw
	for _, key := range path[:len(path)-1] {
		next, ok := current[key].(map[string]any)
		if !ok {
			return
		}
		current = next
	}
	key := path[len(path)-1]
	if value, ok := current[key].(string); ok && value == oldValue {
		current[key] = newValue
	}
}

// ensureSubAgentMandatoryTools appends lifecycle tools missing from role
// allowlists. Older configs omitted sapaloq_stop from planner, which left
// planners unable to end their run.
func ensureSubAgentMandatoryTools(raw map[string]any) {
	required := map[string][]string{
		"planner":     {"sapaloq_stop"},
		"task-runner": {"sapaloq_stop", "sapaloq_complete_task", "sapaloq_fail_task"},
		"scribe":      {"sapaloq_stop", "sapaloq_complete_task", "sapaloq_fail_task"},
	}
	subAgents, ok := raw["subAgents"].(map[string]any)
	if !ok {
		return
	}
	roles, ok := subAgents["roles"].(map[string]any)
	if !ok {
		return
	}
	for roleName, need := range required {
		role, ok := roles[roleName].(map[string]any)
		if !ok {
			continue
		}
		list, ok := role["allowedTools"].([]any)
		if !ok {
			continue
		}
		role["allowedTools"] = appendMissingStringTools(list, need...)
	}
}

func appendMissingStringTools(list []any, need ...string) []any {
	seen := map[string]bool{}
	for _, item := range list {
		if s, ok := item.(string); ok && s != "" {
			seen[s] = true
		}
	}
	out := append([]any(nil), list...)
	for _, name := range need {
		if seen[name] {
			continue
		}
		out = append(out, name)
		seen[name] = true
	}
	return out
}

func renameNestedKey(raw map[string]any, block, oldKey, newKey string) {
	nested, ok := raw[block].(map[string]any)
	if !ok {
		return
	}
	if old, exists := nested[oldKey]; exists {
		if _, already := nested[newKey]; !already {
			nested[newKey] = old
		}
		delete(nested, oldKey)
	}
}

// migrateRaw upgrades raw to CurrentSchemaVersion when its schemaVersion is
// older, returning (upgraded, changed, error).
//
//   - Lower version  → run the ordered upgrade chain to CurrentSchemaVersion.
//   - Equal version  → no change.
//   - Higher version → leave as-is (forward-compat: try to load it; mandatory
//     fields are validated after unmarshal by the caller, which errors if a
//     required field came out empty).
//   - Missing/blank  → treated as the baseline "1.0.0" and upgraded.
func migrateRaw(raw map[string]any) (map[string]any, bool, error) {
	if raw == nil {
		return raw, false, nil
	}
	current := strings.TrimSpace(stringField(raw, "schemaVersion"))
	if current == "" {
		current = "1.0.0"
	}
	cmp, err := compareSemver(current, CurrentSchemaVersion)
	if err != nil {
		// Unparseable version: don't guess. Leave the config untouched and let
		// the caller validate mandatory fields.
		return raw, false, nil
	}
	if cmp == 0 {
		return raw, false, nil
	}
	if cmp > 0 {
		// Newer than this build. Try to use it as-is (forward compatibility).
		return raw, false, nil
	}

	// Older: walk the chain, applying each step whose `from` we're currently at.
	changed := false
	guard := 0
	for {
		if c, e := compareSemver(current, CurrentSchemaVersion); e != nil || c >= 0 {
			break
		}
		step, ok := stepFrom(current)
		if !ok {
			// No step from here but we're still behind: jump straight to the
			// current version (additive defaults via WithDefaults cover the gap).
			raw["schemaVersion"] = CurrentSchemaVersion
			changed = true
			break
		}
		if step.apply != nil {
			step.apply(raw)
		}
		raw["schemaVersion"] = step.to
		current = step.to
		changed = true
		guard++
		if guard > 64 {
			return raw, changed, fmt.Errorf("config migration did not converge (cycle near %s)", current)
		}
	}
	return raw, changed, nil
}

// stepFrom returns the migration step that applies at version v.
func stepFrom(v string) (migrationStep, bool) {
	for _, s := range migrationSteps {
		if s.from == v {
			return s, true
		}
	}
	return migrationStep{}, false
}

// stringField reads a string field from a raw map, tolerating a non-string
// value (returns "").
func stringField(raw map[string]any, key string) string {
	if v, ok := raw[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// compareSemver compares two dotted versions (e.g. "1.2.0"). Missing segments
// are treated as 0, so "1.1" == "1.1.0". Returns -1, 0, or +1. A non-numeric
// segment yields an error.
func compareSemver(a, b string) (int, error) {
	pa, err := parseSemver(a)
	if err != nil {
		return 0, err
	}
	pb, err := parseSemver(b)
	if err != nil {
		return 0, err
	}
	for i := 0; i < 3; i++ {
		if pa[i] < pb[i] {
			return -1, nil
		}
		if pa[i] > pb[i] {
			return 1, nil
		}
	}
	return 0, nil
}

func parseSemver(v string) ([3]int, error) {
	var out [3]int
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	// Drop any pre-release/build metadata (e.g. "1.2.0-rc1").
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	if v == "" {
		return out, fmt.Errorf("empty version")
	}
	parts := strings.Split(v, ".")
	for i := 0; i < len(parts) && i < 3; i++ {
		n, err := strconv.Atoi(strings.TrimSpace(parts[i]))
		if err != nil {
			return out, fmt.Errorf("invalid version %q: %w", v, err)
		}
		out[i] = n
	}
	return out, nil
}
