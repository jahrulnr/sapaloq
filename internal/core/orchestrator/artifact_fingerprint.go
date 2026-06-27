package orchestrator

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const maxFingerprintChars = 2048

var (
	htmlClassRE = regexp.MustCompile(`class\s*=\s*["']([^"']+)["']`)
	htmlIDRE    = regexp.MustCompile(`\bid\s*=\s*["']([^"']+)["']`)
	cssClassRE  = regexp.MustCompile(`\.([a-zA-Z_][\w-]*)`)
	execOutPath = regexp.MustCompile(`(?m)(?:^|\s)([/~\w.-]+\.(?:html?|css|js|tsx?|jsx?|md|json|twig|php|py|go))\s*:\s*(\d+)\s*lines?`)
)

// enrichToolResultWithArtifactFingerprint appends bounded file metadata after
// write/exec results so the model retains cross-file continuity without full
// content replay.
func enrichToolResultWithArtifactFingerprint(toolName, command, result string) string {
	result = strings.TrimSpace(result)
	if result == "" || strings.HasPrefix(result, "Error:") {
		return result
	}
	switch toolName {
	case "exec":
		if fp := fingerprintFromExecOutput(command, result); fp != "" {
			return result + "\n" + fp
		}
		if paths := extractWritePathsFromCommand(command); len(paths) > 0 {
			return result + "\n" + fingerprintPaths(paths)
		}
	case "write_file", "create_file", "edit_file":
		// path may be embedded in result: "Wrote N bytes to path"
		if path := extractPathFromWriteResult(result); path != "" {
			if fp := fingerprintFile(path); fp != "" {
				return result + "\n" + fp
			}
		}
	}
	return result
}

func fingerprintFromExecOutput(command, output string) string {
	matches := execOutPath.FindAllStringSubmatch(output, -1)
	if len(matches) == 0 {
		return ""
	}
	var paths []string
	for _, m := range matches {
		if len(m) > 1 {
			paths = append(paths, m[1])
		}
	}
	return fingerprintPaths(paths)
}

func extractWritePathsFromCommand(command string) []string {
	var paths []string
	for _, marker := range []string{"> ", ">> "} {
		idx := 0
		for {
			at := strings.Index(command[idx:], marker)
			if at < 0 {
				break
			}
			start := idx + at + len(marker)
			rest := strings.TrimSpace(command[start:])
			if rest == "" {
				break
			}
			end := strings.IndexAny(rest, " \t\n;&|")
			p := rest
			if end > 0 {
				p = rest[:end]
			}
			p = strings.Trim(p, `"'`)
			if p != "" {
				paths = append(paths, p)
			}
			idx = start + len(p)
		}
	}
	return paths
}

func extractPathFromWriteResult(result string) string {
	const prefix = "Wrote "
	const mid = " bytes to "
	if strings.HasPrefix(result, prefix) {
		if i := strings.Index(result, mid); i > 0 {
			return strings.TrimSpace(result[i+len(mid):])
		}
	}
	return ""
}

func fingerprintPaths(paths []string) string {
	var parts []string
	for _, p := range paths {
		if fp := fingerprintFile(p); fp != "" {
			parts = append(parts, fp)
		}
	}
	out := strings.Join(parts, "\n")
	if len(out) > maxFingerprintChars {
		out = out[:maxFingerprintChars] + "…"
	}
	return out
}

func fingerprintFile(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	abs := path
	if !filepath.IsAbs(path) {
		if cwd, err := os.Getwd(); err == nil {
			abs = filepath.Join(cwd, path)
		}
	}
	info, err := os.Stat(abs)
	if err != nil || info.IsDir() {
		return ""
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return fmt.Sprintf("[artifact] path=%s bytes=%d (unreadable)", path, info.Size())
	}
	lines := countLines(b)
	ext := strings.ToLower(filepath.Ext(path))
	fp := fmt.Sprintf("[artifact] path=%s bytes=%d lines=%d", path, len(b), lines)
	switch ext {
	case ".html", ".htm":
		fp += " classes=" + joinLimited(extractHTMLClasses(string(b)), 24)
		fp += " ids=" + joinLimited(extractHTMLIDs(string(b)), 16)
	case ".css":
		fp += " selectors=" + joinLimited(extractCSSClasses(string(b)), 32)
	}
	if len(fp) > maxFingerprintChars {
		fp = fp[:maxFingerprintChars] + "…"
	}
	return fp
}

func countLines(b []byte) int {
	if len(b) == 0 {
		return 0
	}
	return bytes.Count(b, []byte{'\n'}) + 1
}

func extractHTMLClasses(s string) []string {
	return uniqueMatches(htmlClassRE, s, 1)
}

func extractHTMLIDs(s string) []string {
	return uniqueMatches(htmlIDRE, s, 1)
}

func extractCSSClasses(s string) []string {
	return uniqueMatches(cssClassRE, s, 1)
}

func uniqueMatches(re *regexp.Regexp, s string, group int) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range re.FindAllStringSubmatch(s, -1) {
		if len(m) <= group {
			continue
		}
		v := strings.TrimSpace(m[group])
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func joinLimited(items []string, limit int) string {
	if len(items) > limit {
		items = items[:limit]
	}
	return strings.Join(items, ",")
}
