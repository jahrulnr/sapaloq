// Package embed bundles static binary assets shipped inside the SapaLOQ
// binaries (currently the completion notification sounds). Assets live next to
// this file so every consumer (the widget, the core service) shares one
// source of truth via //go:embed instead of duplicating bytes per binary.
package embed

import _ "embed"

// Completion chimes. The generic NotificationWav is the fallback used for the
// foreground orchestrator run; NotificationPlannerWav / NotificationAgentWav
// are role-specific chimes for the corresponding sub-agent completions. Each
// is exposed as raw bytes so the widget can hand the browser a data: URI.
//
//go:embed notification.wav
var NotificationWav []byte

//go:embed notification-planner.wav
var NotificationPlannerWav []byte

//go:embed notification-agent.wav
var NotificationAgentWav []byte

// NotificationWavForRole resolves the chime bytes for a sub-agent role. Unknown
// roles fall back to the generic chime so a new role never goes silent.
func NotificationWavForRole(role string) []byte {
	switch role {
	case "planner":
		return NotificationPlannerWav
	case "task-runner", "agent":
		return NotificationAgentWav
	default:
		return NotificationWav
	}
}
