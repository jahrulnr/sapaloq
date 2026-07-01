package prompts

import "strings"

// Tier classifies where a prompt lives and whether the user may override it.
type Tier string

const (
	TierEditable Tier = "editable"
	TierInternal Tier = "internal"
	TierBridge   Tier = "bridge"
)

// CatalogEntry describes one prompt key in the centered registry.
type CatalogEntry struct {
	Key         string
	Tier        Tier
	File        string // embedded path or external reference
	UsedBy      string
	Editable    bool
	Description string
}

// Internal prompt keys (ship-only, go:embed under internal/).
const (
	KeyAutopilotClarificationPending = "internal.autopilot.clarification-pending"
	KeyAutopilotRunning              = "internal.autopilot.running"
	KeyAutopilotRunningEscalated     = "internal.autopilot.running-escalated"
	KeyAutopilotDefaultStop          = "internal.autopilot.default-stop"

	KeyClarificationMediator = "internal.clarification.mediator"

	KeyBackgroundFallbackSystem   = "internal.background.fallback-system"
	KeyBackgroundPlanHandoff      = "internal.background.plan-handoff-prefix"
	KeyBackgroundClarifyPrefix    = "internal.background.clarify-answer-prefix"
	KeyBackgroundClarifySuffix    = "internal.background.clarify-answer-suffix"

	KeyResumeNudgeBase          = "internal.resume.nudge-base"
	KeyResumeNudgeStopped       = "internal.resume.nudge-stopped"
	KeyResumeNudgePriorFailure  = "internal.resume.nudge-prior-failure"
	KeyResumeNudgeFooter        = "internal.resume.nudge-footer"

	KeyCompactionUserPrefix     = "internal.compaction.user-prefix"
	KeyCompactionCheckpointHdr  = "internal.compaction.checkpoint-header"
	KeyCompactionCheckpointResume = "internal.compaction.checkpoint-resume"

	KeyBlockNegativeGuidanceHeader = "internal.blocks.negative-guidance-header"
	KeyBlockPrefetchHeader         = "internal.blocks.prefetch-header"
	KeyBlockPrefetchAntiDeepCheck  = "internal.blocks.prefetch-anti-deep-check"
	KeyBlockSkillsHeader           = "internal.blocks.skills-header"
	KeyBlockActorEventsHeader      = "internal.blocks.actor-events-header"
	KeyBlockActorEventsFooter      = "internal.blocks.actor-events-footer"
	KeyBlockMalformedToolRetry     = "internal.blocks.malformed-tool-retry"

	KeyTemplateRuntimeContext      = "internal.templates.runtime-context"
	KeyTemplateRuntimeRoadmapSuffix = "internal.templates.runtime-roadmap-suffix"
)

// internalKeyFile maps internal catalog keys to embedded file paths.
var internalKeyFile = map[string]string{
	KeyAutopilotClarificationPending: "internal/autopilot/clarification-pending.md",
	KeyAutopilotRunning:              "internal/autopilot/running.md",
	KeyAutopilotRunningEscalated:     "internal/autopilot/running-escalated.md",
	KeyAutopilotDefaultStop:          "internal/autopilot/default-stop.md",

	KeyClarificationMediator: "internal/clarification/mediator.md",

	KeyBackgroundFallbackSystem: "internal/background/fallback-system.md",
	KeyBackgroundPlanHandoff:    "internal/background/plan-handoff-prefix.md",
	KeyBackgroundClarifyPrefix:  "internal/background/clarify-answer-prefix.md",
	KeyBackgroundClarifySuffix:  "internal/background/clarify-answer-suffix.md",

	KeyResumeNudgeBase:         "internal/resume/nudge-base.md",
	KeyResumeNudgeStopped:      "internal/resume/nudge-stopped.md",
	KeyResumeNudgePriorFailure: "internal/resume/nudge-prior-failure.md",
	KeyResumeNudgeFooter:       "internal/resume/nudge-footer.md",

	KeyCompactionUserPrefix:       "internal/compaction/user-prefix.md",
	KeyCompactionCheckpointHdr:    "internal/compaction/checkpoint-header.md",
	KeyCompactionCheckpointResume: "internal/compaction/checkpoint-resume.md",

	KeyBlockNegativeGuidanceHeader: "internal/blocks/negative-guidance-header.md",
	KeyBlockPrefetchHeader:         "internal/blocks/prefetch-header.md",
	KeyBlockPrefetchAntiDeepCheck:  "internal/blocks/prefetch-anti-deep-check.md",
	KeyBlockSkillsHeader:           "internal/blocks/skills-header.md",
	KeyBlockActorEventsHeader:      "internal/blocks/actor-events-header.md",
	KeyBlockActorEventsFooter:      "internal/blocks/actor-events-footer.md",
	KeyBlockMalformedToolRetry:     "internal/blocks/malformed-tool-retry.md",

	KeyTemplateRuntimeContext:       "internal/templates/runtime-context.md",
	KeyTemplateRuntimeRoadmapSuffix: "internal/templates/runtime-roadmap-suffix.md",
}

// Catalog returns the authoritative list of all known prompt keys.
func Catalog() []CatalogEntry {
	out := make([]CatalogEntry, 0, len(roles())+len(internalKeyFile)+1)
	for _, r := range roles() {
		out = append(out, CatalogEntry{
			Key:         r.role,
			Tier:        TierEditable,
			File:        "defaults/" + r.file,
			UsedBy:      "orchestrator.systemPrompt",
			Editable:    true,
			Description: "User-editable role/layer prompt synced to prompts.dir",
		})
	}
	for key, file := range internalKeyFile {
		out = append(out, CatalogEntry{
			Key:         key,
			Tier:        TierInternal,
			File:        file,
			UsedBy:      internalUsedBy(key),
			Editable:    false,
			Description: internalDescription(key),
		})
	}
	out = append(out, CatalogEntry{
		Key:         "bridge.cursor.guard",
		Tier:        TierBridge,
		File:        "internal/bridges/cursor/guard.go",
		UsedBy:      "bridges/cursor wire encode",
		Editable:    false,
		Description: "Cursor tool-scope guard instructions (modular per bridge)",
	})
	return out
}

func internalUsedBy(key string) string {
	switch {
	case strings.HasPrefix(key, "internal.autopilot"):
		return "orchestrator.buildAutopilotContinuation"
	case key == KeyClarificationMediator:
		return "orchestrator.runClarificationResolver"
	case strings.HasPrefix(key, "internal.background"):
		return "orchestrator.buildBackgroundActorMessages"
	case strings.HasPrefix(key, "internal.resume"):
		return "orchestrator.buildResumeNudge"
	case strings.HasPrefix(key, "internal.compaction"):
		return "orchestrator.compaction"
	case key == KeyBlockMalformedToolRetry:
		return "orchestrator.conversation malformed-tool retry"
	case strings.HasPrefix(key, "internal.blocks"):
		return "orchestrator per-turn system blocks"
	case strings.HasPrefix(key, "internal.templates"):
		return "orchestrator.runtimeContextMessage"
	default:
		return "orchestrator"
	}
}

func internalDescription(key string) string {
	switch key {
	case KeyAutopilotClarificationPending:
		return "Autopilot nudge when a delegated task awaits clarification"
	case KeyAutopilotRunning:
		return "Autopilot nudge while background tasks are running"
	case KeyAutopilotRunningEscalated:
		return "Escalated autopilot nudge after repeated narration-only turns"
	case KeyAutopilotDefaultStop:
		return "Default autopilot nudge to call sapaloq_stop"
	case KeyClarificationMediator:
		return "System prompt for clarification decision mediator"
	case KeyBackgroundFallbackSystem:
		return "Fallback system prompt when role prompt is empty"
	case KeyBackgroundPlanHandoff:
		return "Prefix before approved plan markdown for task-runner"
	case KeyBackgroundClarifyPrefix, KeyBackgroundClarifySuffix:
		return "Wrapper around clarification answer when resuming task"
	case KeyResumeNudgeBase:
		return "Base resume instruction for failed/stopped tasks"
	case KeyResumeNudgeStopped:
		return "Resume line when prior status was stopped"
	case KeyResumeNudgePriorFailure:
		return "Resume line with prior failure details (templated)"
	case KeyResumeNudgeFooter:
		return "Resume footer urging remaining work"
	case KeyCompactionUserPrefix:
		return "User-turn prefix for compaction summarization"
	case KeyCompactionCheckpointHdr:
		return "Checkpoint summary header in compacted transcript"
	case KeyCompactionCheckpointResume:
		return "Instruction to resume from checkpoint without redoing work"
	case KeyBlockNegativeGuidanceHeader:
		return "Header for do_not_repeat feedback facts"
	case KeyBlockPrefetchHeader:
		return "Header for prefetched memory index facts"
	case KeyBlockPrefetchAntiDeepCheck:
		return "Anti deep-check line when prefetch confidence is high"
	case KeyBlockSkillsHeader:
		return "Header for matched skill bodies"
	case KeyBlockActorEventsHeader, KeyBlockActorEventsFooter:
		return "Actor control event steering block"
	case KeyBlockMalformedToolRetry:
		return "Retry nudge when tool calls were malformed"
	case KeyTemplateRuntimeContext:
		return "Runtime paths system block (templated)"
	case KeyTemplateRuntimeRoadmapSuffix:
		return "Workspace/host contract appended to ROADMAP.md"
	default:
		return "Internal SapaLOQ orchestration steering"
	}
}
