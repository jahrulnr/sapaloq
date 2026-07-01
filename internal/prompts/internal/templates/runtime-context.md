---
# SapaLOQ runtime variables

workspace={{.Workspace}}
config_path={{.ConfigPath}}
data_path={{.DataPath}}
memory_path={{.MemoryPath}}
state_path={{.StatePath}}
prompts_path={{.PromptsPath}}
skills_path={{.SkillsPath}}
vault_path={{.VaultPath}}
run_path={{.RunPath}}
etc_path={{.EtcPath}}
runtime_roadmap={{.RuntimeRoadmap}}

Authoritative tool cwd is workspace= above. Relative paths and default exec cwd resolve from it; absolute paths are used as given.
