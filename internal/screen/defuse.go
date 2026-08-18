package screen

import "strings"

// Defuse rewrites untrusted text so that instructions inside it are much
// less likely to be followed by a model that reads it, while staying
// human- and machine-readable ("spotlighting via datamarking"):
//
//   - invisible/bidi characters are stripped;
//   - every run of whitespace is replaced with the marker character, so
//     injected phrases no longer look like natural prose;
//   - unbroken runs longer than 2*defuseRunLen runes (CJK and similar
//     scripts have no spaces) get the marker inserted every defuseRunLen
//     runes;
//   - every line is prefixed with a quarantine marker.
//
// It deliberately does NOT redact: this is the read path for content that
// screening blocked, when the operator still needs to see what is inside.
const (
	defuseMarker  = "ˆ" // U+02C6, rare in real mail; pre-existing ones are folded to '^'
	defuseLine    = "❯ "
	defuseRunLen  = 12
	defuseMaxRun  = 2 * defuseRunLen
	whitespaceSet = " \t\r\f\v "
)

func Defuse(text string) string {
	text = StripInvisible(text)
	text = strings.ReplaceAll(text, defuseMarker, "^")

	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = defuseLine + datamark(line)
	}
	return strings.Join(lines, "\n")
}

func datamark(line string) string {
	var b strings.Builder
	run := 0
	inSpace := false
	for _, r := range line {
		if strings.ContainsRune(whitespaceSet, r) {
			if !inSpace {
				b.WriteString(defuseMarker)
				inSpace = true
				run = 0
			}
			continue
		}
		inSpace = false
		if run >= defuseMaxRun {
			b.WriteString(defuseMarker)
			run = 0
		}
		b.WriteRune(r)
		run++
	}
	return b.String()
}
