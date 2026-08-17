// Package screen checks email-derived text for prompt-injection attempts.
//
// Every string that originates from a mailbox (subject, body, sender name,
// attachment filename) must pass through a Screener before it is placed in
// an MCP tool result. Screening is layered: cheap heuristics run first; a
// local classifier model (Llama Prompt Guard 2 via ONNX Runtime/CoreML) is
// added behind the same interface.
package screen

import (
	"context"
	"strings"
)

// Verdict is the result of screening one piece of text.
type Verdict struct {
	Flagged bool     `json:"flagged"`
	Reasons []string `json:"reasons,omitempty"`
}

// Merge combines another verdict into v.
func (v *Verdict) Merge(other Verdict) {
	v.Flagged = v.Flagged || other.Flagged
	v.Reasons = append(v.Reasons, other.Reasons...)
}

// Screener inspects untrusted text.
type Screener interface {
	Screen(ctx context.Context, text string) (Verdict, error)
	Name() string
}

// Chain runs several screeners and merges their verdicts.
type Chain []Screener

func (c Chain) Name() string { return "chain" }

func (c Chain) Screen(ctx context.Context, text string) (Verdict, error) {
	var v Verdict
	for _, s := range c {
		sv, err := s.Screen(ctx, text)
		if err != nil {
			return v, err
		}
		v.Merge(sv)
	}
	return v, nil
}

// invisibleRunes are characters commonly used to hide or reorder injected
// instructions (zero-width and bidi-control characters).
var invisibleRunes = map[rune]bool{
	'\u200b': true, // zero width space
	'\u200c': true, // zero width non-joiner
	'\u200d': true, // zero width joiner
	'\u2060': true, // word joiner
	'\ufeff': true, // zero width no-break space / BOM
	'\u202a': true, // left-to-right embedding
	'\u202b': true, // right-to-left embedding
	'\u202c': true, // pop directional formatting
	'\u202d': true, // left-to-right override
	'\u202e': true, // right-to-left override
	'\u2066': true, // left-to-right isolate
	'\u2067': true, // right-to-left isolate
	'\u2068': true, // first strong isolate
	'\u2069': true, // pop directional isolate
}

// StripInvisible removes zero-width and bidi-control characters. It is
// applied to all mailbox-derived text regardless of the screening verdict.
func StripInvisible(s string) string {
	return strings.Map(func(r rune) rune {
		if invisibleRunes[r] {
			return -1
		}
		return r
	}, s)
}

func containsInvisible(s string) bool {
	for _, r := range s {
		if invisibleRunes[r] {
			return true
		}
	}
	return false
}
