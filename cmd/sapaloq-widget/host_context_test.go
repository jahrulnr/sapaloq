package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildHostContextJSON(t *testing.T) {
	raw := buildHostContextJSON("/home/me/proj", []ComposeAttachment{
		{Name: "a.go", Path: "/home/me/proj/a.go"},
	})
	if len(raw) == 0 {
		t.Fatal("expected host context json")
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["version"] != float64(1) {
		t.Fatalf("version: %v", m["version"])
	}
	s := string(raw)
	if !strings.Contains(s, "/home/me/proj") {
		t.Fatalf("missing workspace: %s", s)
	}
}

func TestBuildHostContextJSONEmpty(t *testing.T) {
	if buildHostContextJSON("", nil) != nil {
		t.Fatal("empty inputs should yield nil raw")
	}
}
