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

// unrestrictedSystemTools give the model FULL host access (read any path, run
// any command anywhere) — deliberately NOT sandboxed to the workspace root.
// Per the user's design these are available in EVERY mode (Ask, planner,
// agent), because spawning a plan/agent is overkill for many host-inspection
// tasks. system_read_file is read-only; system_exec can mutate, but is offered
// in all modes by explicit policy.
var unrestrictedSystemTools = []string{
	"system_read_file",
	"system_exec",
}

// desktopTools: host-desktop integration (notifications / DND). Gated by the
// active adapter's capabilities at execution time.
var desktopTools = []string{
	"desktop_notify",
	"desktop_dnd_status",
}

// askTools: orchestrator coordinates and may assess lightly before delegating.
// It also gets the unrestricted system_* tools so simple host tasks (e.g. "read
// /etc/hosts", "run `id`") don't require spawning a plan/agent.
var askTools = append(append(append([]string{
	"sapaloq_spawn_plan",
	"sapaloq_spawn_agent",
	"sapaloq_spawn_scribe",
	"sapaloq_get_task_status",
	"sapaloq_wait",
	"sapaloq_answer_clarification",
	"sapaloq_stop",
}, readOnlyAssessmentTools...), desktopTools...), unrestrictedSystemTools...)

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

// planTools: planner. Assessment + the unrestricted system_* tools (so it can
// investigate the host while planning) + write/read its own plan markdown.
var planTools = append(append(append([]string{}, readOnlyAssessmentTools...), unrestrictedSystemTools...),
	"sapaloq_write_plan_markdown",
	"sapaloq_read_plan_markdown",
	"sapaloq_request_clarification",
)

// agentTools: full executor. Assessment + unrestricted host access +
// write/edit/delete/exec + lifecycle.
var agentTools = append(append(append([]string{}, readOnlyAssessmentTools...), unrestrictedSystemTools...),
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
	"desktop_notify",
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
	for _, profile := range [][]string{askTools, planTools, agentTools, scribeTools, desktopTools, unrestrictedSystemTools} {
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

	reg("system_read_file", `{
		"type":"object",
		"properties":{
			"path":{"type":"string","description":"Any file path on the host (absolute, ~-relative, or relative to CWD). NOT restricted to the workspace — e.g. /etc/hosts."},
			"offset":{"type":"integer","description":"Optional 1-based start line. With limit, returns only that numbered line window."},
			"limit":{"type":"integer","description":"Optional max lines to read from offset (default 200 when offset/limit used)."},
			"max_bytes":{"type":"integer","description":"Optional cap on bytes read (default 262144, max 4194304). Binary files are refused."}
		},
		"required":["path"]
	}`)

	reg("system_exec", `{
		"type":"object",
		"properties":{
			"command":{"type":"string","description":"Any shell command to run on the host with full, unrestricted access (run via bash -lc). NOT pinned to the workspace root."},
			"cwd":{"type":"string","description":"Optional working directory (any path, ~ expanded). Defaults to the process CWD."},
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

	reg("desktop_notify", `{
		"type":"object",
		"properties":{
			"title":{"type":"string","description":"Notification title."},
			"body":{"type":"string","description":"Notification body text."},
			"urgency":{"type":"string","description":"Optional urgency: low | normal | critical (default normal)."}
		},
		"required":["title"]
	}`)

	reg("desktop_dnd_status", `{"type":"object","properties":{}}`)
}
