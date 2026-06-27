package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

func TestArtifactFingerprintHTMLCSS(t *testing.T) {
	dir := t.TempDir()
	htmlPath := filepath.Join(dir, "index.html")
	cssPath := filepath.Join(dir, "css", "style.css")
	_ = os.MkdirAll(filepath.Dir(cssPath), 0o755)
	_ = os.WriteFile(htmlPath, []byte(`<div class="site-header btn--accent" id="nav-toggle"></div>`), 0o644)
	_ = os.WriteFile(cssPath, []byte(`.region-header { color: red; }\n.btn-primary { }`), 0o644)

	htmlFP := fingerprintFile(htmlPath)
	if !strings.Contains(htmlFP, "site-header") || !strings.Contains(htmlFP, "nav-toggle") {
		t.Fatalf("html fingerprint missing selectors: %q", htmlFP)
	}
	cssFP := fingerprintFile(cssPath)
	if !strings.Contains(cssFP, "region-header") {
		t.Fatalf("css fingerprint missing classes: %q", cssFP)
	}
}

func TestEnrichExecOutputWithLineCount(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "index.html")
	_ = os.WriteFile(p, []byte("a\nb\nc\n"), 0o644)
	out := enrichToolResultWithArtifactFingerprint("exec", "", p+": 3 lines")
	if !strings.Contains(out, "[artifact]") {
		t.Fatalf("expected artifact block in output: %q", out)
	}
}

func TestJSONStoreRolloutDir(t *testing.T) {
	store, err := chatstore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if store.RolloutDir() == "" {
		t.Fatal("expected rollout dir")
	}
}

func TestCrossFileClassOverlapScore(t *testing.T) {
	dir := t.TempDir()
	html := writeAndFingerprint(dir, "index.html", `<div class="site-header btn--accent"></div>`)
	css := writeAndFingerprint(dir, "style.css", `.region-header {}\n.site-header {}`)
	score := crossFileClassOverlap(html, css)
	if score <= 0 {
		t.Fatalf("expected some overlap score, got %f", score)
	}
}

func writeAndFingerprint(dir, name, body string) string {
	p := filepath.Join(dir, name)
	_ = os.WriteFile(p, []byte(body), 0o644)
	return fingerprintFile(p)
}

func crossFileClassOverlap(htmlFP, cssFP string) float64 {
	htmlClasses := parseFingerprintList(htmlFP, "classes=")
	cssSelectors := parseFingerprintList(cssFP, "selectors=")
	if len(htmlClasses) == 0 || len(cssSelectors) == 0 {
		return 0
	}
	set := map[string]bool{}
	for _, c := range htmlClasses {
		set[c] = true
	}
	matches := 0
	for _, s := range cssSelectors {
		if set[s] {
			matches++
		}
	}
	return float64(matches) / float64(len(cssSelectors))
}

func parseFingerprintList(fp, key string) []string {
	i := strings.Index(fp, key)
	if i < 0 {
		return nil
	}
	rest := fp[i+len(key):]
	if j := strings.Index(rest, " "); j >= 0 {
		rest = rest[:j]
	}
	if rest == "" {
		return nil
	}
	return strings.Split(rest, ",")
}
