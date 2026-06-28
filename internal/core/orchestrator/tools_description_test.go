package orchestrator

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/bridges/provider"
	"github.com/jahrulnr/sapaloq/internal/tooldocs"
)

// registeredToolNames is the set of tools with schemas registered in tools.go init.
var registeredToolNames = []string{
	"read_file", "glob", "edit_file", "delete_file", "sapaloq_send_steering",
	"sapaloq_request_decision", "search", "list_dir", "web_search", "web_fetch",
	"write_file", "create_file", "exec", "read_image", "sapaloq_spawn_plan",
	"sapaloq_spawn_agent", "write_plan", "read_plan", "sapaloq_update_task_progress",
	"sapaloq_complete_task", "sapaloq_fail_task", "request_clarification",
	"sapaloq_answer_clarification", "sapaloq_spawn_scribe", "scribe_write_note",
	"desktop_notify", "desktop_dnd_status", "wait", "sapaloq_cancel_job",
	"sapaloq_stop", "sapaloq_get_task_status", "sapaloq_resume_task",
}

func TestRegisteredToolsHaveWireDescriptions(t *testing.T) {
	for _, name := range registeredToolNames {
		doc := tooldocs.Description(name)
		if strings.TrimSpace(doc) == "" {
			t.Errorf("registered tool %q has no tooldocs description", name)
		}
		wire := provider.RegisteredToolDescription(name)
		if strings.TrimSpace(wire) == "" {
			t.Errorf("registered tool %q has no provider description after init", name)
		}
		if wire != doc {
			t.Errorf("tool %q provider description != tooldocs (%q vs %q)", name, wire, doc)
		}
	}
}

func TestLifecycleToolsOmitWaitForOutput(t *testing.T) {
	lifecycle := []string{
		"sapaloq_stop", "sapaloq_complete_task", "wait", "sapaloq_spawn_agent",
	}
	for _, name := range lifecycle {
		schema := string(provider.RegisteredToolSchema(name))
		if schemaHasWaitForOutputArg(schema) {
			t.Errorf("tool %q schema should not inject wait_for_output property", name)
		}
	}
}

func TestWorkToolsIncludeWaitForOutput(t *testing.T) {
	work := []string{"read_file", "exec", "write_plan"}
	for _, name := range work {
		schema := string(provider.RegisteredToolSchema(name))
		if !schemaHasWaitForOutputArg(schema) {
			t.Errorf("tool %q schema should include wait_for_output property", name)
		}
	}
}

func schemaHasWaitForOutputArg(schema string) bool {
	var m map[string]any
	if err := json.Unmarshal([]byte(schema), &m); err != nil {
		return false
	}
	props, _ := m["properties"].(map[string]any)
	_, ok := props["wait_for_output"]
	return ok
}
