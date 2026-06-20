package orchestrator

import (
	"encoding/json"

	provider "github.com/jahrulnr/sapaloq/internal/bridges/provider"
)

// Tool profiles per execution mode (see docs/ORCHESTRATOR.md "Ask → Plan →
// Agent"). Ask coordinates; Plan does read-only assessment + writes a plan;
// Agent does read-only assessment + write/exec + task lifecycle.
//
// All three now share a common set of read-only assessment tools so Ask and
// Plan are no longer "blind" — they can read files, search, list directories,
// and run web research before deciding or planning.

var readOnlyAssessmentTools = []string{
	"workspace_read_file",
	"workspace_search",
	"workspace_list_dir",
	"workspace_glob",
	"web_search",
	"web_fetch",
}

// askTools: orchestrator coordinates and may assess lightly before delegating.
var askTools = append([]string{
	"sapaloq_spawn_plan",
	"sapaloq_spawn_agent",
	"sapaloq_spawn_scribe",
	"sapaloq_get_task_status",
	"sapaloq_wait",
	"sapaloq_answer_clarification",
	"sapaloq_stop",
}, readOnlyAssessmentTools...)

// scribeTools: a named sub-agent that captures notes into the user's storage by
// boundary. It is read-only on the project (assessment tools) and may only
// persist via scribe_write_note (NOT general workspace writes).
var scribeTools = append(append([]string{}, readOnlyAssessmentTools...),
	"scribe_write_note",
	"sapaloq_update_task_progress",
	"sapaloq_complete_task",
	"sapaloq_fail_task",
	"sapaloq_request_clarification",
)

// planTools: read-only planner. Assessment + write/read its own plan markdown
// (read enables iterating/refining the plan before finishing).
var planTools = append(append([]string{}, readOnlyAssessmentTools...),
	"sapaloq_write_plan_markdown",
	"sapaloq_read_plan_markdown",
	"sapaloq_request_clarification",
)

// agentTools: full executor. Assessment + write/edit/delete/exec + lifecycle.
var agentTools = append(append([]string{}, readOnlyAssessmentTools...),
	"workspace_write_file",
	"workspace_create_file",
	"workspace_edit_file",
	"workspace_delete_file",
	"terminal_run",
	"sapaloq_read_plan_markdown",
	"sapaloq_update_task_progress",
	"sapaloq_complete_task",
	"sapaloq_fail_task",
	"sapaloq_request_clarification",
)

// staticToolsForRole returns the built-in declared-tool profile for a role.
func staticToolsForRole(role string) []string {
	switch role {
	case "planner":
		return planTools
	case "task-runner":
		return agentTools
	case "scribe":
		return scribeTools
	default:
		return askTools
	}
}

// toolsForRole returns the declared-tool list a sub-agent role may be OFFERED.
// When the role declares allowedTools in config, the offered set is the config
// allowlist intersected with the known tool registry (so the model is only told
// about tools it can actually call AND that exist). When unconfigured, the
// static per-role profile is used (backward-compatible).
func (o *Orchestrator) toolsForRole(role string) []string {
	static := staticToolsForRole(role)
	roles := o.cfg.SubAgents.Roles
	if roles == nil {
		return static
	}
	r, ok := roles[role]
	if !ok || len(r.AllowedTools) == 0 {
		return static
	}
	// Intersect the config allowlist with all known tools (union of every
	// static profile) so only real, registered tools are offered.
	known := knownToolSet()
	var out []string
	for name := range known {
		if matchToolAllowlist(r.AllowedTools, name) {
			out = append(out, name)
		}
	}
	if len(out) == 0 {
		// Config named only unknown/abstract tools; fall back so the agent
		// isn't left toolless.
		return static
	}
	return out
}

// knownToolSet is the union of every static role profile — the set of tool
// names the orchestrator actually implements.
func knownToolSet() map[string]struct{} {
	set := map[string]struct{}{}
	for _, profile := range [][]string{askTools, planTools, agentTools, scribeTools} {
		for _, name := range profile {
			set[name] = struct{}{}
		}
	}
	return set
}

// init registers concrete JSON parameter schemas so the upstream model knows
// the arguments for each tool (instead of an open object).
func init() {
	reg := func(name, schema string) {
		provider.RegisterToolSchema(name, json.RawMessage(schema))
	}

	reg("workspace_read_file", `{
		"type":"object",
		"properties":{
			"path":{"type":"string","description":"Relative path within the workspace root to read."},
			"offset":{"type":"integer","description":"Optional 1-based start line. With limit, returns only that line window (numbered)."},
			"limit":{"type":"integer","description":"Optional max lines to read from offset (default 200 when offset/limit used)."},
			"max_bytes":{"type":"integer","description":"Optional cap on bytes read (default 65536). Binary files are refused."}
		},
		"required":["path"]
	}`)

	reg("workspace_glob", `{
		"type":"object",
		"properties":{
			"pattern":{"type":"string","description":"Glob pattern, e.g. *.go or **/*.ts (supports ** for recursive)."},
			"max_results":{"type":"integer","description":"Max paths to return (default 40)."}
		},
		"required":["pattern"]
	}`)

	reg("workspace_edit_file", `{
		"type":"object",
		"properties":{
			"path":{"type":"string","description":"Relative path within the workspace root to edit in place."},
			"old_string":{"type":"string","description":"Exact text to replace (include surrounding context to make it unique)."},
			"new_string":{"type":"string","description":"Replacement text."},
			"replace_all":{"type":"boolean","description":"Replace every occurrence instead of requiring a unique match."}
		},
		"required":["path","old_string","new_string"]
	}`)

	reg("workspace_delete_file", `{
		"type":"object",
		"properties":{
			"path":{"type":"string","description":"Relative path within the workspace root to delete (files only)."}
		},
		"required":["path"]
	}`)

	reg("workspace_search", `{
		"type":"object",
		"properties":{
			"pattern":{"type":"string","description":"Regular expression to search for in file contents."},
			"glob":{"type":"string","description":"Optional filename glob filter, e.g. *.go."},
			"max_results":{"type":"integer","description":"Max matching lines to return (default 40)."}
		},
		"required":["pattern"]
	}`)

	reg("workspace_list_dir", `{
		"type":"object",
		"properties":{
			"path":{"type":"string","description":"Relative directory path within the workspace root (default \".\")."}
		}
	}`)

	reg("web_search", `{
		"type":"object",
		"properties":{
			"query":{"type":"string","description":"Search query."}
		},
		"required":["query"]
	}`)

	reg("web_fetch", `{
		"type":"object",
		"properties":{
			"url":{"type":"string","description":"Fully-qualified URL to fetch."},
			"max_bytes":{"type":"integer","description":"Optional cap on bytes returned (default 32768)."}
		},
		"required":["url"]
	}`)

	reg("workspace_write_file", `{
		"type":"object",
		"properties":{
			"path":{"type":"string","description":"Relative path within the workspace root to overwrite."},
			"content":{"type":"string","description":"Full file content to write."}
		},
		"required":["path","content"]
	}`)

	reg("workspace_create_file", `{
		"type":"object",
		"properties":{
			"path":{"type":"string","description":"Relative path within the workspace root to create (must not exist)."},
			"content":{"type":"string","description":"Full file content to write."}
		},
		"required":["path","content"]
	}`)

	reg("terminal_run", `{
		"type":"object",
		"properties":{
			"command":{"type":"string","description":"Shell command to run inside the workspace root."},
			"timeout_seconds":{"type":"integer","description":"Optional timeout (default 60, max 600)."}
		},
		"required":["command"]
	}`)

	reg("sapaloq_write_plan_markdown", `{
		"type":"object",
		"properties":{
			"markdown":{"type":"string","description":"The full plan in Markdown with Goal, Constraints, Steps, Risks, and Acceptance sections."}
		},
		"required":["markdown"]
	}`)

	reg("sapaloq_read_plan_markdown", `{"type":"object","properties":{}}`)

	reg("sapaloq_update_task_progress", `{
		"type":"object",
		"properties":{
			"note":{"type":"string","description":"Short progress note for the user."}
		},
		"required":["note"]
	}`)

	reg("sapaloq_complete_task", `{
		"type":"object",
		"properties":{
			"summary":{"type":"string","description":"Final result summary; confirm acceptance criteria were met."}
		},
		"required":["summary"]
	}`)

	reg("sapaloq_fail_task", `{
		"type":"object",
		"properties":{
			"reason":{"type":"string","description":"Why the task could not be completed."}
		},
		"required":["reason"]
	}`)

	reg("sapaloq_request_clarification", `{
		"type":"object",
		"properties":{
			"question":{"type":"string","description":"The clarifying question for the user/orchestrator."},
			"options":{"type":"array","items":{"type":"string"},"description":"Optional answer options."}
		},
		"required":["question"]
	}`)

	reg("sapaloq_answer_clarification", `{
		"type":"object",
		"properties":{
			"task_id":{"type":"string","description":"Task awaiting clarification. Omit to target the latest awaiting task in this session."},
			"answer":{"type":"string","description":"The user's answer to the sub-agent's clarification question. The task resumes with this answer."}
		},
		"required":["answer"]
	}`)

	reg("sapaloq_spawn_scribe", `{
		"type":"object",
		"properties":{
			"task":{"type":"string","description":"What to capture, e.g. 'note that the deploy runs on Fridays'."}
		},
		"required":["task"]
	}`)

	reg("scribe_write_note", `{
		"type":"object",
		"properties":{
			"note":{"type":"string","description":"The note text to append (timestamped)."},
			"storage_id":{"type":"string","description":"Explicit storage path id from storage.paths (highest priority)."},
			"intent":{"type":"string","description":"Intent phrase mapped via storage.intents (e.g. 'catat', 'work note')."},
			"mode":{"type":"string","description":"Boundary mode: personal | work | hobby."},
			"kind":{"type":"string","description":"Optional kind within a mode (e.g. notes, inbox, journal)."}
		},
		"required":["note"]
	}`)
}
