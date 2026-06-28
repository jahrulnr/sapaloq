package bridge

// Transcript patch modes. Empty Mode is treated as snapshot (legacy).
const (
	TranscriptPatchSnapshot = "snapshot"
	TranscriptPatchDelta    = "delta"
)

// TranscriptPatchOp is one incremental mutation for mode=delta patches.
type TranscriptPatchOp struct {
	Op      string          `json:"op"` // upsert | append_text | remove
	Entry   TranscriptEntry `json:"entry,omitempty"`
	EntryID string          `json:"entry_id,omitempty"`
	Delta   string          `json:"delta,omitempty"`
}

// SnapshotPatch builds a full-replace transcript patch (default / legacy).
func SnapshotPatch(sessionID, generationID string, entries []TranscriptEntry, finished bool) TranscriptPatch {
	return TranscriptPatch{
		Mode:         TranscriptPatchSnapshot,
		SessionID:    sessionID,
		GenerationID: generationID,
		Entries:      entries,
		Finished:     finished,
	}
}

// DeltaPatch builds an incremental transcript patch with one or more ops.
func DeltaPatch(sessionID, generationID string, ops []TranscriptPatchOp) TranscriptPatch {
	return TranscriptPatch{
		Mode:         TranscriptPatchDelta,
		SessionID:    sessionID,
		GenerationID: generationID,
		Ops:          ops,
	}
}
