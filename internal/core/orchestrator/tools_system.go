package orchestrator

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const imageReadMaxBytes = 10 * 1024 * 1024 // 10 MiB cap for read_image payloads.

// imageMimeByExt maps common image extensions to their MIME types. This is the
// accepted image set across the bridges (png/jpg/jpeg/gif/webp).
var imageMimeByExt = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
}

// toolReadImage reads an image file from any host path and returns it as an
// inline Markdown data-URI image: ![name](data:<mime>;base64,<payload>).
//
// This is how a local image becomes real vision input: the orchestrator's
// extractImages() scans messages for exactly this markdown and attaches the
// decoded image to the next inference turn's bridge.Request.Images (the same
// channel widget attachments use). Available in every mode via the shared
// dispatcher. Unlike read_file it does NOT reject binary content (images are
// binary by nature).
func toolReadImage(args toolArgs) string {
	path := strings.TrimSpace(args.Path)
	if path == "" {
		return "Error: path is required."
	}
	path = expandHome(path)
	if !filepath.IsAbs(path) {
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		return "Error: " + err.Error()
	}
	if info.IsDir() {
		return fmt.Sprintf("Error: %q is a directory, not an image file.", path)
	}
	limit := imageReadMaxBytes
	if args.MaxBytes > 0 && args.MaxBytes < limit {
		limit = args.MaxBytes
	}
	if info.Size() > int64(limit) {
		return fmt.Sprintf("Error: %q is %d bytes, larger than the %d-byte read_image limit.", path, info.Size(), limit)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "Error: " + err.Error()
	}
	mime := imageMimeFor(path, data)
	if mime == "" {
		return fmt.Sprintf("Error: %q does not look like a supported image (png, jpeg, gif, webp). Use read_file/exec for non-image files.", path)
	}
	encoded := base64.StdEncoding.EncodeToString(data)
	name := filepath.Base(path)
	return fmt.Sprintf("![%s](data:%s;base64,%s)", name, mime, encoded)
}

// imageMimeFor resolves a supported image MIME type from the file extension,
// falling back to content sniffing. Returns "" if it is not a supported image.
func imageMimeFor(path string, data []byte) string {
	if mime, ok := imageMimeByExt[strings.ToLower(filepath.Ext(path))]; ok {
		return mime
	}
	sniffed := http.DetectContentType(data)
	switch sniffed {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return sniffed
	}
	return ""
}

// expandHome expands a leading ~ to the user's home directory.
func expandHome(path string) string {
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return path
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}
