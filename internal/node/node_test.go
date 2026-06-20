package node

import (
	"context"
	"errors"
	"testing"
)

func TestFakeTransportStreamsScript(t *testing.T) {
	ft := &FakeTransport{Script: []Progress{
		{Kind: "progress", Text: "step 1"},
		{Kind: "result", Text: "done", Done: true},
	}}
	ch, err := ft.Spawn(context.Background(), SpawnEnvelope{SubAgentID: "t1", Role: "scribe", Task: "x"})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	var got []Progress
	for p := range ch {
		got = append(got, p)
	}
	if len(got) != 2 || !got[1].Done {
		t.Fatalf("unexpected stream: %+v", got)
	}
	if env, ok := ft.LastEnvelope(); !ok || env.SubAgentID != "t1" {
		t.Fatalf("envelope not recorded: %+v", env)
	}
}

func TestFakeTransportSpawnError(t *testing.T) {
	ft := &FakeTransport{SpawnErr: errors.New("connect refused")}
	if _, err := ft.Spawn(context.Background(), SpawnEnvelope{}); err == nil {
		t.Fatalf("expected spawn error for fallback path")
	}
}

func TestEnforceRemoteInvariants(t *testing.T) {
	env := SpawnEnvelope{
		SubAgentID: "t1",
		ContextPacket: map[string]any{
			"taskId":     "t1",
			"mode":       "work",
			"memory_bus": "LEAK",
			"bus":        "LEAK",
		},
	}
	out := EnforceRemoteInvariants(env)
	if !out.NoMemoryBus {
		t.Fatalf("NoMemoryBus must be forced true")
	}
	if _, leaked := out.ContextPacket["memory_bus"]; leaked {
		t.Fatalf("memory_bus must be stripped")
	}
	if _, leaked := out.ContextPacket["bus"]; leaked {
		t.Fatalf("bus must be stripped")
	}
	if out.ContextPacket["mode"] != "work" {
		t.Fatalf("safe context must be preserved")
	}
}

func TestFakeTransportControlAndClose(t *testing.T) {
	ft := &FakeTransport{}
	_ = ft.Control(context.Background(), "t1", "stop")
	_ = ft.Close()
	if len(ft.Controls) != 1 || ft.Controls[0] != "t1:stop" {
		t.Fatalf("control not recorded: %+v", ft.Controls)
	}
	if ft.ClosedCnt != 1 {
		t.Fatalf("close not recorded")
	}
}
