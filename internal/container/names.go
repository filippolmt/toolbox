package container

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"regexp"
	"strings"
)

// containerNamePrefix identifies containers managed by toolbox.
const containerNamePrefix = "toolbox-"

var sanitizeRe = regexp.MustCompile(`[^a-z0-9]+`)

// ContainerNameFor builds the container name for a given workspace path.
// Format: toolbox-<basename>-<hash8>. The hash is over the absolute path so
// that two directories sharing the same basename do not collide. Output is
// capped at 63 characters to respect Docker's conventional name length: the
// basename is truncated first so the stable prefix and hash suffix survive.
func ContainerNameFor(workspace string) string {
	abs, err := filepath.Abs(workspace)
	if err != nil {
		abs = workspace
	}
	abs = filepath.Clean(abs)

	sum := sha256.Sum256([]byte(abs))
	hash := hex.EncodeToString(sum[:])[:8]

	base := strings.ToLower(filepath.Base(abs))
	base = sanitizeRe.ReplaceAllString(base, "-")
	base = strings.Trim(base, "-")
	if base == "" {
		base = "root"
	}

	// 63 (Docker convention) - len("toolbox-") - len("-") - 8 (hash) = 46.
	const maxBasename = 46
	if len(base) > maxBasename {
		base = strings.TrimRight(base[:maxBasename], "-")
		if base == "" {
			base = "root"
		}
	}

	return containerNamePrefix + base + "-" + hash
}
