package chat

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// storePaths resolves JSON persistence locations under state/.
type storePaths struct {
	stateDir    string
	rolloutDir  string
	sessionsDir string
	memoryDir   string
	configDir   string
}

func resolveStorePaths(memoryDir string) storePaths {
	dataDir := filepath.Dir(memoryDir)
	stateDir := filepath.Join(dataDir, "state")
	// Standalone temp dirs (tests): colocate state under memoryDir parent when
	// memoryDir is not the canonical .../memory layout.
	if filepath.Base(memoryDir) != "memory" {
		stateDir = filepath.Join(memoryDir, "state")
		memoryDir = filepath.Join(memoryDir, "memory")
	}
	return storePaths{
		stateDir:    stateDir,
		rolloutDir:  filepath.Join(stateDir, "rollout"),
		sessionsDir: filepath.Join(stateDir, "sessions"),
		memoryDir:   memoryDir,
		configDir:   filepath.Join(stateDir, "config"),
	}
}

func (p storePaths) sessionsIndex() string { return filepath.Join(p.sessionsDir, "index.json") }
func (p storePaths) sessionDir(id string) string {
	if strings.HasPrefix(id, "task-") {
		return filepath.Join(p.stateDir, "tasks", id)
	}
	return filepath.Join(p.sessionsDir, id)
}
func (p storePaths) sessionTurns(id string) string {
	return filepath.Join(p.sessionDir(id), "turns.json")
}
func (p storePaths) sessionCheckpoints(id string) string {
	return filepath.Join(p.sessionDir(id), "checkpoints.json")
}
func (p storePaths) sessionUsage(id string) string {
	return filepath.Join(p.sessionDir(id), "usage.jsonl")
}
func (p storePaths) rolloutFile(sessionID string) string {
	return filepath.Join(p.rolloutDir, sessionID+".jsonl")
}
func (p storePaths) factsFile() string       { return filepath.Join(p.memoryDir, "facts.json") }
func (p storePaths) feedbackFile() string    { return filepath.Join(p.memoryDir, "feedback.jsonl") }
func (p storePaths) learningFile() string    { return filepath.Join(p.memoryDir, "learning_queue.json") }
func (p storePaths) hotCacheFile() string    { return filepath.Join(p.memoryDir, "hot_cache.json") }
func (p storePaths) prefetchLogFile() string { return filepath.Join(p.memoryDir, "prefetch_log.jsonl") }
func (p storePaths) nodesFile() string       { return filepath.Join(p.configDir, "nodes.json") }
func (p storePaths) skillsIndexFile() string { return filepath.Join(p.configDir, "skills_index.json") }
func (p storePaths) prefetchRulesFile() string {
	return filepath.Join(p.configDir, "prefetch_rules.json")
}
func (p storePaths) promptSlicesFile() string {
	return filepath.Join(p.configDir, "prompt_slices.json")
}

func (p storePaths) ensureAll() error {
	for _, d := range []string{p.rolloutDir, p.sessionsDir, p.memoryDir, p.configDir} {
		if err := ensureDir(d); err != nil {
			return err
		}
	}
	return nil
}

type sessionRecord struct {
	ID        string `json:"id"`
	Namespace string `json:"namespace"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	Active    bool   `json:"active"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	ResetAt   string `json:"reset_at,omitempty"`
}

type sessionsIndex struct {
	Sessions []sessionRecord `json:"sessions"`
}

type compactionRecord struct {
	CheckpointIndex int    `json:"checkpoint_index"`
	SummaryTurnID   int64  `json:"summary_turn_id"`
	CompactedTurns  int    `json:"compacted_turns"`
	Reason          string `json:"reason"`
	TailStartTurnID int64  `json:"tail_start_turn_id,omitempty"`
	CreatedAt       string `json:"created_at"`
}

type feedbackRecord struct {
	ID         int64   `json:"id"`
	SessionID  string  `json:"session_id"`
	TurnID     *int64  `json:"turn_id,omitempty"`
	Signal     string  `json:"signal"`
	Reward     float64 `json:"reward"`
	Correction string  `json:"correction,omitempty"`
	CreatedAt  string  `json:"created_at"`
}

type hotCacheRecord struct {
	Key       string `json:"key"`
	Payload   string `json:"payload"`
	ExpiresAt string `json:"expires_at"`
	CreatedAt string `json:"created_at"`
}

type prefetchLogRecord struct {
	SessionID     string  `json:"session_id"`
	Intent        string  `json:"intent"`
	Confidence    float64 `json:"confidence"`
	DeepCheckUsed bool    `json:"deep_check_used"`
	TaskSuccess   *bool   `json:"task_success,omitempty"`
	LatencyMS     int64   `json:"latency_ms"`
	CreatedAt     string  `json:"created_at"`
}

func loadJSONLines[T any](path string) ([]T, error) {
	mu := lockPath(path)
	defer mu()
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []T
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var row T
		if err := json.Unmarshal(line, &row); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, sc.Err()
}

func rewriteJSONL[T any](path string, rows []T) error {
	mu := lockPath(path)
	defer mu()
	if err := ensureDir(filepath.Dir(path)); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	for _, row := range rows {
		if err := enc.Encode(row); err != nil {
			f.Close()
			_ = os.Remove(tmp)
			return err
		}
	}
	if err := f.Sync(); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// RolloutDir returns the canonical rollout JSONL directory for progress streams.
func (s *Store) RolloutDir() string {
	if s == nil {
		return ""
	}
	return s.paths.rolloutDir
}
