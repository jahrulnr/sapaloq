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
	"read_file",
	"search",
	"list_dir",
	"glob",
	"read_image",
	"web_search",
	"web_fetch",
}

// desktopTools: host-desktop integration (notifications / DND). Gated by the
// active adapter's capabilities at execution time.
var desktopTools = []string{
	"desktop_notify",
	"desktop_dnd_status",
}

// askTools: orchestrator coordinates and may assess lightly before delegating.
// It also gets exec so simple host tasks (e.g. "read /etc/hosts", "run `id`")
// don't require spawning a plan/agent. exec_async is also offered so Ask can
// launch long-running or potentially-hanging commands without blocking the
// turn loop; exec_status / exec_result / exec_cancel are part of the same
// async-exec workflow.
var askTools = append(append([]string{
	"sapaloq_spawn_plan",
	"sapaloq_spawn_agent",
	"sapaloq_spawn_scribe",
	"sapaloq_get_task_status",
	"sapaloq_wait",
	"sapaloq_answer_clarification",
	"sapaloq_send_steering",
	"sapaloq_wait_events",
	"sapaloq_stop",
	"exec",
	"exec_async",
	"exec_status",
	"exec_result",
	"exec_cancel",
}, readOnlyAssessmentTools...), desktopTools...)

// scribeTools: a named sub-agent that captures notes into the user's storage by
// boundary. It is read-only on the project (assessment tools) and may only
// persist via scribe_write_note (NOT general file writes).
var scribeTools = append(append([]string{}, readOnlyAssessmentTools...),
	"scribe_write_note",
	"sapaloq_update_task_progress",
	"sapaloq_complete_task",
	"sapaloq_fail_task",
	"request_clarification",
	"sapaloq_request_decision",
	"sapaloq_send_steering",
	"sapaloq_wait_events",
)

// planTools: planner. Assessment + exec (so it can investigate the host while
// planning) + write/read its own plan markdown. exec_async + status/result/
// cancel are also offered so a planner can launch a long probe (e.g. a build
// or a network check) without hanging its own turn loop.
var planTools = append(append([]string{}, readOnlyAssessmentTools...),
	"exec",
	"exec_async",
	"exec_status",
	"exec_result",
	"exec_cancel",
	"write_plan",
	"read_plan",
	"request_clarification",
	"sapaloq_request_decision",
	"sapaloq_send_steering",
	"sapaloq_wait_events",
)

// agentTools: full executor. Assessment + exec + write/edit/delete + lifecycle.
// The async exec tools (exec_async / exec_status / exec_result / exec_cancel)
// let the agent stay alive when a single command would otherwise wedge its
// turn loop — see prompts/agent.md and tools_async_exec.go.
var agentTools = append(append([]string{}, readOnlyAssessmentTools...),
	"exec",
	"exec_async",
	"exec_status",
	"exec_result",
	"exec_cancel",
	"write_file",
	"create_file",
	"edit_file",
	"delete_file",
	"read_plan",
	"sapaloq_update_task_progress",
	"sapaloq_complete_task",
	"sapaloq_fail_task",
	"request_clarification",
	"desktop_notify",
	"sapaloq_request_decision",
	"sapaloq_send_steering",
	"sapaloq_wait_events",
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
	for _, profile := range [][]string{askTools, planTools, agentTools, scribeTools, desktopTools} {
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

	reg("read_file", `{
		"type":"object",
		"properties":{
			"path":{"type":"string","description":"Path to read (absolute, ~-relative, or relative to CWD). Any host path is allowed."},
			"offset":{"type":"integer","description":"Optional 1-based start line. With limit, returns only that line window (numbered)."},
			"limit":{"type":"integer","description":"Optional max lines to read from offset (default 200 when offset/limit used)."},
			"max_bytes":{"type":"integer","description":"Optional cap on bytes read (default 65536). Binary files are refused."}
		},
		"required":["path"]
	}`)

	reg("glob", `{
		"type":"object",
		"properties":{
			"path":{"type":"string","description":"Root directory to search (default CWD). Any host path is allowed."},
			"pattern":{"type":"string","description":"Glob pattern, e.g. *.go or **/*.ts (supports ** for recursive)."},
			"max_results":{"type":"integer","description":"Max paths to return (default 40)."}
		},
		"required":["pattern"]
	}`)

	reg("edit_file", `{
		"type":"object",
		"properties":{
			"path":{"type":"string","description":"Path to edit in place (absolute, ~-relative, or relative to CWD). Any host path is allowed."},
			"old_string":{"type":"string","description":"Exact text to replace (include surrounding context to make it unique)."},
			"new_string":{"type":"string","description":"Replacement text."},
			"replace_all":{"type":"boolean","description":"Replace every occurrence instead of requiring a unique match."}
		},
		"required":["path","old_string","new_string"]
	}`)

	reg("delete_file", `{
		"type":"object",
		"properties":{
			"path":{"type":"string","description":"Path to delete (files only; absolute, ~-relative, or relative to CWD). Any host path is allowed."}
		},
		"required":["path"]
	}`)

	reg("sapaloq_send_steering", `{
		"type":"object",
		"properties":{
			"target_task_id":{"type":"string","description":"Target actor/task id. Use the session id to steer the foreground UI orchestrator."},
			"message":{"type":"string","description":"Concrete steering, correction, new evidence, or follow-up."},
			"priority":{"type":"string","enum":["normal","interrupt"],"default":"normal"},
			"correlation_id":{"type":"string","description":"Optional id linking this steering to a prior decision or plan event."}
		},
		"required":["target_task_id","message"]
	}`)
	reg("sapaloq_wait_events", `{
		"type":"object",
		"properties":{
			"timeout_seconds":{"type":"integer","minimum":1,"maximum":600,"default":120}
		}
	}`)
	reg("sapaloq_request_decision", `{
		"type":"object",
		"properties":{
			"question":{"type":"string"},
			"options":{"type":"array","items":{"type":"string"}},
			"correlation_id":{"type":"string"}
		},
		"required":["question"]
	}`)

	reg("search", `{
		"type":"object",
		"properties":{
			"path":{"type":"string","description":"Root directory to search (default CWD). Any host path is allowed."},
			"pattern":{"type":"string","description":"Regular expression to search for in file contents."},
			"glob":{"type":"string","description":"Optional filename glob filter, e.g. *.go."},
			"max_results":{"type":"integer","description":"Max matching lines to return (default 40)."}
		},
		"required":["pattern"]
	}`)

	reg("list_dir", `{
		"type":"object",
		"properties":{
			"path":{"type":"string","description":"Directory path to list (default \".\"; absolute, ~-relative, or relative to CWD). Any host path is allowed."}
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

	reg("write_file", `{
		"type":"object",
		"properties":{
			"path":{"type":"string","description":"Path to overwrite (absolute, ~-relative, or relative to CWD). Any host path is allowed. Parent dirs are created."},
			"content":{"type":"string","description":"Full file content to write."}
		},
		"required":["path","content"]
	}`)

	reg("create_file", `{
		"type":"object",
		"properties":{
			"path":{"type":"string","description":"Path to create (must not exist; absolute, ~-relative, or relative to CWD). Any host path is allowed. Parent dirs are created."},
			"content":{"type":"string","description":"Full file content to write."}
		},
		"required":["path","content"]
	}`)

	reg("exec", `{
		"type":"object",
		"properties":{
			"command":{"type":"string","description":"Any shell command to run on the host with full access. Use this to read host files too (e.g. cat/sed -n/head/tail/rg). NOTE: commands run via 'bash -lc', so syntax is POSIX/Unix (Linux & macOS); macOS BSD tools differ slightly from GNU (e.g. sed -i, date), and on Windows hosts a bash shell may be unavailable — prefer portable invocations or check the OS first."},
			"cwd":{"type":"string","description":"Optional working directory (any path, ~ expanded). Defaults to the actor's persisted workspace CWD (initially ~/SapaLOQ/workspace). A cd persists for later calls by the same actor."},
			"timeout_seconds":{"type":"integer","description":"Optional timeout (default 60, max 600)."}
		},
		"required":["command"]
	}`)

	reg("exec_async", `{
		"type":"object",
		"properties":{
			"command":{"type":"string","description":"Any shell command to run on the host with full access. Returns immediately with a job_id; poll exec_status / exec_result to fetch the output. Use this for long-running or potentially-hanging commands (servers, watchers, network calls)."},
			"cwd":{"type":"string","description":"Optional working directory (any path, ~ expanded)."},
			"timeout_seconds":{"type":"integer","description":"Per-job timeout enforced by the host (default 60, max 600)."}
		},
		"required":["command"]
	}`)

	reg("exec_status", `{
		"type":"object",
		"properties":{
			"job_id":{"type":"string","description":"The job_id returned by exec_async."}
		},
		"required":["job_id"]
	}`)

	reg("exec_result", `{
		"type":"object",
		"properties":{
			"job_id":{"type":"string","description":"The job_id returned by exec_async."},
			"wait_seconds":{"type":"integer","description":"How long to block waiting for the job to finish (default 30, max 300). Use a small value in a poll loop, or a larger one-shot value when you are willing to wait. If the job is still running after the wait, the response is {status:'running', waited_ms, hint} — call exec_cancel(job_id) or sapaloq_fail_task if it has been too long."}
		},
		"required":["job_id"]
	}`)

	reg("exec_cancel", `{
		"type":"object",
		"properties":{
			"job_id":{"type":"string","description":"The job_id returned by exec_async. The host kills the process and the job is marked cancelled; partial output (if any) is returned."}
		},
		"required":["job_id"]
	}`)

	reg("read_image", `{
		"type":"object",
		"properties":{
			"path":{"type":"string","description":"Path to an image file on the host (absolute, ~-relative, or relative to CWD). Loads the actual image into your vision so you can SEE it — use this for local image files (png, jpeg, gif, webp). Do NOT use read_file/exec to read image bytes as text; use this instead. Requires a vision-capable model."},
			"max_bytes":{"type":"integer","description":"Optional cap on bytes read (default 10485760 / 10 MiB; larger files are refused)."}
		},
		"required":["path"]
	}`)

	reg("sapaloq_spawn_plan", `{
		"type":"object",
		"properties":{
			"task":{"type":"string","description":"The task to investigate and plan."}
		},
		"required":["task"]
	}`)

	reg("sapaloq_spawn_agent", `{
		"type":"object",
		"properties":{
			"task":{"type":"string","description":"The task to execute."},
			"plan_task_id":{"type":"string","description":"Optional explicit planner task id to execute after the user approves that plan. Omit for a direct Agent run."}
		},
		"required":["task"]
	}`)

	reg("write_plan", `{
		"type":"object",
		"properties":{
			"markdown":{"type":"string","description":"The full plan in Markdown with Goal, Constraints, Steps, Risks, and Acceptance sections."}
		},
		"required":["markdown"]
	}`)

	reg("read_plan", `{"type":"object","properties":{}}`)

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

	reg("request_clarification", `{
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
