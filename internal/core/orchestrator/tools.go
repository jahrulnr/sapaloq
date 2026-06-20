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
	"web_search",
	"web_fetch",
}

// askTools: orchestrator coordinates and may assess lightly before delegating.
var askTools = append([]string{
	"sapaloq_spawn_plan",
	"sapaloq_spawn_agent",
	"sapaloq_get_task_status",
	"sapaloq_wait",
	"sapaloq_stop",
}, readOnlyAssessmentTools...)

// planTools: read-only planner. Assessment + write the plan markdown.
var planTools = append(append([]string{}, readOnlyAssessmentTools...),
	"sapaloq_write_plan_markdown",
	"sapaloq_request_clarification",
)

// agentTools: full executor. Assessment + write/exec + lifecycle.
var agentTools = append(append([]string{}, readOnlyAssessmentTools...),
	"workspace_write_file",
	"workspace_create_file",
	"terminal_run",
	"sapaloq_read_plan_markdown",
	"sapaloq_update_task_progress",
	"sapaloq_complete_task",
	"sapaloq_fail_task",
	"sapaloq_request_clarification",
)

// toolsForRole returns the declared-tool list for a sub-agent role.
func toolsForRole(role string) []string {
	switch role {
	case "planner":
		return planTools
	case "task-runner":
		return agentTools
	default:
		return askTools
	}
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
			"max_bytes":{"type":"integer","description":"Optional cap on bytes read (default 65536)."}
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
}
