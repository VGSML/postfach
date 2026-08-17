package tools

import (
	"fmt"
	"strings"

	"github.com/hugr-lab/postfach/internal/screen"
)

const maxFilenameLen = 150

// SanitizeFilename turns an untrusted attachment filename into a name that
// is safe to create inside the attachments directory: no path separators,
// no control or invisible characters, no leading dots, bounded length.
// fallback is used when nothing usable remains.
func SanitizeFilename(name, fallback string) string {
	name = screen.StripInvisible(name)

	// Keep only the last path element for both separator conventions.
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}

	var b strings.Builder
	for _, r := range name {
		switch {
		case r < 0x20 || r == 0x7f: // control chars
		case r == ':', r == '*', r == '?', r == '"', r == '<', r == '>', r == '|':
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	name = strings.Trim(b.String(), " .")

	if len(name) > maxFilenameLen {
		// Preserve the extension when truncating.
		ext := ""
		if i := strings.LastIndexByte(name, '.'); i > 0 && len(name)-i <= 20 {
			ext = name[i:]
		}
		name = name[:maxFilenameLen-len(ext)] + ext
	}
	if name == "" {
		return fallback
	}
	return name
}

// uniqueName appends -1, -2, ... before the extension until exists(name)
// reports false.
func uniqueName(name string, exists func(string) bool) string {
	if !exists(name) {
		return name
	}
	base, ext := name, ""
	if i := strings.LastIndexByte(name, '.'); i > 0 {
		base, ext = name[:i], name[i:]
	}
	for n := 1; ; n++ {
		candidate := fmt.Sprintf("%s-%d%s", base, n, ext)
		if !exists(candidate) {
			return candidate
		}
	}
}
