package codex

import "encoding/json"

// Event type discriminators on the top-level `type` field (CONTRACT §3).
const (
	typeThreadStarted = "thread.started"
	typeTurnStarted   = "turn.started"
	typeItemStarted   = "item.started"
	typeItemCompleted = "item.completed"
	typeTurnCompleted = "turn.completed"
	typeError         = "error"
	typeTurnFailed    = "turn.failed"
)

// item.type discriminators on item.started / item.completed (CONTRACT §3.3).
const (
	itemAgentMessage     = "agent_message"
	itemReasoning        = "reasoning"
	itemCommandExecution = "command_execution"
	itemError            = "error"
)

// command_execution status values (CONTRACT §3.3).
const (
	cmdStatusInProgress = "in_progress"
	cmdStatusCompleted  = "completed"
)

// Status sub-kinds carried on a bridge.EventStatus emitted by this bridge. They
// let a consumer distinguish session capture, working telemetry, and tool
// completion. StatusToolDone is followed by a ":exit=<code>" suffix on the wire.
const (
	StatusSession  = "session"   // thread.started: thread_id captured for resume
	StatusWorking  = "working"   // turn.started
	StatusToolDone = "tool_done" // command_execution completed (exit code in suffix)
)

// codexEvent is the decode target for a single JSONL line emitted by
// `codex exec --json` on stdout. The `Type` field discriminates the event; the
// remaining fields are optional and only populated for the matching type. See
// CODEX_CLI_CONTRACT.md §3 for the authoritative schema (codex 0.141.0).
type codexEvent struct {
	Type     string      `json:"type"`
	ThreadID string      `json:"thread_id"`
	Item     *codexItem  `json:"item"`
	Usage    *codexUsage `json:"usage"`
	// Error appears on `turn.failed` ({"error":{"message":...}}).
	Error *codexError `json:"error"`
	// Message appears on a top-level `error` event ({"message":...}).
	Message string `json:"message"`
}

// codexItem is the payload of item.started / item.completed events. The set of
// populated fields depends on `Type` (the item.type): agent_message uses Text,
// command_execution uses Command/AggregatedOutput/ExitCode/Status.
type codexItem struct {
	ID   string `json:"id"`
	Type string `json:"type"`

	// agent_message / reasoning / error
	Text    string `json:"text"`
	Message string `json:"message"`

	// command_execution
	Command          string `json:"command"`
	AggregatedOutput string `json:"aggregated_output"`
	// ExitCode is null while in_progress, an integer once completed; a pointer
	// distinguishes "not yet known" (nil) from a real 0 exit.
	ExitCode *int   `json:"exit_code"`
	Status   string `json:"status"`
}

// codexUsage carries token accounting on turn.completed.
type codexUsage struct {
	InputTokens           int `json:"input_tokens"`
	CachedInputTokens     int `json:"cached_input_tokens"`
	OutputTokens          int `json:"output_tokens"`
	ReasoningOutputTokens int `json:"reasoning_output_tokens"`
}

// codexError carries the failure payload on turn.failed.
type codexError struct {
	Message string `json:"message"`
}

// decodeLine parses one JSONL line. It returns ok=false for malformed JSON (or
// a well-formed object without a `type` discriminator) so the caller can
// tolerantly skip junk lines without crashing (CONTRACT §3.3 / §5).
func decodeLine(line []byte) (codexEvent, bool) {
	var ev codexEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return codexEvent{}, false
	}
	if ev.Type == "" {
		return codexEvent{}, false
	}
	return ev, true
}
