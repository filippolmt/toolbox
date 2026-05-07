// Package sessionplan owns the full pipeline that turns a toolbox Config,
// a workspace path, --publish specs, and the host CLI version into the
// typed plan handed to internal/container.Shell: image reference, bind
// set (delegating to mountplan), publish specs, env, working dir, and
// container name.
//
// The pipeline is intentionally a single concept exposed at one external
// seam — Plan(cfg, workspace, ports, cliVersion) — even though internally
// it walks five distinct stages (port parse, image resolve, mount compose,
// container-name derivation, env synthesis). Callers and tests both cross
// the same seam.
//
// Before this concept was named, cmd/shell.go::runShell and
// internal/container/lifecycle.Shell each ran the same sequencing inline,
// with image / mounts / ports / name / env derivations scattered across
// two call sites and three packages. The "Session Plan" name turns the
// sequencing into one observable typed plan that tests construct without
// Docker (SESS-05).
package sessionplan

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/go-connections/nat"

	"github.com/filippolmt/toolbox/internal/build"
	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/mountplan"
)

// --- Public Seams ---

// Image identifies the container image to launch and whether it must be
// built locally (custom tools config) versus pulled from the registry
// (defaults config).
type Image struct {
	Ref     string
	IsLocal bool
}

// SessionPlan is the resolved-fs plan returned by Plan. Binds are the
// typed mountplan.Bind entries with sources fully resolved (symlinks
// created, dirs materialised). Lifecycle stringifies them at the daemon
// edge.
type SessionPlan struct {
	Image         Image
	Binds         []mountplan.Bind
	Warnings      []string
	WorkingDir    string
	ExposedPorts  nat.PortSet
	PortBindings  nat.PortMap
	Env           []string
	ContainerName string
}

// MergedSessionPlan is the pure-data shape returned by Merge. Binds are
// the post-merge config.Mount slice (no filesystem side-effects). Tests
// assert merge decisions at this layer without invoking mountplan.Plan.
type MergedSessionPlan struct {
	Image         Image
	Binds         []config.Mount
	WorkingDir    string
	ExposedPorts  nat.PortSet
	PortBindings  nat.PortMap
	Env           []string
	ContainerName string
}

// Plan walks the full session pipeline for cfg + workspace + ports and
// returns the resolved plan handed to container.Shell. Hard fails when
// mount-stage validation rejects the user list, when port specs cannot
// be parsed, or when the home directory cannot be resolved. Per-mount
// soft skips surface via SessionPlan.Warnings.
func Plan(cfg *config.Config, workspace string, ports []string, cliVersion string) (*SessionPlan, error) {
	// STUB — wired in Task 2. Returns zero-value plan + nil error so the
	// package compiles. Task 2 fills in: workspace normalize → port parse
	// → image resolve → mountplan.Plan → ContainerNameFor → shellEnv.
	return &SessionPlan{}, nil
}

// Merge returns the pure-data plan shape: identical to Plan but composes
// mountplan.Merge (no fs side effects) and exposes Binds as the post-merge
// config.Mount slice. Tests asserting the contract construct merged plans
// without t.TempDir / HOME setup.
func Merge(cfg *config.Config, workspace string, ports []string, cliVersion string) (*MergedSessionPlan, error) {
	// STUB — wired in Task 2. Returns zero-value plan + nil error.
	return &MergedSessionPlan{}, nil
}

// MissingPublishPorts returns the wanted publish ports that the existing
// container was not created with. PortBindings are fixed at create time,
// so "--publish" on a reused container is a silent no-op for any port
// not in this list. nil-safe against InspectResponse.ContainerJSONBase
// and HostConfig (CONT-05 / Pitfall 7).
func MissingPublishPorts(plan *SessionPlan, inspect container.InspectResponse) []string {
	if inspect.ContainerJSONBase == nil || inspect.HostConfig == nil {
		return nil
	}
	current := inspect.HostConfig.PortBindings
	var missing []string
	for port := range plan.PortBindings {
		if _, ok := current[port]; !ok {
			missing = append(missing, string(port))
		}
	}
	return missing
}

// ContainerNamePrefix is the prefix that identifies toolbox-managed
// containers. Exported so internal/container.StopAll can filter the
// host's full container list without taking a SessionPlan input.
const ContainerNamePrefix = "toolbox-"

var sanitizeRe = regexp.MustCompile(`[^a-z0-9]+`)

// ContainerNameFor builds the container name for a given workspace path.
// Exported so internal/container.Stop (which has no SessionPlan in scope)
// can reuse the format. Plan and Merge call this internally and stash the
// result on plan.ContainerName.
//
// Format: toolbox-<basename>-<hash8>. The hash is over the absolute path
// so two directories sharing a basename do not collide. Output capped at
// 63 chars (Docker convention): basename is truncated first so the prefix
// and hash suffix survive.
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

	return ContainerNamePrefix + base + "-" + hash
}

// --- Port Parsing ---

// parsePublishSpecs parses "docker run -p"-style publish specs into
// Docker's ExposedPorts + PortBindings. Defaults the host IP to
// 127.0.0.1 (not 0.0.0.0) so OAuth callbacks stay loopback-only
// instead of being exposed to the LAN.
func parsePublishSpecs(specs []string) (nat.PortSet, nat.PortMap, error) {
	if len(specs) == 0 {
		return nil, nil, nil
	}
	exposed := nat.PortSet{}
	bindings := nat.PortMap{}
	for _, spec := range specs {
		mappings, err := nat.ParsePortSpec(spec)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid --publish %q: %w", spec, err)
		}
		for _, m := range mappings {
			exposed[m.Port] = struct{}{}
			b := m.Binding
			if b.HostIP == "" {
				b.HostIP = "127.0.0.1"
			}
			bindings[m.Port] = append(bindings[m.Port], b)
		}
	}
	return exposed, bindings, nil
}

// --- Workspace Env ---

// shellEnv returns the env vars injected into every shell spawned by the
// container. TOOLBOX_HOST_WORKSPACE holds the absolute host path mounted
// at the canonical workspace target so that Makefiles and compose files
// can pass a host-resolvable path to `docker run -v` under the
// bind-mounted socket (DooD): a literal "/workspace/foo" is meaningless
// to the host daemon. PWD is set explicitly to workingDir so that scripts
// reading $PWD directly (without a getcwd fallback) see the same path
// bash exposes after starting in WorkingDir.
//
// The workspace target itself and the host-path mirror logic live in
// internal/mountplan; sessionplan.Plan consults mountplan.Plan to learn
// workingDir and forwards it here.
func shellEnv(workspace, workingDir string) []string {
	return []string{
		"TOOLBOX_HOST_WORKSPACE=" + workspace,
		"PWD=" + workingDir,
	}
}

// _ silences the unused-import linter while Plan/Merge bodies are stubs.
// Removed in Task 2 once both Seam bodies are wired.
var _ = build.ResolveImage
var _ = mountplan.Plan
