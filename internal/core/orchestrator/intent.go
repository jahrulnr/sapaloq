package orchestrator

import "strings"

// Intent is the heuristic classification of a user message produced by the
// no-LLM intent-router in the ingress pipeline (Context-SOP Fase 0). It feeds
// the prefetch step: the Name selects a prefetch rule, the Mode selects the
// memory namespace, and Confidence gates how aggressively the orchestrator may
// explore the filesystem (anti-deep-check).
type Intent struct {
	// Name is the classified intent: "catat", "notify", "task", "settings",
	// "status", or "chat" (the default fallback).
	Name string
	// Mode is the boundary/namespace the prompt belongs to: "personal" (default),
	// "work", or "hobby".
	Mode string
	// Confidence in [0,1]. Keyword hits raise it; an unclassified message lands
	// at the low "chat" baseline so the pipeline stays conservative.
	Confidence float64
}

// intentKeyword maps a lowercase trigger token to an intent name. The router is
// deliberately small and deterministic (no LLM): it favors precision on the
// common companion intents and falls back to "chat" otherwise. Indonesian and
// English triggers are both included since the product is bilingual.
var intentKeywords = map[string]string{
	// catat / note-taking
	"catat":    "catat",
	"note":     "catat",
	"notes":    "catat",
	"ingat":    "catat",
	"remind":   "catat",
	"reminder": "catat",
	"simpan":   "catat",
	"jurnal":   "catat",
	"journal":  "catat",
	// notify
	"notify":       "notify",
	"notifikasi":   "notify",
	"notification": "notify",
	"ingatkan":     "notify",
	"alert":        "notify",
	// task / automation
	"task":     "task",
	"tugas":    "task",
	"otomatis": "task",
	"automate": "task",
	"jalankan": "task",
	"run":      "task",
	"buatkan":  "task",
	"kerjakan": "task",
	// settings
	"settings": "settings",
	"setting":  "settings",
	"setelan":  "settings",
	"konfigur": "settings",
	"config":   "settings",
	// status
	"status":   "status",
	"progress": "status",
	"sampai":   "status",
}

// modeKeywords maps a lowercase token to a boundary mode. Absent any hit, the
// router defaults to "personal".
var modeKeywords = map[string]string{
	"kerja":    "work",
	"work":     "work",
	"kantor":   "work",
	"office":   "work",
	"klien":    "work",
	"client":   "work",
	"hobi":     "hobby",
	"hobby":    "hobby",
	"game":     "hobby",
	"main":     "hobby",
	"pribadi":  "personal",
	"personal": "personal",
	"keluarga": "personal",
}

// classifyIntent runs the heuristic intent-router over a user message. It is
// pure (no I/O) so it can be unit-tested and run in <100µs on the hot path.
//
// Confidence model:
//   - a slash command (handled elsewhere) is not seen here;
//   - an explicit intent keyword → 0.8 (single hit) or 0.9 (multiple hits);
//   - no intent keyword → "chat" at 0.3 so the pipeline treats it as low
//     confidence and does not forbid exploration.
func classifyIntent(message string) Intent {
	msg := strings.ToLower(strings.TrimSpace(message))
	in := Intent{Name: "chat", Mode: "personal", Confidence: 0.3}
	if msg == "" {
		// Nothing to act on: lowest confidence, plain chat.
		in.Confidence = 0.2
		return in
	}

	fields := strings.FieldsFunc(msg, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	})

	intentHits := 0
	for _, f := range fields {
		if name, ok := intentKeywords[f]; ok {
			if intentHits == 0 {
				in.Name = name
			}
			intentHits++
		}
		if mode, ok := modeKeywords[f]; ok {
			in.Mode = mode
		}
	}

	switch {
	case intentHits >= 2:
		in.Confidence = 0.9
	case intentHits == 1:
		in.Confidence = 0.8
	default:
		// Unclassified → conservative chat baseline (keeps in.Name == "chat").
		in.Confidence = 0.3
	}
	return in
}
