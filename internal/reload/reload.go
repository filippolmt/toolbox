// Package reload owns the two named channels of a session reload and nothing
// else: it is pure plus a little filesystem, so every package on the reload
// path can depend on it without a cycle.
//
// The two names share a prefix and point in opposite directions — the
// documentation must say so or someone will merge them:
//
//   - TOOLBOX_RELOAD_MARKER travels host → container. The host injects the
//     absolute path of this session's marker file; presence of the variable is
//     the capability declaration, so an image running under a CLI too old to
//     read the marker refuses at the prompt instead of spending the session
//     for nothing. The value carries the path because the path is three pieces
//     of host-owned knowledge (the state mount's target, the per-container
//     naming, the atomic write) and the container can reconstruct none of them.
//   - TOOLBOX_RELOAD_FROM travels host → host, across the syscall.Exec, and
//     never enters a container. It is consumed and unset before the session
//     plan builds the container env, which is a correctness property rather
//     than tidiness — hence one JSON variable instead of one per field.
//
// Every field of the payload is optional with a safe zero value except
// Container: lose that and nothing destroys the old container, the next
// `toolbox shell` reuses it, and the developer lands silently back on the old
// image — the exact failure the reload exists to remove. So an unparseable
// payload is a hard error, never a degrade.
package reload

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/filippolmt/toolbox/internal/fsx"
)

// MarkerEnv names the host-injected capability marker. Presence is the
// capability; the value is the marker's absolute path inside the container.
const MarkerEnv = "TOOLBOX_RELOAD_MARKER"

// FromEnv names the host-to-host handover across the re-exec.
const FromEnv = "TOOLBOX_RELOAD_FROM"

// markerPrefix namespaces the marker inside the shared state mount, whose
// other occupants are the update-check cache and the shell history.
const markerPrefix = "reload."

// From is the payload TOOLBOX_RELOAD_FROM carries. JSON tolerates unknown
// fields on read and absent fields as zero values, so no version field is
// needed — and a version check's failure mode (refusing to reload) would be
// worse than reloading without the cwd.
type From struct {
	// Container is the container the new process must destroy. Mandatory:
	// recomputing it is unsafe, because config on disk may have changed
	// between the two processes and a diverging recompute would tear down the
	// wrong container.
	Container string `json:"container"`
	// Cwd is the working directory the session was in, validated against the
	// workspace target by the consumer. Empty falls back to the canonical
	// working directory from the mount plan.
	Cwd string `json:"cwd,omitempty"`
	// ImageDigest and CLIVersion are the "before" half of the summary the
	// reload owes the developer — the command always reloads, so the summary
	// is the only evidence distinguishing a successful-but-pointless reload
	// from one that failed silently.
	ImageDigest string `json:"image_digest,omitempty"`
	CLIVersion  string `json:"cli_version,omitempty"`
}

// MarkerName is the marker's basename inside the state mount.
//
// Keyed on the container name rather than its id: the env is fixed at
// ContainerCreate, so the id does not exist yet when the value has to be
// computed, while the name is deterministic per workspace and identical on the
// connect path — where a sibling shell's host process never created anything
// and must still arrive at the same file. Both sides of the seam read this one
// function, so the path the container writes and the path the host checks
// cannot diverge.
func MarkerName(containerName string) string { return markerPrefix + containerName }

// MarkerPath joins a state directory (host source or container target) with
// the marker for containerName. Callers pass the side of the bind mount they
// live on; the basename is what makes it the same file.
func MarkerPath(stateDir, containerName string) string {
	return filepath.Join(stateDir, MarkerName(containerName))
}

// TakeMarker reads the marker at path and deletes it, reporting whether a
// reload was asked for. Deleting on read is what keeps a marker orphaned by a
// crashed session from firing later; the deletion is therefore unconditional
// on the read succeeding.
//
// The body is the working directory the session was in. An empty or unreadable
// body still counts as a reload request — the marker's existence is the
// request, its content is the one carried nicety.
func TakeMarker(path string) (cwd string, requested bool) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	_ = os.Remove(path)
	return strings.TrimRight(string(body), "\r\n"), true
}

// WriteMarker publishes a reload request at path. The host never calls this —
// the in-container `toolbox-reload` zsh function does — but the Go side owns
// the format, so the peer gate and any future host-side caller write it
// through the same code rather than a second spelling of the same bytes.
func WriteMarker(path, cwd string) error {
	return fsx.AtomicWriteFile(path, []byte(cwd+"\n"), 0o644)
}

// Encode renders the payload for the environment variable.
func Encode(f From) (string, error) {
	raw, err := json.Marshal(f)
	if err != nil {
		return "", fmt.Errorf("encode %s: %w", FromEnv, err)
	}
	return string(raw), nil
}

// Decode parses the payload. A missing container name is a hard error rather
// than a degrade: see the package comment.
func Decode(raw string) (*From, error) {
	var f From
	if err := json.Unmarshal([]byte(raw), &f); err != nil {
		return nil, fmt.Errorf("%s is not readable: %w", FromEnv, err)
	}
	if f.Container == "" {
		return nil, errors.New(FromEnv + " carries no container name")
	}
	return &f, nil
}

// Take reads and unsets the payload in one act, returning (nil, nil) when this
// process was not started by a reload. Unsetting here — before anything builds
// a container env — is what keeps the host-to-host variable out of the
// container.
func Take() (*From, error) {
	raw, ok := os.LookupEnv(FromEnv)
	if !ok {
		return nil, nil
	}
	// Unset before parsing: a payload we refuse must not survive into the
	// environment of anything this process goes on to start.
	_ = os.Unsetenv(FromEnv)
	if strings.TrimSpace(raw) == "" {
		return nil, errors.New(FromEnv + " is empty")
	}
	return Decode(raw)
}
