// Package workspace resolves the host-side directory the user means by
// "this project" into an absolute, Docker-bind-safe path. It is the single
// seam shared by `toolbox shell` and `toolbox stop` for picking the
// workspace; both commands derive their per-workspace container identity
// from this resolved path.
package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Resolve returns the cleaned, absolute path of the current working
// directory after validating it is safe to use as a Docker bind source.
// Errors surface verbatim from os.Getwd / filepath.Abs so the caller can
// distinguish missing CWD from a malformed path.
func Resolve() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current directory: %w", err)
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", fmt.Errorf("failed to resolve absolute path: %w", err)
	}
	clean := filepath.Clean(abs)
	if err := Validate(clean); err != nil {
		return "", err
	}
	return clean, nil
}

// Validate rejects paths incompatible with Docker's legacy Binds format
// (host:container:options). A ':' in the host path would be silently
// re-parsed as a field separator — e.g. /Users/foo:bar/project becomes
// bind source "/Users/foo", target "bar/project". Fail loudly so the
// user either renames the directory or opens toolbox from a safe path.
func Validate(p string) error {
	if strings.ContainsRune(p, ':') {
		return fmt.Errorf("workspace path %q contains ':' — Docker bind-mount format uses ':' as a separator; rename the directory or cd into a different path", p)
	}
	return nil
}

// ValidateAbsolute composes the absolute-path requirement with Validate.
// Callers feeding a user-supplied host path to Docker's Binds format must
// know it is rooted before they hand it off; this helper keeps the two
// checks paired so a future caller cannot regress by forgetting one.
func ValidateAbsolute(p string) error {
	if !filepath.IsAbs(p) {
		return fmt.Errorf("path %q is not absolute", p)
	}
	return Validate(p)
}
