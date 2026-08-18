package tools

import (
	"fmt"
	"strings"
	"unicode/utf8"

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
		// Preserve the extension when truncating; cut on a rune boundary —
		// a byte cut can produce invalid UTF-8 that APFS refuses to create.
		ext := ""
		if i := strings.LastIndexByte(name, '.'); i > 0 && len(name)-i <= 20 {
			ext = name[i:]
		}
		cut := maxFilenameLen - len(ext)
		base := name
		for len(base) > cut {
			_, size := utf8.DecodeLastRuneInString(base)
			base = base[:len(base)-size]
		}
		name = base + ext
	}
	if name == "" {
		return fallback
	}
	return name
}

// withHashSuffix inserts a short content-hash before the extension:
// "Rechnung.pdf" + sha → "Rechnung-83afa79c.pdf".
func withHashSuffix(name, sha256hex string) string {
	if len(sha256hex) < 8 {
		return name
	}
	suffix := "-" + sha256hex[:8]
	if i := strings.LastIndexByte(name, '.'); i > 0 {
		return name[:i] + suffix + name[i:]
	}
	return name + suffix
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
