package orchestrator

import (
	"encoding/json"

	provider "github.com/jahrulnr/sapaloq/internal/bridges/provider"
	"github.com/jahrulnr/sapaloq/internal/tooldocs"
)

// Tool profiles per execution mode (see docs/ORCHESTRATOR.md "Ask → Plan →
// Agent"). Ask and Agent share the same workspace tool surface; behavioral
// differences are prompt-driven (delegation, spawn, fire-and-forget). Plan is
// read-only on the project plus write_plan; scribe is assessment + notes.

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

// askTools: full workspace surface for the foreground Ask actor. Behavioral
// differences vs background executors live in the Ask system prompt (when to
// delegate, fire-and-forget spawn, etc.) — not in a reduced tool allowlist.
var askTools = append(append([]string{
	"sapaloq_spawn_plan",
	"sapaloq_spawn_agent",
	"sapaloq_spawn_scribe",
	"sapaloq_get_task_status",
	"sapaloq_resume_task",
	"wait",
	"sapaloq_cancel_job",
	"sapaloq_answer_clarification",
	"sapaloq_send_steering",
	"sapaloq_stop",
	"exec",
	"write_file",
	"create_file",
	"edit_file",
	"delete_file",
}, readOnlyAssessmentTools...), desktopTools...)

// scribeTools: a named sub-agent that captures notes into the user's storage by
// boundary. It is read-only on the project (assessment tools) and may only
// persist via scribe_write_note (NOT general file writes).
var scribeTools = append(append([]string{}, readOnlyAssessmentTools...),
	"scribe_write_note",
	"wait",
	"sapaloq_cancel_job",
	"sapaloq_update_task_progress",
	"sapaloq_complete_task",
	"sapaloq_fail_task",
	"sapaloq_stop",
	"request_clarification",
	"sapaloq_request_decision",
	"sapaloq_send_steering",
)

// planTools: planner. Assessment + exec (so it can investigate the host while
// planning) + write/read its own plan markdown. `wait` + `sapaloq_cancel_job`
// let a planner fire-and-forget a slow probe and collect it later.
var planTools = append(append([]string{}, readOnlyAssessmentTools...),
	"exec",
	"wait",
	"sapaloq_cancel_job",
	"write_plan",
	"read_plan",
	"sapaloq_stop",
	"request_clarification",
	"sapaloq_request_decision",
	"sapaloq_send_steering",
)

// agentTools: full executor. Assessment + exec + write/edit/delete + lifecycle.
// Any work tool can be fired with wait_for_output:false and collected via
// `wait` (mode=tool); sapaloq_cancel_job aborts a running background job.
var agentTools = append(append([]string{}, readOnlyAssessmentTools...),
	"exec",
	"wait",
	"sapaloq_cancel_job",
	"write_file",
	"create_file",
	"edit_file",
	"delete_file",
	"read_plan",
	"sapaloq_update_task_progress",
	"sapaloq_complete_task",
	"sapaloq_fail_task",
	"sapaloq_stop",
	"request_clarification",
	"desktop_notify",
	"sapaloq_request_decision",
	"sapaloq_send_steering",
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
	return mergeToolOffer(out, mandatoryToolsForRole(role))
}

// mandatoryToolsForRole lists lifecycle tools that must always be offered and
// permitted for a sub-agent role, even when config allowedTools omits them.
// Without sapaloq_stop a planner cannot end its run and autopilot continuations
// loop forever.
func mandatoryToolsForRole(role string) []string {
	switch role {
	case "planner":
		return []string{"sapaloq_stop"}
	case "task-runner", "scribe":
		return []string{"sapaloq_stop", "sapaloq_complete_task", "sapaloq_fail_task"}
	default:
		return nil
	}
}

func mergeToolOffer(base, extra []string) []string {
	if len(extra) == 0 {
		return base
	}
	known := knownToolSet()
	seen := make(map[string]struct{}, len(base)+len(extra))
	out := make([]string, 0, len(base)+len(extra))
	add := func(name string) {
		if name == "" {
			return
		}
		if _, ok := seen[name]; ok {
			return
		}
		if _, ok := known[name]; !ok {
			return
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	for _, name := range base {
		add(name)
	}
	for _, name := range extra {
		add(name)
	}
	return out
}

// knownToolSet is the union of every static role profile - the set of tool
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
	// waitForOutputExempt: lifecycle / meta tools whose result IS the transition
	// or that manage async jobs directly. They never accept wait_for_output.
	waitForOutputExempt := map[string]struct{}{
		"sapaloq_stop":                 {},
		"sapaloq_complete_task":        {},
		"sapaloq_fail_task":            {},
		"request_clarification":        {},
		"sapaloq_answer_clarification": {},
		"sapaloq_send_steering":        {},
		"sapaloq_update_task_progress": {},
		"sapaloq_get_task_status":      {},
		"sapaloq_resume_task":          {},
		"sapaloq_spawn_plan":           {},
		"sapaloq_spawn_agent":          {},
		"sapaloq_spawn_scribe":         {},
		"sapaloq_request_decision":     {},
		"wait":                         {},
		"sapaloq_cancel_job":           {},
	}
	reg := func(name, schema string) {
		if _, exempt := waitForOutputExempt[name]; !exempt {
			schema = injectWaitForOutput(schema)
		}
		provider.RegisterTool(name, json.RawMessage(schema), tooldocs.Description(name))
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
			"command":{"type":"string","description":"Any shell command to run on the host with full access. Use this to read host files too (e.g. cat/sed -n/head/tail/rg). NOTE: commands run via 'bash -lc', so syntax is POSIX/Unix (Linux & macOS); macOS BSD tools differ slightly from GNU (e.g. sed -i, date), and on Windows hosts a bash shell may be unavailable - prefer portable invocations or check the OS first."},
			"cwd":{"type":"string","description":"Optional working directory (any path, ~ expanded). Defaults to the actor's persisted workspace CWD. A cd persists for later calls by the same actor."},
			"timeout_seconds":{"type":"integer","description":"Optional timeout (default 60, max 600)."}
		},
		"required":["command"]
	}`)

	reg("read_image", `{
		"type":"object",
		"properties":{
			"path":{"type":"string","description":"Path to an image file on the host (absolute, ~-relative, or relative to CWD). Loads the actual image into your vision so you can SEE it - use this for local image files (png, jpeg, gif, webp). Do NOT use read_file/exec to read image bytes as text; use this instead. Requires a vision-capable model."},
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

	// wait: the unified wait tool. mode selects behavior:
	//   time   - sleep `seconds` (bounded by continuation.maxWaitSeconds).
	//   tool   - collect a background job's result by job_id (replaces
	//            exec_status/exec_result). timeout_seconds=0 = instant peek.
	//   task   - wait for a sub-agent task to change state (replaces sapaloq_wait).
	//   events - wait for actor/steering events (replaces sapaloq_wait_events).
	reg("wait", `{
		"type":"object",
		"properties":{
			"mode":{"type":"string","enum":["time","tool","task","events"],"description":"What to wait for."},
			"seconds":{"type":"integer","minimum":1,"maximum":600,"description":"mode=time: how long to sleep (bounded by continuation.maxWaitSeconds)."},
			"job_id":{"type":"string","description":"mode=tool: the job_id returned by a fire-and-forget tool (wait_for_output:false)."},
			"task_id":{"type":"string","description":"mode=task: the sub-agent task id to watch. Omit to watch the latest task."},
			"timeout_seconds":{"type":"integer","minimum":0,"maximum":600,"description":"mode=tool: how long to block for the job (default 30; 0 = instant peek). mode=events: how long to wait for an event (default 120)."}
		},
		"required":["mode"]
	}`)

	// sapaloq_cancel_job: cancel any background job by job_id (replaces
	// exec_cancel, generalized to every fire-and-forget tool).
	reg("sapaloq_cancel_job", `{
		"type":"object",
		"properties":{
			"job_id":{"type":"string","description":"The job_id returned by a fire-and-forget tool (wait_for_output:false). The host cancels the running job and returns its partial output, if any."}
		},
		"required":["job_id"]
	}`)

	reg("sapaloq_stop", `{
		"type":"object",
		"properties":{
			"scope":{"type":"string","enum":["generation","task","all"],"description":"What to stop. Default generation ends the current foreground run only. task stops a background task by task_id. all stops generation and every session task."},
			"task_id":{"type":"string","description":"Required when scope=task: the background task id to cancel."},
			"reason":{"type":"string","description":"Optional short reason recorded in the tool result."}
		}
	}`)

	reg("sapaloq_get_task_status", `{
		"type":"object",
		"properties":{
			"task_id":{"type":"string","description":"Background task id to inspect. Omit to use the latest task in the session."}
		}
	}`)

	reg("sapaloq_resume_task", `{
		"type":"object",
		"properties":{
			"task_id":{"type":"string","description":"Failed or stopped background task to resume from persisted turns. Omit to resume the latest resumable task in this session."}
		}
	}`)
}

// injectWaitForOutput adds the wait_for_output property to a tool's JSON
// schema. The argument defaults to true (blocking, current behavior); when
// false, the tool is dispatched as a background job and returns immediately
// with {job_id, status:"queued"} so the model can collect the result later
// via the unified `wait` tool. Tools whose schema is an empty object
// ({"type":"object","properties":{}}) get a properties object created.
func injectWaitForOutput(schema string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(schema), &m); err != nil {
		// Malformed schema: register as-is rather than dropping the tool.
		return schema
	}
	props, ok := m["properties"].(map[string]any)
	if !ok {
		props = map[string]any{}
	}
	props["wait_for_output"] = map[string]any{
		"type":        "boolean",
		"default":     true,
		"description": "When true (default), the tool blocks and returns its result inline. When false, the tool is dispatched in the background and returns immediately with {job_id, status:'queued'}; collect the result later with wait {mode:'tool', job_id}. Use false for slow/long tools or when firing several in parallel.",
	}
	m["properties"] = props
	out, err := json.Marshal(m)
	if err != nil {
		return schema
	}
	return string(out)
}
