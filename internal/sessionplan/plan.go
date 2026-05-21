// Package sessionplan owns the pipeline that turns a toolbox Config, a
// workspace path, --publish specs, and the host CLI version into the typed
// plan handed to internal/container.Shell: image reference, bind set,
// publish specs, env, working dir, container name, container Cmd, security
// opts, and build args. The single external seam is Plan; Merge is the
// pure-data twin used by tests.
package sessionplan

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/go-connections/nat"

	"github.com/filippolmt/toolbox/internal/build"
	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/mountplan"
	"github.com/filippolmt/toolbox/internal/sdd"
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
	Cmd           []string
	SecurityOpt   []string
	BuildArgs     map[string]*string
	// ExtraHosts is the docker --add-host list. Populated when the browser
	// bridge is enabled so host.docker.internal resolves on native Linux
	// Docker (Docker Desktop already provides the mapping; the duplicate
	// entry is harmless there).
	ExtraHosts []string
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
	Cmd           []string
	SecurityOpt   []string
	BuildArgs     map[string]*string
}

// Plan walks the full session pipeline for cfg + workspace + ports and
// returns the resolved plan handed to container.Shell. Hard fails when
// mount-stage validation rejects the user list, when port specs cannot
// be parsed, or when the home directory cannot be resolved. Per-mount
// soft skips surface via SessionPlan.Warnings.
//
// bridgeLoopback toggles the in-container loopback bridge (init.d/70):
// when true and at least one publish spec is present, the resulting env
// carries TOOLBOX_LOOPBACK_BRIDGE_PORTS so the bridge listener spawns one
// socat per published container port — see
// docs/runtime-notes.md#loopback-bridge.
func Plan(cfg *config.Config, workspace string, ports []string, bridgeLoopback bool, cliVersion string) (*SessionPlan, error) {
	workspace = normalizeWorkspace(workspace)

	exposed, bindings, uniqContainerPorts, err := parsePublishSpecs(ports)
	if err != nil {
		return nil, err
	}

	ref, isLocal := build.ResolveImage(cfg, cliVersion)

	// Resolve the container Cmd up front so an incoherent shell+tools
	// combination fails before any fs side effects (mountplan.Plan creates
	// dirs/symlinks under ~/.toolbox; we don't want them on a config error).
	cmd, err := ResolveShellCmd(cfg)
	if err != nil {
		return nil, err
	}

	// mountplan.Plan owns the fs side effects (mkdir, symlinks); per-mount
	// soft skips ride out on Warnings.
	mp, err := mountplan.Plan(cfg, workspace)
	if err != nil {
		return nil, err
	}

	return &SessionPlan{
		Image:         Image{Ref: ref, IsLocal: isLocal},
		Binds:         mp.Binds,
		Warnings:      mp.Warnings,
		WorkingDir:    mp.WorkingDir,
		ExposedPorts:  exposed,
		PortBindings:  bindings,
		Env:           append(shellEnv(workspace, mp.WorkingDir, cfg.SDD, cfg.Tools), loopbackBridgeEnv(bridgeLoopback, uniqContainerPorts)...),
		ContainerName: ContainerNameFor(workspace),
		Cmd:           cmd,
		SecurityOpt:   NestedSandboxSecurityOpt(cfg),
		BuildArgs:     build.BuildArgsFromTools(cfg.Tools),
		ExtraHosts:    browserBridgeExtraHosts(cfg),
	}, nil
}

// browserBridgeExtraHosts returns the docker --add-host entries needed for
// the in-container wrapper to reach the host daemon. Empty when the bridge
// is disabled.
func browserBridgeExtraHosts(cfg *config.Config) []string {
	if cfg.BrowserBridge != nil && !*cfg.BrowserBridge {
		return nil
	}
	return []string{"host.docker.internal:host-gateway"}
}

// loopbackBridgeEnv returns the `TOOLBOX_LOOPBACK_BRIDGE_*` env entries the
// container needs to enable the runtime bridge for the requested publish
// specs. Returns nil when bridgeLoopback is false. When bridgeLoopback is
// true with no published container ports, only the no-publish marker env
// is emitted so the in-container init.d/70 script can warn the user about
// the no-op. When ports are present, the deduplicated, insertion-ordered
// list is comma-joined into TOOLBOX_LOOPBACK_BRIDGE_PORTS.
func loopbackBridgeEnv(bridgeLoopback bool, uniqContainerPorts []string) []string {
	if !bridgeLoopback {
		return nil
	}
	if len(uniqContainerPorts) == 0 {
		// init.d/70 reads this marker to emit the "enabled but no -p ports
		// published — skipping" warning. The marker is intentionally absent
		// in the happy path; a non-empty TOOLBOX_LOOPBACK_BRIDGE_PORTS is
		// itself the "enabled" signal.
		return []string{"TOOLBOX_LOOPBACK_BRIDGE_NO_PUBLISH=1"}
	}
	return []string{"TOOLBOX_LOOPBACK_BRIDGE_PORTS=" + strings.Join(uniqContainerPorts, ",")}
}

// Merge returns the pure-data plan shape: identical to Plan but composes
// mountplan.Merge (no fs side effects) and exposes Binds as the post-merge
// config.Mount slice. Tests asserting the contract construct merged plans
// without t.TempDir / HOME setup.
func Merge(cfg *config.Config, workspace string, ports []string, bridgeLoopback bool, cliVersion string) (*MergedSessionPlan, error) {
	workspace = normalizeWorkspace(workspace)

	exposed, bindings, uniqContainerPorts, err := parsePublishSpecs(ports)
	if err != nil {
		return nil, err
	}

	ref, isLocal := build.ResolveImage(cfg, cliVersion)

	merged, err := mountplan.Merge(cfg)
	if err != nil {
		return nil, err
	}

	// Pure WorkingDir: mountplan.WorkspaceMirrorPath is fs-free, so Merge
	// can match Plan's mirror-or-target choice without touching disk.
	workingDir := mountplan.WorkspaceTarget
	if mirror, ok := mountplan.WorkspaceMirrorPath(workspace); ok {
		workingDir = mirror
	}

	cmd, err := ResolveShellCmd(cfg)
	if err != nil {
		return nil, err
	}

	return &MergedSessionPlan{
		Image:         Image{Ref: ref, IsLocal: isLocal},
		Binds:         merged,
		WorkingDir:    workingDir,
		ExposedPorts:  exposed,
		PortBindings:  bindings,
		Env:           append(shellEnv(workspace, workingDir, cfg.SDD, cfg.Tools), loopbackBridgeEnv(bridgeLoopback, uniqContainerPorts)...),
		ContainerName: ContainerNameFor(workspace),
		Cmd:           cmd,
		SecurityOpt:   NestedSandboxSecurityOpt(cfg),
		BuildArgs:     build.BuildArgsFromTools(cfg.Tools),
	}, nil
}

// MissingPublishPorts returns the wanted publish ports that the existing
// container was not created with. PortBindings are fixed at create time,
// so "--publish" on a reused container is a silent no-op for any port
// not in this list. nil-safe against InspectResponse.ContainerJSONBase
// and HostConfig.
func MissingPublishPorts(wanted nat.PortMap, inspect container.InspectResponse) []string {
	if inspect.ContainerJSONBase == nil || inspect.HostConfig == nil {
		return nil
	}
	current := inspect.HostConfig.PortBindings
	var missing []string
	for port := range wanted {
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

// MaxContainerNameLen is the documented Docker convention cap for
// container names. Exposed so cmd/ validators can budget the user-supplied
// portion (workspace basename or named-shell name) without rederiving it.
const MaxContainerNameLen = 63

// WorkspaceHashLen is the length of the trailing hash suffix in the
// workspace container format (`toolbox-<base>-<8hex>`). cmd/ uses this
// value to reject named-shell names whose sanitized form would be
// indistinguishable from the hash suffix.
const WorkspaceHashLen = 8

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
	abs := normalizeWorkspace(workspace)
	hash := hashWorkspace(abs)

	base := strings.ToLower(filepath.Base(abs))
	base = sanitizeRe.ReplaceAllString(base, "-")
	base = strings.Trim(base, "-")
	if base == "" {
		base = "root"
	}

	// MaxContainerNameLen - len("toolbox-") - len("-") - WorkspaceHashLen.
	maxBasename := MaxContainerNameLen - len(ContainerNamePrefix) - 1 - WorkspaceHashLen
	if len(base) > maxBasename {
		base = strings.TrimRight(base[:maxBasename], "-")
		if base == "" {
			base = "root"
		}
	}

	return ContainerNamePrefix + base + "-" + hash
}

// MaxNamedShellNameLen is the cap for the sanitized form of a named shell
// name, derived from MaxContainerNameLen minus the named-shell prefix +
// infix. cmd/ validators consume this so a prefix rename keeps the cap in
// lockstep.
const MaxNamedShellNameLen = MaxContainerNameLen - len(ContainerNamePrefix) - len(NamedContainerNameInfix)

// NamedContainerNameInfix separates the ContainerNamePrefix from the
// user-supplied shell name. The dedicated infix guarantees the named-shell
// container format is disjoint from the workspace-hash format produced by
// ContainerNameFor (`toolbox-<base>-<8hex>`): a workspace whose basename is
// literally "named" still gets a trailing -<8hex> suffix, while named-shell
// containers never carry one (callers reject hash-shaped names upstream).
const NamedContainerNameInfix = "named-"

// SanitizeShellName lowercases and slug-collapses the user-supplied shell
// name using the same character class as the workspace basename portion of
// ContainerNameFor. Exposed so cmd/* can validate the sanitized form (e.g.
// reject names that would collide with the workspace-hash suffix) without
// re-implementing the regex.
func SanitizeShellName(name string) string {
	base := strings.ToLower(strings.TrimSpace(name))
	base = sanitizeRe.ReplaceAllString(base, "-")
	return strings.Trim(base, "-")
}

// NamedContainerName returns the container name used by `toolbox shell <name>`
// and `toolbox stop <name>`.
//
// Format: toolbox-named-<sanitized>. The named- infix keeps this disjoint
// from the workspace-hash format (toolbox-<base>-<8hex>); cmd/ validation
// additionally refuses sanitized names that match the 8-hex hash pattern so
// a named shell cannot impersonate a workspace container.
func NamedContainerName(name string) string {
	return NamedContainerNameFromSanitized(SanitizeShellName(name))
}

// NamedContainerNameFromSanitized is the post-sanitization sibling of
// NamedContainerName. cmd/ already runs SanitizeShellName during input
// validation, so threading the sanitized form back through this entry
// avoids a redundant regex+lower+trim pass on every `toolbox shell <name>`.
func NamedContainerNameFromSanitized(sanitized string) string {
	if sanitized == "" {
		sanitized = "shell"
	}
	return ContainerNamePrefix + NamedContainerNameInfix + sanitized
}

// normalizeWorkspace returns the workspace path as an absolute, cleaned
// path. Falls back to the input on Abs failure (the empty string and other
// pathological inputs propagate untouched and surface downstream).
func normalizeWorkspace(workspace string) string {
	if abs, err := filepath.Abs(workspace); err == nil {
		return filepath.Clean(abs)
	}
	return workspace
}

// --- Port Parsing ---

// parsePublishSpecs parses "docker run -p"-style publish specs into
// Docker's ExposedPorts + PortBindings, and the ordered slice of unique
// container ports (insertion order; deduplicated) used by callers that
// need to iterate ports deterministically — currently loopbackBridgeEnv,
// which forwards each to its socat listener. Defaults the host IP to
// 127.0.0.1 (not 0.0.0.0) so OAuth callbacks stay loopback-only instead
// of being exposed to the LAN.
func parsePublishSpecs(specs []string) (nat.PortSet, nat.PortMap, []string, error) {
	if len(specs) == 0 {
		return nil, nil, nil, nil
	}
	exposed := nat.PortSet{}
	bindings := nat.PortMap{}
	seenPorts := make(map[string]struct{}, len(specs))
	orderedPorts := make([]string, 0, len(specs))
	for _, spec := range specs {
		mappings, err := nat.ParsePortSpec(spec)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("invalid --publish %q: %w", spec, err)
		}
		for _, m := range mappings {
			exposed[m.Port] = struct{}{}
			b := m.Binding
			if b.HostIP == "" {
				b.HostIP = "127.0.0.1"
			}
			bindings[m.Port] = append(bindings[m.Port], b)

			port := m.Port.Port() // drops "/tcp" suffix
			if _, dup := seenPorts[port]; !dup {
				seenPorts[port] = struct{}{}
				orderedPorts = append(orderedPorts, port)
			}
		}
	}
	return exposed, bindings, orderedPorts, nil
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
func shellEnv(workspace, workingDir string, sddFlags, tools map[string]bool) []string {
	env := []string{
		"TOOLBOX_HOST_WORKSPACE=" + workspace,
		"PWD=" + workingDir,
	}
	env = append(env, sddEnv(workspace, sddFlags, tools)...)
	return env
}

// sddEnv translates the cfg.SDD opt-in map into the env contract consumed
// by internal/build/assets/entrypoint.sh. Unknown keys are silently dropped
// so a typo in .toolbox.yaml (e.g. `sdd.gds: true`) never aborts the shell
// — the bash bootstrap runs before any user-facing diagnostic surface.
//
// tools is forwarded to Skill.StepsFor when set; see internal/sdd.Skill
// for the per-skill contract.
//
// Field-per-env-var encoding (vs a single pipe-packed value) keeps the
// host/container boundary typed: bash decodes by reading one variable per
// field with no fragile splitting.
func sddEnv(workspace string, sddFlags, tools map[string]bool) []string {
	if len(sddFlags) == 0 {
		return nil
	}
	enabled := resolveEnabledSkills(sddFlags)
	if len(enabled) == 0 {
		return nil
	}

	keys := make([]string, 0, len(enabled))
	for k := range enabled {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	emitted := make([]string, 0, len(keys))
	body := make([]string, 0, len(keys)*5)
	for _, k := range keys {
		s := enabled[k]
		stepArgs := s.InstallSteps
		if s.StepsFor != nil {
			stepArgs = s.StepsFor(tools)
			if len(stepArgs) == 0 {
				continue
			}
		}
		steps := make([]string, len(stepArgs))
		for i, args := range stepArgs {
			steps[i] = strings.Join(args, " ")
		}
		emitted = append(emitted, k)
		body = append(body,
			sdd.SkillEnvKey(k, sdd.EnvFieldPkg)+"="+s.NpmPackage,
			sdd.SkillEnvKey(k, sdd.EnvFieldVersion)+"="+s.Version,
			sdd.SkillEnvKey(k, sdd.EnvFieldBin)+"="+s.BinName,
			sdd.SkillEnvKey(k, sdd.EnvFieldSteps)+"="+strings.Join(steps, sdd.StepSeparator),
			sdd.SkillEnvKey(k, sdd.EnvFieldMarker)+"="+s.RequiresMarker,
		)
	}
	if len(emitted) == 0 {
		return nil
	}

	out := make([]string, 0, 2+len(body))
	out = append(out,
		sdd.EnvEnabled+"="+strings.Join(emitted, ","),
		sdd.EnvWorkspaceHash+"="+hashWorkspace(workspace),
	)
	out = append(out, body...)
	return out
}

// resolveEnabledSkills returns the map of known skill keys to their
// registry entries, after filtering sddFlags through the registry to
// drop unknown keys and false values.
func resolveEnabledSkills(sddFlags map[string]bool) map[string]sdd.Skill {
	out := make(map[string]sdd.Skill, len(sddFlags))
	for k, on := range sddFlags {
		if !on {
			continue
		}
		if s, ok := sdd.Lookup(k); ok {
			out[k] = s
		}
	}
	return out
}

// hashWorkspace is the single source of the WorkspaceHashLen-byte hash
// derived from a workspace path. Shared by ContainerNameFor (container
// name suffix) and sddEnv (sentinel filename suffix the entrypoint reads
// to decide whether a skill is up to date). Computing it twice would
// drift if the slicing rule changed.
func hashWorkspace(workspace string) string {
	sum := sha256.Sum256([]byte(workspace))
	return hex.EncodeToString(sum[:])[:WorkspaceHashLen]
}
