package chat

import (
	"context"
	"strings"
)

type PromptSlice struct {
	ID           string `json:"id"`
	Role         string `json:"role"`
	Conditions   string `json:"conditions"`
	TemplatePath string `json:"template_path"`
	TokenBudget  int    `json:"token_budget"`
}

func (s *Store) UpsertPromptSlice(ctx context.Context, p PromptSlice) error {
	_ = ctx
	id := strings.TrimSpace(p.ID)
	path := strings.TrimSpace(p.TemplatePath)
	if id == "" || path == "" {
		return nil
	}
	cond := strings.TrimSpace(p.Conditions)
	if cond == "" {
		cond = "{}"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.promptSlices {
		if s.promptSlices[i].ID == id {
			s.promptSlices[i] = PromptSlice{
				ID: id, Role: strings.TrimSpace(p.Role), Conditions: cond,
				TemplatePath: path, TokenBudget: p.TokenBudget,
			}
			return s.savePromptSlices()
		}
	}
	s.promptSlices = append(s.promptSlices, PromptSlice{
		ID: id, Role: strings.TrimSpace(p.Role), Conditions: cond,
		TemplatePath: path, TokenBudget: p.TokenBudget,
	})
	return s.savePromptSlices()
}

func (s *Store) PromptSlicesForRole(ctx context.Context, role string) ([]PromptSlice, error) {
	_ = ctx
	r := strings.TrimSpace(role)
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []PromptSlice
	for _, p := range s.promptSlices {
		if r == "" || p.Role == r {
			out = append(out, p)
		}
	}
	return out, nil
}

type SkillIndexEntry struct {
	ID        string `json:"id"`
	Triggers  string `json:"triggers"`
	Path      string `json:"path"`
	MaxTokens int    `json:"max_tokens"`
	Priority  int    `json:"priority"`
}

func (s *Store) UpsertSkillIndex(ctx context.Context, e SkillIndexEntry) error {
	_ = ctx
	id := strings.TrimSpace(e.ID)
	if id == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.skillsIndex {
		if s.skillsIndex[i].ID == id {
			s.skillsIndex[i] = SkillIndexEntry{
				ID: id, Triggers: jsonOrEmptyArray(e.Triggers), Path: strings.TrimSpace(e.Path),
				MaxTokens: e.MaxTokens, Priority: e.Priority,
			}
			return s.saveSkillsIndex()
		}
	}
	s.skillsIndex = append(s.skillsIndex, SkillIndexEntry{
		ID: id, Triggers: jsonOrEmptyArray(e.Triggers), Path: strings.TrimSpace(e.Path),
		MaxTokens: e.MaxTokens, Priority: e.Priority,
	})
	return s.saveSkillsIndex()
}

func (s *Store) SkillIndexEntries(ctx context.Context) ([]SkillIndexEntry, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]SkillIndexEntry(nil), s.skillsIndex...)
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].Priority > out[i].Priority || (out[j].Priority == out[i].Priority && out[j].ID < out[i].ID) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out, nil
}
