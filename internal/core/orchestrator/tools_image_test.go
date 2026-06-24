package orchestrator

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/parse"
)

// 1x1 transparent PNG.
const onePxPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAAC0lEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="

func writePNG(t *testing.T, dir, name string) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(onePxPNGBase64)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReadImageReturnsInlineMarkdown(t *testing.T) {
	p := writePNG(t, t.TempDir(), "shot.png")
	got := toolReadImage(toolArgs{Path: p})
	if !strings.HasPrefix(got, "![shot.png](data:image/png;base64,") {
		t.Fatalf("expected inline png markdown, got %q", truncate(got))
	}
	// Must be re-ingestible by the orchestrator's image extractor - this is the
	// whole mechanism that turns the result into real vision input.
	if !inlineImageRE.MatchString(got) {
		t.Fatalf("read_image output not matched by inlineImageRE: %q", truncate(got))
	}
	msgs, images := extractImages([]bridge.Message{{Role: "user", Content: got}})
	if len(images) != 1 || images[0].MimeType != "image/png" {
		t.Fatalf("expected 1 png vision image, got %+v", images)
	}
	if strings.Contains(msgs[0].Content, "base64") {
		t.Fatalf("image markdown should be replaced by a placeholder, got %q", msgs[0].Content)
	}
}

// A read_image result is fed back under the "tool" role. extractImages must
// still treat that turn as the latest vision source - otherwise the inline
// image would be downgraded to a text placeholder and the model would go blind
// to it. This guards the tool-role addition against silently breaking vision.
func TestExtractImagesTreatsToolRoleAsVisionSource(t *testing.T) {
	got := toolReadImage(toolArgs{Path: writePNG(t, t.TempDir(), "shot.png")})
	msgs, images := extractImages([]bridge.Message{
		{Role: "user", Content: "look at this"},
		{Role: "assistant", Content: "reading the image"},
		{Role: "tool", Content: got},
	})
	if len(images) != 1 || images[0].MimeType != "image/png" {
		t.Fatalf("expected 1 png vision image from tool turn, got %+v", images)
	}
	if strings.Contains(msgs[2].Content, "base64") {
		t.Fatalf("tool image markdown should be replaced by a placeholder, got %q", msgs[2].Content)
	}
}

func TestReadImageJPEGByExtension(t *testing.T) {
	// Bytes are PNG, but the .jpg extension drives the mime mapping first.
	dir := t.TempDir()
	p := writePNG(t, dir, "pic.jpg")
	got := toolReadImage(toolArgs{Path: p})
	if !strings.HasPrefix(got, "![pic.jpg](data:image/jpeg;base64,") {
		t.Fatalf("expected jpeg mime by extension, got %q", truncate(got))
	}
}

func TestReadImageMissingPath(t *testing.T) {
	if got := toolReadImage(toolArgs{}); !strings.HasPrefix(got, "Error:") {
		t.Fatalf("expected error for empty path, got %q", got)
	}
}

func TestReadImageRejectsNonImage(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(p, []byte("just some text, definitely not an image\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := toolReadImage(toolArgs{Path: p})
	if !strings.HasPrefix(got, "Error:") || !strings.Contains(got, "image") {
		t.Fatalf("expected non-image refusal, got %q", got)
	}
}

func TestReadImageRejectsOversize(t *testing.T) {
	p := writePNG(t, t.TempDir(), "big.png")
	got := toolReadImage(toolArgs{Path: p, MaxBytes: 1})
	if !strings.HasPrefix(got, "Error:") || !strings.Contains(got, "limit") {
		t.Fatalf("expected oversize refusal, got %q", got)
	}
}

func TestReadImageDispatchedSharedInAllModes(t *testing.T) {
	p := writePNG(t, t.TempDir(), "d.png")
	text, handled := runSharedTool(context.Background(), parse.ToolCall{Name: "read_image", Arguments: []byte(`{"path":"` + p + `"}`)})
	if !handled || !strings.HasPrefix(text, "![d.png](data:image/png;base64,") {
		t.Fatalf("shared dispatch failed for read_image: handled=%v text=%q", handled, truncate(text))
	}
	for mode, profile := range map[string][]string{"ask": askTools, "plan": planTools, "agent": agentTools} {
		if !containsTool(profile, "read_image") {
			t.Fatalf("%s profile missing read_image: %v", mode, profile)
		}
	}
}

func truncate(s string) string {
	if len(s) > 80 {
		return s[:80] + "…"
	}
	return s
}
