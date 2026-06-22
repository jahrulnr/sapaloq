package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/config"
)

func (o *Orchestrator) runtimeContextMessage() bridge.Message {
	dirs := config.RuntimeDirs(o.snapshot().cfg)
	content := fmt.Sprintf(`[SapaLOQ runtime variables]
config_path=%s
data_path=%s
memory_path=%s
state_path=%s
workspace=%s
prompts_path=%s
skills_path=%s
vault_path=%s
run_path=%s
etc_path=%s
runtime_roadmap=%s

Use these paths instead of guessing. Relative tool paths resolve from the actor workspace.`,
		o.cfgPath, dirs.DataDir, dirs.MemoryDir, dirs.StateDir, dirs.WorkspaceDir,
		dirs.PromptsDir, dirs.SkillsDir, dirs.VaultDir, dirs.RunDir, dirs.EtcDir,
		filepath.Join(dirs.EtcDir, "ROADMAP.md"))
	return bridge.Message{Role: "system", Content: content}
}

func (o *Orchestrator) materializeRuntimeRoadmap() {
	dirs := config.RuntimeDirs(o.snapshot().cfg)
	if dirs.EtcDir == "" {
		return
	}
	content := o.runtimeContextMessage().Content + `

[Workspace contract]
- Every actor starts at workspace unless it has a persisted cwd.
- Relative file and exec paths follow that actor cwd.
- cd persists for the same actor.
`
	if os.MkdirAll(dirs.EtcDir, 0o755) != nil {
		return
	}
	_ = writeFileAtomic(filepath.Join(dirs.EtcDir, "ROADMAP.md"), []byte(content), 0o600)
}
