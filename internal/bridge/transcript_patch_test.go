package bridge

import "testing"

func TestSnapshotPatchDefaultsMode(t *testing.T) {
	p := SnapshotPatch("s1", "42", []TranscriptEntry{{ID: "u1", Kind: TranscriptUser, Text: "hi"}}, true)
	if p.Mode != TranscriptPatchSnapshot {
		t.Fatalf("mode = %q", p.Mode)
	}
	if len(p.Entries) != 1 || p.Entries[0].Text != "hi" || !p.Finished {
		t.Fatalf("snapshot = %+v", p)
	}
}

func TestDeltaPatchCarriesOps(t *testing.T) {
	ops := []TranscriptPatchOp{{Op: "append_text", EntryID: "42-pending-text", Delta: "hi"}}
	p := DeltaPatch("s1", "42", ops)
	if p.Mode != TranscriptPatchDelta {
		t.Fatalf("mode = %q", p.Mode)
	}
	if len(p.Ops) != 1 || p.Ops[0].Delta != "hi" {
		t.Fatalf("ops = %+v", p.Ops)
	}
}
