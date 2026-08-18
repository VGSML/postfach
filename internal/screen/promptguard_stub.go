//go:build !promptguard

package screen

import (
	"errors"
	"os"
)

// NewPromptGuardFromEnv in the default build (no `promptguard` tag) only
// verifies the binary is not misconfigured: enabling the classifier requires
// the tagged build with its native dependencies (see Makefile).
func NewPromptGuardFromEnv() (Screener, error) {
	if os.Getenv("POSTFACH_PG2_MODEL") != "" {
		return nil, errors.New("POSTFACH_PG2_MODEL is set, but this binary was built without Prompt Guard support; " +
			"rebuild with `make build-guard` (go build -tags promptguard)")
	}
	return nil, nil
}
