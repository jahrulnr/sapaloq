package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const defaultSessionID = "default"

// Turn is a normalized chat turn used for active-session restore and context assembly.
type Turn struct {
	ID                int64      `json:"id"`
	SessionID         string     `json:"session_id"`
	Seq               int        `json:"seq"`
	Role              string     `json:"role"`
	Content           string     `json:"content"`
	TokenEstimate     int        `json:"token_estimate"`
	IncludedInContext bool       `json:"included_in_context"`
	CompactedAt       *time.Time `json:"compacted_at,omitempty"`
	CheckpointIndex   int        `json:"checkpoint_index,omitempty"`
	GenerationID      string     `json:"generation_id,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
}

// Usage summarizes active context usage for the widget.
type Usage struct {
	SessionID       string `json:"session_id"`
	UsedTokens      int    `json:"used_tokens"`
	ContextWindow   int    `json:"context_window"`
	Percent         int    `json:"percent"`
	Provider        string `json:"provider"`
	Model           string `json:"model"`
	CompactedTurns  int    `json:"compacted_turns"`
	ActiveTurns     int    `json:"active_turns"`
	LastCompactedAt string `json:"last_compacted_at,omitempty"`
}

// Store owns JSON/JSONL persistence under state/. Rollout JSONL is the canonical
// audit stream for tool events; turns live in state/sessions/<id>/turns.json.
type Store struct {
	paths storePaths
	mu    sync.Mutex

	nextFactID     int64
	nextFeedbackID int64
	nextLearningID int64
	nextPrefetchID int64

	facts         []Fact
	learningQueue []LearningEvent
	hotCache      map[string]hotCacheRecord
	nodes         []Node
	prefetchRules []PrefetchRule
	skillsIndex   []SkillIndexEntry
	promptSlices  []PromptSlice
}

// Open initializes the JSON-backed store. memoryDir is the legacy memory root
// ({dataDir}/memory); state files live under {dataDir}/state.
func Open(memoryDir string) (*Store, error) {
	if memoryDir == "" {
		return nil, errors.New("memory dir is required")
	}
	paths := resolveStorePaths(memoryDir)
	if err := paths.ensureAll(); err != nil {
		return nil, err
	}
	s := &Store{
		paths: paths, hotCache: make(map[string]hotCacheRecord),
		nextFactID: 1, nextFeedbackID: 1, nextLearningID: 1, nextPrefetchID: 1,
	}
	if err := s.loadAuxiliary(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return nil }

func (s *Store) loadAuxiliary() error {
	if err := readJSONFile(s.paths.factsFile(), &s.facts); err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, f := range s.facts {
		if f.ID >= s.nextFactID {
			s.nextFactID = f.ID + 1
		}
	}
	if err := readJSONFile(s.paths.learningFile(), &s.learningQueue); err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, e := range s.learningQueue {
		if e.ID >= s.nextLearningID {
			s.nextLearningID = e.ID + 1
		}
	}
	var cache []hotCacheRecord
	if err := readJSONFile(s.paths.hotCacheFile(), &cache); err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, c := range cache {
		s.hotCache[c.Key] = c
	}
	if err := readJSONFile(s.paths.nodesFile(), &s.nodes); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := readJSONFile(s.paths.prefetchRulesFile(), &s.prefetchRules); err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, r := range s.prefetchRules {
		if r.ID >= s.nextPrefetchID {
			s.nextPrefetchID = r.ID + 1
		}
	}
	if err := readJSONFile(s.paths.skillsIndexFile(), &s.skillsIndex); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := readJSONFile(s.paths.promptSlicesFile(), &s.promptSlices); err != nil && !os.IsNotExist(err) {
		return err
	}
	feedback, err := loadJSONLines[feedbackRecord](s.paths.feedbackFile())
	if err != nil {
		return err
	}
	for _, fb := range feedback {
		if fb.ID >= s.nextFeedbackID {
			s.nextFeedbackID = fb.ID + 1
		}
	}
	return nil
}

func (s *Store) saveFacts() error {
	return writeJSONFileAtomic(s.paths.factsFile(), s.facts)
}

func (s *Store) saveLearning() error {
	return writeJSONFileAtomic(s.paths.learningFile(), s.learningQueue)
}

func (s *Store) saveHotCache() error {
	cache := make([]hotCacheRecord, 0, len(s.hotCache))
	for _, c := range s.hotCache {
		cache = append(cache, c)
	}
	return writeJSONFileAtomic(s.paths.hotCacheFile(), cache)
}

func (s *Store) saveNodes() error {
	return writeJSONFileAtomic(s.paths.nodesFile(), s.nodes)
}

func (s *Store) savePrefetchRules() error {
	return writeJSONFileAtomic(s.paths.prefetchRulesFile(), s.prefetchRules)
}

func (s *Store) saveSkillsIndex() error {
	return writeJSONFileAtomic(s.paths.skillsIndexFile(), s.skillsIndex)
}

func (s *Store) savePromptSlices() error {
	return writeJSONFileAtomic(s.paths.promptSlicesFile(), s.promptSlices)
}

func (s *Store) loadSessionsIndex() (sessionsIndex, error) {
	var idx sessionsIndex
	err := readJSONFile(s.paths.sessionsIndex(), &idx)
	if os.IsNotExist(err) {
		return sessionsIndex{}, nil
	}
	return idx, err
}

func (s *Store) saveSessionsIndex(idx sessionsIndex) error {
	return writeJSONFileAtomic(s.paths.sessionsIndex(), idx)
}

func (s *Store) loadSessionTurns(sessionID string) ([]Turn, error) {
	var turns []Turn
	err := readJSONFile(s.paths.sessionTurns(sessionID), &turns)
	if os.IsNotExist(err) {
		return nil, nil
	}
	return turns, err
}

func (s *Store) saveSessionTurns(sessionID string, turns []Turn) error {
	if err := ensureDir(s.paths.sessionDir(sessionID)); err != nil {
		return err
	}
	return writeJSONFileAtomic(s.paths.sessionTurns(sessionID), turns)
}

func (s *Store) loadSessionCheckpoints(sessionID string) ([]compactionRecord, error) {
	var ckpts []compactionRecord
	err := readJSONFile(s.paths.sessionCheckpoints(sessionID), &ckpts)
	if os.IsNotExist(err) {
		return nil, nil
	}
	return ckpts, err
}

func (s *Store) saveSessionCheckpoints(sessionID string, ckpts []compactionRecord) error {
	if err := ensureDir(s.paths.sessionDir(sessionID)); err != nil {
		return err
	}
	return writeJSONFileAtomic(s.paths.sessionCheckpoints(sessionID), ckpts)
}

func (s *Store) ActiveSession(ctx context.Context, provider, model string) (string, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	idx, err := s.loadSessionsIndex()
	if err != nil {
		return "", err
	}
	for i := range idx.Sessions {
		if idx.Sessions[i].Active {
			return idx.Sessions[i].ID, nil
		}
	}
	return s.resetLocked(provider, model)
}

func (s *Store) Reset(ctx context.Context, provider, model string) (string, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resetLocked(provider, model)
}

func (s *Store) resetLocked(provider, model string) (string, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	id := fmt.Sprintf("chat-%d", time.Now().UTC().UnixNano())
	idx, err := s.loadSessionsIndex()
	if err != nil {
		return "", err
	}
	for i := range idx.Sessions {
		if idx.Sessions[i].Active {
			idx.Sessions[i].Active = false
			idx.Sessions[i].ResetAt = now
			idx.Sessions[i].UpdatedAt = now
		}
	}
	idx.Sessions = append(idx.Sessions, sessionRecord{
		ID: id, Namespace: "default", Provider: provider, Model: model,
		Active: true, CreatedAt: now, UpdatedAt: now, ResetAt: now,
	})
	if err := s.saveSessionsIndex(idx); err != nil {
		return "", err
	}
	if err := s.saveSessionTurns(id, nil); err != nil {
		return "", err
	}
	return id, nil
}

func (s *Store) ClearSession(ctx context.Context, sessionID string) error {
	_ = ctx
	if sessionID == "" {
		return errors.New("session id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	idx, err := s.loadSessionsIndex()
	if err != nil {
		return err
	}
	found := false
	for i := range idx.Sessions {
		if idx.Sessions[i].ID == sessionID {
			idx.Sessions[i].UpdatedAt = now
			found = true
		}
	}
	if !found {
		return fmt.Errorf("session %q not found", sessionID)
	}
	if err := s.saveSessionsIndex(idx); err != nil {
		return err
	}
	if err := s.saveSessionTurns(sessionID, nil); err != nil {
		return err
	}
	return s.saveSessionCheckpoints(sessionID, nil)
}

func (s *Store) DeleteSession(ctx context.Context, sessionID string) error {
	_ = ctx
	if sessionID == "" {
		return errors.New("session id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	idx, err := s.loadSessionsIndex()
	if err != nil {
		return err
	}
	var kept []sessionRecord
	for _, rec := range idx.Sessions {
		if rec.ID != sessionID {
			kept = append(kept, rec)
		}
	}
	idx.Sessions = kept
	if err := s.saveSessionsIndex(idx); err != nil {
		return err
	}
	_ = os.RemoveAll(s.paths.sessionDir(sessionID))
	_ = os.Remove(s.paths.rolloutFile(sessionID))
	return nil
}

type SessionSummary struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Active    bool   `json:"active"`
	TurnCount int    `json:"turn_count"`
	UpdatedAt string `json:"updated_at"`
	CreatedAt string `json:"created_at"`
}

func (s *Store) ListSessions(ctx context.Context, limit int) ([]SessionSummary, error) {
	_ = ctx
	if limit <= 0 {
		limit = 50
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	idx, err := s.loadSessionsIndex()
	if err != nil {
		return nil, err
	}
	recs := append([]sessionRecord(nil), idx.Sessions...)
	sort.Slice(recs, func(i, j int) bool {
		if recs[i].Active != recs[j].Active {
			return recs[i].Active
		}
		return recs[i].UpdatedAt > recs[j].UpdatedAt
	})
	if len(recs) > limit {
		recs = recs[:limit]
	}
	out := make([]SessionSummary, 0, len(recs))
	for _, rec := range recs {
		title, count, derr := s.sessionTitleAndCountLocked(rec.ID)
		if derr != nil {
			return nil, derr
		}
		out = append(out, SessionSummary{
			ID: rec.ID, Title: title, Active: rec.Active,
			TurnCount: count, UpdatedAt: rec.UpdatedAt, CreatedAt: rec.CreatedAt,
		})
	}
	return out, nil
}

func (s *Store) sessionTitleAndCountLocked(sessionID string) (string, int, error) {
	turns, err := s.loadSessionTurns(sessionID)
	if err != nil {
		return "", 0, err
	}
	count := 0
	var firstUser string
	for _, t := range turns {
		switch t.Role {
		case "user", "assistant", "error":
			count++
		}
		if firstUser == "" && t.Role == "user" {
			firstUser = t.Content
		}
	}
	return summarizeTitle(firstUser), count, nil
}

func summarizeTitle(content string) string {
	line := strings.TrimSpace(content)
	if line == "" {
		return ""
	}
	if idx := strings.IndexAny(line, "\r\n"); idx >= 0 {
		line = strings.TrimSpace(line[:idx])
	}
	const maxLen = 48
	if len([]rune(line)) > maxLen {
		runes := []rune(line)
		line = strings.TrimSpace(string(runes[:maxLen])) + "…"
	}
	return line
}

func (s *Store) Activate(ctx context.Context, sessionID string) error {
	_ = ctx
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return errors.New("session id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	idx, err := s.loadSessionsIndex()
	if err != nil {
		return err
	}
	found := false
	for i := range idx.Sessions {
		if idx.Sessions[i].ID == sessionID {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("session %q not found", sessionID)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for i := range idx.Sessions {
		idx.Sessions[i].Active = idx.Sessions[i].ID == sessionID
		if idx.Sessions[i].Active {
			idx.Sessions[i].UpdatedAt = now
		}
	}
	return s.saveSessionsIndex(idx)
}

func (s *Store) AppendTurn(ctx context.Context, sessionID, role, content string, tokenEstimate int) error {
	_, err := s.AppendTurnID(ctx, sessionID, role, content, tokenEstimate)
	return err
}

// AppendAutopilotTurn records a tool-less autopilot nudge for usage/UI accounting
// without counting it toward compaction headroom (IncludedInContext: false).
func (s *Store) AppendAutopilotTurn(ctx context.Context, sessionID, content string, tokenEstimate int) error {
	_, err := s.AppendTurnIDWithFlags(ctx, sessionID, "autopilot", content, tokenEstimate, "", false)
	return err
}

func (s *Store) AppendTurnID(ctx context.Context, sessionID, role, content string, tokenEstimate int) (int64, error) {
	return s.AppendTurnIDWithGeneration(ctx, sessionID, role, content, tokenEstimate, "")
}

func (s *Store) AppendTurnIDWithGeneration(ctx context.Context, sessionID, role, content string, tokenEstimate int, generationID string) (int64, error) {
	return s.AppendTurnIDWithFlags(ctx, sessionID, role, content, tokenEstimate, generationID, true)
}

func (s *Store) AppendTurnIDWithFlags(ctx context.Context, sessionID, role, content string, tokenEstimate int, generationID string, includedInContext bool) (int64, error) {
	_ = ctx
	if strings.TrimSpace(content) == "" {
		return 0, nil
	}
	if sessionID == "" {
		sessionID = defaultSessionID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	turns, err := s.loadSessionTurns(sessionID)
	if err != nil {
		return 0, err
	}
	seq := 0
	var maxID int64
	for _, t := range turns {
		if t.Seq > seq {
			seq = t.Seq
		}
		if t.ID > maxID {
			maxID = t.ID
		}
	}
	seq++
	id := maxID + 1
	now := time.Now().UTC()
	turns = append(turns, Turn{
		ID: id, SessionID: sessionID, Seq: seq, Role: role, Content: content,
		TokenEstimate: tokenEstimate, IncludedInContext: includedInContext, GenerationID: generationID,
		CreatedAt: now,
	})
	if err := s.saveSessionTurns(sessionID, turns); err != nil {
		return 0, err
	}
	if err := s.touchSessionLocked(sessionID); err != nil {
		return 0, err
	}
	return id, nil
}

func (s *Store) touchSessionLocked(sessionID string) error {
	idx, err := s.loadSessionsIndex()
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for i := range idx.Sessions {
		if idx.Sessions[i].ID == sessionID {
			idx.Sessions[i].UpdatedAt = now
			return s.saveSessionsIndex(idx)
		}
	}
	return nil
}

func (s *Store) Turn(ctx context.Context, sessionID string, turnID int64) (Turn, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	turns, err := s.loadSessionTurns(sessionID)
	if err != nil {
		return Turn{}, err
	}
	for _, t := range turns {
		if t.ID == turnID {
			return t, nil
		}
	}
	return Turn{}, fmt.Errorf("turn %d not found", turnID)
}

// SetTurnGenerationID re-tags an existing turn with the active generation id
// (e.g. chat retry reuses the user turn under a new runSeq).
func (s *Store) SetTurnGenerationID(ctx context.Context, sessionID string, turnID int64, generationID string) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	turns, err := s.loadSessionTurns(sessionID)
	if err != nil {
		return err
	}
	found := false
	for i := range turns {
		if turns[i].ID == turnID {
			turns[i].GenerationID = generationID
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("turn %d not found", turnID)
	}
	return s.saveSessionTurns(sessionID, turns)
}

func (s *Store) DeleteFromTurn(ctx context.Context, sessionID string, turnID int64) error {
	return s.deleteRelativeToTurn(ctx, sessionID, turnID, true)
}

func (s *Store) DeleteAfterTurn(ctx context.Context, sessionID string, turnID int64) error {
	return s.deleteRelativeToTurn(ctx, sessionID, turnID, false)
}

func (s *Store) deleteRelativeToTurn(ctx context.Context, sessionID string, turnID int64, inclusive bool) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	turns, err := s.loadSessionTurns(sessionID)
	if err != nil {
		return err
	}
	var target *Turn
	for i := range turns {
		if turns[i].ID == turnID {
			target = &turns[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("turn %d not found", turnID)
	}
	var kept []Turn
	for _, t := range turns {
		if inclusive {
			if t.Seq < target.Seq {
				kept = append(kept, t)
			}
		} else if t.Seq <= target.Seq {
			kept = append(kept, t)
		}
	}
	if err := s.saveSessionTurns(sessionID, kept); err != nil {
		return err
	}
	return s.touchSessionLocked(sessionID)
}

func (s *Store) ActiveTurns(ctx context.Context, sessionID string, includeCompacted bool) ([]Turn, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	turns, err := s.loadSessionTurns(sessionID)
	if err != nil {
		return nil, err
	}
	if includeCompacted {
		return append([]Turn(nil), turns...), nil
	}
	var out []Turn
	for _, t := range turns {
		if t.IncludedInContext {
			out = append(out, t)
		}
	}
	return out, nil
}

func (s *Store) Compact(ctx context.Context, sessionID string, keepRecent int, summary string, estimate func(string) int) (int, error) {
	if keepRecent < 2 {
		keepRecent = 2
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	turns, err := s.loadSessionTurns(sessionID)
	if err != nil {
		return 0, err
	}
	var active []Turn
	for _, t := range turns {
		if t.IncludedInContext {
			active = append(active, t)
		}
	}
	if len(active) <= keepRecent+1 {
		return 0, nil
	}
	cutoff := len(active) - keepRecent
	now := time.Now().UTC()
	archiveIDs := make(map[int64]bool)
	for _, t := range active[:cutoff] {
		archiveIDs[t.ID] = true
	}
	for i := range turns {
		if archiveIDs[turns[i].ID] {
			turns[i].IncludedInContext = false
			turns[i].CompactedAt = &now
		}
	}
	seq := 0
	var maxID int64
	for _, t := range turns {
		if t.Seq > seq {
			seq = t.Seq
		}
		if t.ID > maxID {
			maxID = t.ID
		}
	}
	seq++
	summaryID := maxID + 1
	turns = append(turns, Turn{
		ID: summaryID, SessionID: sessionID, Seq: seq, Role: "system", Content: summary,
		TokenEstimate: estimate(summary), IncludedInContext: true, CreatedAt: now,
	})
	ckpts, _ := s.loadSessionCheckpoints(sessionID)
	ckpts = append(ckpts, compactionRecord{
		SummaryTurnID: summaryID, CompactedTurns: cutoff, CreatedAt: now.Format(time.RFC3339Nano),
	})
	if err := s.saveSessionTurns(sessionID, turns); err != nil {
		return 0, err
	}
	if err := s.saveSessionCheckpoints(sessionID, ckpts); err != nil {
		return 0, err
	}
	return cutoff, s.touchSessionLocked(sessionID)
}

type Checkpoint struct {
	Index           int       `json:"index"`
	SummaryTurnID   int64     `json:"summary_turn_id"`
	Summary         string    `json:"summary"`
	Reason          string    `json:"reason"`
	CompactedTurns  int       `json:"compacted_turns"`
	TailStartTurnID int64     `json:"tail_start_turn_id,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

type CheckpointResult struct {
	Index           int    `json:"index"`
	SummaryTurnID   int64  `json:"summary_turn_id"`
	Reason          string `json:"reason"`
	CompactedTurns  int    `json:"compacted_turns"`
	TailStartTurnID int64  `json:"tail_start_turn_id,omitempty"`
}

type TailPolicy struct {
	ArchiveTurnIDs  []int64
	TailStartTurnID int64
}

func (s *Store) CreateCheckpoint(ctx context.Context, sessionID, summary, reason string, tail TailPolicy, estimate func(string) int) (CheckpointResult, error) {
	_ = ctx
	if sessionID == "" {
		sessionID = defaultSessionID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	turns, err := s.loadSessionTurns(sessionID)
	if err != nil {
		return CheckpointResult{}, err
	}
	ckpts, err := s.loadSessionCheckpoints(sessionID)
	if err != nil {
		return CheckpointResult{}, err
	}
	nextIndex := 1
	for _, c := range ckpts {
		if c.CheckpointIndex >= nextIndex {
			nextIndex = c.CheckpointIndex + 1
		}
	}
	now := time.Now().UTC()
	archive := make(map[int64]bool)
	for _, id := range tail.ArchiveTurnIDs {
		archive[id] = true
	}
	archived := 0
	for i := range turns {
		if archive[turns[i].ID] {
			turns[i].IncludedInContext = false
			turns[i].CompactedAt = &now
			archived++
		}
	}
	seq, maxID := maxSeqID(turns)
	seq++
	summaryID := maxID + 1
	turns = append(turns, Turn{
		ID: summaryID, SessionID: sessionID, Seq: seq, Role: "checkpoint", Content: summary,
		TokenEstimate: estimate(summary), IncludedInContext: true, CheckpointIndex: nextIndex,
		CreatedAt: now,
	})
	ckpts = append(ckpts, compactionRecord{
		CheckpointIndex: nextIndex, SummaryTurnID: summaryID, CompactedTurns: archived,
		Reason: reason, TailStartTurnID: tail.TailStartTurnID, CreatedAt: now.Format(time.RFC3339Nano),
	})
	if err := s.saveSessionTurns(sessionID, turns); err != nil {
		return CheckpointResult{}, err
	}
	if err := s.saveSessionCheckpoints(sessionID, ckpts); err != nil {
		return CheckpointResult{}, err
	}
	_ = s.touchSessionLocked(sessionID)
	return CheckpointResult{
		Index: nextIndex, SummaryTurnID: summaryID, Reason: reason,
		CompactedTurns: archived, TailStartTurnID: tail.TailStartTurnID,
	}, nil
}

func maxSeqID(turns []Turn) (seq int, maxID int64) {
	for _, t := range turns {
		if t.Seq > seq {
			seq = t.Seq
		}
		if t.ID > maxID {
			maxID = t.ID
		}
	}
	return seq, maxID
}

func (s *Store) LatestCheckpoint(ctx context.Context, sessionID string) (Checkpoint, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	ckpts, err := s.loadSessionCheckpoints(sessionID)
	if err != nil {
		return Checkpoint{}, err
	}
	if len(ckpts) == 0 {
		return Checkpoint{}, errors.New("no checkpoint")
	}
	last := ckpts[len(ckpts)-1]
	for i := range ckpts {
		if ckpts[i].CheckpointIndex > last.CheckpointIndex {
			last = ckpts[i]
		}
	}
	turns, _ := s.loadSessionTurns(sessionID)
	var summary string
	for _, t := range turns {
		if t.ID == last.SummaryTurnID {
			summary = t.Content
			break
		}
	}
	created, _ := time.Parse(time.RFC3339Nano, last.CreatedAt)
	return Checkpoint{
		Index: last.CheckpointIndex, SummaryTurnID: last.SummaryTurnID, Summary: summary,
		Reason: last.Reason, CompactedTurns: last.CompactedTurns, TailStartTurnID: last.TailStartTurnID,
		CreatedAt: created,
	}, nil
}

func (s *Store) Checkpoints(ctx context.Context, sessionID string) ([]Checkpoint, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	ckpts, err := s.loadSessionCheckpoints(sessionID)
	if err != nil {
		return nil, err
	}
	sort.Slice(ckpts, func(i, j int) bool {
		return ckpts[i].CheckpointIndex < ckpts[j].CheckpointIndex
	})
	var out []Checkpoint
	for _, c := range ckpts {
		created, _ := time.Parse(time.RFC3339Nano, c.CreatedAt)
		out = append(out, Checkpoint{
			Index: c.CheckpointIndex, SummaryTurnID: c.SummaryTurnID,
			Reason: c.Reason, CompactedTurns: c.CompactedTurns, TailStartTurnID: c.TailStartTurnID,
			CreatedAt: created,
		})
	}
	return out, nil
}

func (s *Store) Usage(ctx context.Context, sessionID, provider, model string, contextWindow int) (Usage, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	turns, err := s.loadSessionTurns(sessionID)
	if err != nil {
		return Usage{SessionID: sessionID, ContextWindow: contextWindow, Provider: provider, Model: model}, err
	}
	used := 0
	active := 0
	for _, t := range turns {
		if t.IncludedInContext {
			used += t.TokenEstimate
			active++
		}
	}
	ckpts, _ := s.loadSessionCheckpoints(sessionID)
	compacted := 0
	var last string
	for _, c := range ckpts {
		compacted += c.CompactedTurns
		if c.CreatedAt > last {
			last = c.CreatedAt
		}
	}
	percent := 0
	if contextWindow > 0 {
		percent = (used * 100) / contextWindow
	}
	return Usage{
		SessionID: sessionID, UsedTokens: used, ContextWindow: contextWindow, Percent: percent,
		Provider: provider, Model: model, CompactedTurns: compacted, ActiveTurns: active, LastCompactedAt: last,
	}, nil
}

func (s *Store) SnapshotUsage(ctx context.Context, usage Usage) error {
	_ = ctx
	rec := struct {
		Usage
		CreatedAt string `json:"created_at"`
	}{Usage: usage, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	return appendJSONL(s.paths.sessionUsage(usage.SessionID), rec)
}

// RewriteRollout atomically rewrites a session rollout file (used by compaction).
func (s *Store) RewriteRollout(sessionID string, lines []json.RawMessage) error {
	path := s.paths.rolloutFile(sessionID)
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
	for _, line := range lines {
		if _, err := f.Write(append(line, '\n')); err != nil {
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
		return err
	}
	return os.Rename(tmp, path)
}
