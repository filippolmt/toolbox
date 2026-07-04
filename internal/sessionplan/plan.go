// Package sessionplan owns the pipeline that turns a toolbox Config, a
// workspace path, and --publish specs into the typed plan handed to
// internal/container.Shell: image reference, bind set, publish specs, env,
// working dir, container name, container Cmd, security opts. Plan is the
// external seam with filesystem side effects; Merge is the pure-data twin
// used by tests.
package sessionplan

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/netip"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/docker/go-connections/nat"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"

	"github.com/filippolmt/toolbox/internal/build"
	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/mountplan"
	"github.com/filippolmt/toolbox/internal/proximo"
	"github.com/filippolmt/toolbox/internal/sdd"
	"github.com/filippolmt/toolbox/internal/version"
)

// --- Public Seams ---

// Image identifies the container image to launch. Ref defaults to the
// canonical registry image (`toolbox build` overwrites its local cache) but
// can be relocated, opt-in, via config Image / RegistryMirror —
// build.ResolveImage owns the precedence. PullPolicy mirrors config.Pull
// ("auto" | "always" | "never") and drives imageplan.Refresh.
type Image struct {
	Ref        string
	PullPolicy string
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
	ExposedPorts  network.PortSet
	PortBindings  network.PortMap
	Env           []string
	ContainerName string
	Cmd           []string
	// ExecCmd overrides the command run in the attached interactive exec
	// session. When nil the exec reuses Cmd (the normal shell). Callers that
	// must keep the container's main process an idle shell while running a
	// different command in the user's attached session (e.g. toolbox worktree
	// auto-launching an agent) set this so the agent does not also run headless
	// in the container's main PID. Lifecycle.Shell reads it at the exec edge.
	ExecCmd     []string
	SecurityOpt []string
	// ExtraHosts is the docker --add-host list. Populated when the browser
	// bridge is enabled so host.docker.internal resolves on native Linux
	// Docker (Docker Desktop already provides the mapping; the duplicate
	// entry is harmless there).
	ExtraHosts []string
	// OverlayDockerfile is the resolved host path of the optional local image
	// overlay (~/.toolbox/Dockerfile, mounts_root-aware). Always set; the
	// container edge (localimage.Ensure) passes through unchanged when the
	// file is absent and only builds the derived `:local` image when present.
	OverlayDockerfile string
	// Proximo mirrors cfg.Proximo. When true, the container edge augments
	// ExtraHosts with the proximo-routed hostnames discovered from running
	// containers (pinned to host-gateway) just before ContainerCreate — that
	// discovery needs the Docker client, which only the container package
	// holds. The CA bind + NODE_EXTRA_CA_CERTS/TOOLBOX_PROXIMO_CA env are
	// already resolved into Binds/Env here (pure, host-side).
	Proximo bool
}

// MergedSessionPlan is the pure-data shape returned by Merge. Binds are
// the post-merge config.Mount slice (no filesystem side-effects). Tests
// assert merge decisions at this layer without invoking mountplan.Plan.
type MergedSessionPlan struct {
	Image         Image
	Binds         []config.Mount
	WorkingDir    string
	ExposedPorts  network.PortSet
	PortBindings  network.PortMap
	Env           []string
	ContainerName string
	Cmd           []string
	SecurityOpt   []string
}

// PlanInput is the full set of inputs to Plan and Merge. The two share one
// struct so the pure-data Merge stays signature-parallel with the fs-touching
// Plan; Merge ignores ImageDigest (the poller identity is an fs-free-plan
// non-concern). Bundling the inputs keeps the container-name decision — the
// one field that varies between a workspace session and a named shell — a
// single Name input rather than a post-planning override in the caller.
type PlanInput struct {
	Cfg            *config.Config
	Workspace      string
	Ports          []string
	BridgeLoopback bool

	// ImageDigest is the running image's resolved repo digest (`sha256:...`),
	// supplied host-side by the caller (it needs the Docker client, which the
	// pure planner does not hold). Empty when unresolvable — e.g. a locally
	// built untagged image; the identity injection then omits the digest entry
	// so the in-container poller skips the image check rather than treating an
	// empty value as a stale digest. Ignored by Merge. See update-notification.
	ImageDigest string

	// Name is the sanitized named-shell name (see SanitizeShellName). Empty for
	// workspace sessions (no-arg / absolute-path), where the container name
	// derives from the workspace path hash; non-empty routes the name through
	// NamedContainerNameFromSanitized (`toolbox-named-<sanitized>`). Keeping the
	// decision here means cmd never handles the container-name format — it sets
	// Name by intent and nothing else.
	Name string

	// Profile is the active `toolbox shell --profile` selection, or nil for a
	// default (non-profile) session. It drives the mount plan (its own root +
	// share skip-set, owned by mountplan.Profile) and is folded into the
	// container name so a profile shell never collides with the default shell
	// for the same workspace, each keeping its own mount set fixed at
	// ContainerCreate.
	Profile *mountplan.Profile
}

// containerName resolves the container name from the workspace path and the
// optional named-shell name — the single place the workspace-hash vs
// named-shell choice lives. Empty name → workspace-derived; non-empty →
// named form. bridgeLoopback-free and fs-free, so both Plan and Merge share
// it.
func containerName(workspace, name string, profile *mountplan.Profile) string {
	if name == "" {
		// Workspace sessions fold the full profile discriminator (name + share
		// set) into the hash, so switching profile OR --share yields a distinct
		// container — mounts are fixed at ContainerCreate.
		return ContainerNameFor(workspace, mountplan.ContainerDiscriminator(profile))
	}
	// Named shells fold the profile name into the sanitized name so a profile
	// named-shell keeps a distinct container from the default one. The suffix
	// carries no -<8hex>, so it stays disjoint from the workspace-hash format.
	// (A --share change alone reuses the container — refresh with `toolbox stop`,
	// same as any mount/port flag; see docs/commands.md#profiles.)
	if pn := mountplan.ProfileName(profile); pn != "" {
		name = name + "-" + SanitizeShellName(pn)
	}
	return NamedContainerNameFromSanitized(name)
}

// Plan walks the full session pipeline for in.Cfg + in.Workspace + in.Ports
// and returns the resolved plan handed to container.Shell. Hard fails when
// mount-stage validation rejects the user list, when port specs cannot
// be parsed, or when the home directory cannot be resolved. Per-mount
// soft skips surface via SessionPlan.Warnings.
//
// in.BridgeLoopback toggles the in-container loopback bridge (init.d/70):
// when true and at least one publish spec is present, the resulting env
// carries TOOLBOX_LOOPBACK_BRIDGE_PORTS so the bridge listener spawns one
// socat per published container port — see docs/commands.md#loopback-bridge.
func Plan(in PlanInput) (*SessionPlan, error) {
	workspace := normalizeWorkspace(in.Workspace)

	exposed, bindings, uniqContainerPorts, err := parsePublishSpecs(in.Ports)
	if err != nil {
		return nil, err
	}

	ref := build.ResolveImage(in.Cfg.Image, in.Cfg.RegistryMirror)

	// Resolve the container Cmd up front so an incoherent shell+tools
	// combination fails before any fs side effects (mountplan.Plan creates
	// dirs/symlinks under ~/.toolbox; we don't want them on a config error).
	cmd, err := ResolveShellCmd(in.Cfg)
	if err != nil {
		return nil, err
	}

	// mountplan.Plan owns the fs side effects (mkdir, symlinks); per-mount
	// soft skips ride out on Warnings.
	mp, err := mountplan.Plan(in.Cfg, workspace, in.Profile)
	if err != nil {
		return nil, err
	}

	// Resolve the local overlay Dockerfile path (mounts_root-aware). Runs
	// after mountplan.Plan so the same home-resolution failure surfaces once.
	overlayDockerfile, err := mountplan.OverlayDockerfilePath(in.Cfg, in.Profile)
	if err != nil {
		return nil, err
	}

	return &SessionPlan{
		Image:             Image{Ref: ref, PullPolicy: in.Cfg.Pull},
		Binds:             mp.Binds,
		Warnings:          mp.Warnings,
		WorkingDir:        mp.WorkingDir,
		ExposedPorts:      exposed,
		PortBindings:      bindings,
		Env:               composeEnv(workspace, mp.WorkingDir, in.Cfg, in.BridgeLoopback, uniqContainerPorts, in.ImageDigest, proximo.Env(in.Cfg)),
		ContainerName:     containerName(workspace, in.Name, in.Profile),
		Cmd:               cmd,
		SecurityOpt:       NestedSandboxSecurityOpt(in.Cfg),
		ExtraHosts:        browserBridgeExtraHosts(in.Cfg),
		OverlayDockerfile: overlayDockerfile,
		Proximo:           proximo.Enabled(in.Cfg),
	}, nil
}

// browserBridgeExtraHosts returns the docker --add-host entries needed for
// the in-container wrapper to reach the host daemon. Empty when the bridge
// is disabled.
func browserBridgeExtraHosts(cfg *config.Config) []string {
	if cfg.Bridge != nil && !*cfg.Bridge {
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
func Merge(in PlanInput) (*MergedSessionPlan, error) {
	workspace := normalizeWorkspace(in.Workspace)

	exposed, bindings, uniqContainerPorts, err := parsePublishSpecs(in.Ports)
	if err != nil {
		return nil, err
	}

	ref := build.ResolveImage(in.Cfg.Image, in.Cfg.RegistryMirror)

	merged, err := mountplan.Merge(in.Cfg, in.Profile)
	if err != nil {
		return nil, err
	}

	// Pure WorkingDir: mountplan.WorkspaceMirrorPath is fs-free, so Merge
	// can match Plan's mirror-or-target choice without touching disk.
	workingDir := mountplan.WorkspaceTarget
	if mirror, ok := mountplan.WorkspaceMirrorPath(workspace); ok {
		workingDir = mirror
	}

	cmd, err := ResolveShellCmd(in.Cfg)
	if err != nil {
		return nil, err
	}

	return &MergedSessionPlan{
		Image:         Image{Ref: ref, PullPolicy: in.Cfg.Pull},
		Binds:         merged,
		WorkingDir:    workingDir,
		ExposedPorts:  exposed,
		PortBindings:  bindings,
		Env:           composeEnv(workspace, workingDir, in.Cfg, in.BridgeLoopback, uniqContainerPorts, "", nil),
		ContainerName: containerName(workspace, in.Name, in.Profile),
		Cmd:           cmd,
		SecurityOpt:   NestedSandboxSecurityOpt(in.Cfg),
	}, nil
}

// composeEnv assembles the full ordered env slice for a session: the curated
// workspace + SDD entries first, then the loopback-bridge markers, then the
// self-identity entries (CLI version + image digest) the in-container update
// poller compares against published releases, then any caller-supplied
// curated extras (Plan passes proximo.Env, which stats the host CA —
// fs-touching, so the pure-data Merge passes nil), then the user-supplied
// env: map. Reserved-key collisions are already rejected by config.ValidateEnv,
// so userEnv can append unconditionally.
func composeEnv(workspace, workingDir string, cfg *config.Config, bridgeLoopback bool, uniqContainerPorts []string, imageDigest string, extra []string) []string {
	env := append(shellEnv(workspace, workingDir, cfg.SDD), loopbackBridgeEnv(bridgeLoopback, uniqContainerPorts)...)
	env = append(env, identityEnv(imageDigest)...)
	env = append(env, managedStatuslineEnv(cfg.ManagedStatusline)...)
	env = append(env, extra...)
	return append(env, userEnv(cfg.Env)...)
}

// identityEnv emits the self-identification env that lets the in-container
// update poller compare the running toolbox against published releases:
// TOOLBOX_CLI_VERSION (always, from the host CLI build) and
// TOOLBOX_IMAGE_DIGEST (only when the host resolved a repo digest). The
// digest entry is omitted — not emitted empty — when unresolvable so the
// poller skips the image check instead of reading an empty value as a stale
// digest and reporting a bogus "update available". See update-notification.
func identityEnv(imageDigest string) []string {
	out := []string{"TOOLBOX_CLI_VERSION=" + version.Version}
	if imageDigest != "" {
		out = append(out, "TOOLBOX_IMAGE_DIGEST="+imageDigest)
	}
	return out
}

// managedStatuslineEnv emits TOOLBOX_MANAGED_STATUSLINE=0 only when the user
// opted out (managed_statusline: false); nil/true emit nothing so the boot
// hook's default-on path runs. Emitting only on opt-out keeps the env minimal
// and the value unambiguous: present-and-0 = opt out, absent = managed.
func managedStatuslineEnv(managed *bool) []string {
	if managed != nil && !*managed {
		return []string{"TOOLBOX_MANAGED_STATUSLINE=0"}
	}
	return nil
}

// userEnv emits the cfg.Env map as deterministically ordered K=V strings.
// Sorted by key so the plan is stable across runs (map iteration order is
// nondeterministic) — load-bearing for the sessionplan tests.
func userEnv(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+env[k])
	}
	return out
}

// MissingPublishPorts returns the wanted publish ports that the existing
// container was not created with. PortBindings are fixed at create time,
// so "--publish" on a reused container is a silent no-op for any port
// not in this list. nil-safe against InspectResponse.HostConfig.
func MissingPublishPorts(wanted network.PortMap, inspect container.InspectResponse) []string {
	if inspect.HostConfig == nil {
		return nil
	}
	current := inspect.HostConfig.PortBindings
	var missing []string
	for port := range wanted {
		if _, ok := current[port]; !ok {
			missing = append(missing, port.String())
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
//
// A non-empty discriminator (the profile identity, from
// mountplan.ContainerDiscriminator) is folded into the hash seed (not the
// visible basename), so a profile shell gets a distinct name from the default
// shell for the same workspace while the format and length budget are
// unchanged. The plain workspace hash (sddEnv sentinel) stays discriminator-free
// — only the container identity varies by profile.
func ContainerNameFor(workspace, discriminator string) string {
	abs := normalizeWorkspace(workspace)
	seed := abs
	if discriminator != "" {
		seed = abs + "\x00profile=" + discriminator
	}
	hash := hashWorkspace(seed)

	base := workspaceSlug(abs)

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

// workspaceSlug lowercases and slug-collapses the basename of a (normalized)
// workspace path, falling back to "root" when nothing survives — e.g. the
// filesystem root, where filepath.Base returns "/". Shared by ContainerNameFor
// and shellEnv so the container name and the Remote Control session prefix
// derive from a single sanitization rule.
func workspaceSlug(abs string) string {
	base := strings.ToLower(filepath.Base(abs))
	base = sanitizeRe.ReplaceAllString(base, "-")
	base = strings.Trim(base, "-")
	if base == "" {
		return "root"
	}
	return base
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

// defaultHostIP keeps OAuth callbacks loopback-only: publish specs with no
// explicit host IP bind 127.0.0.1, not 0.0.0.0, so a callback port is never
// exposed to the LAN.
var defaultHostIP = netip.MustParseAddr("127.0.0.1")

// natMappingToBinding converts a go-connections publish mapping (kept solely
// because nat.ParsePortSpec has no moby equivalent) into the moby port +
// binding pair, applying the loopback host-IP default. This is the only
// nat→moby seam in the codebase.
func natMappingToBinding(m nat.PortMapping) (network.Port, network.PortBinding, error) {
	p, err := network.ParsePort(string(m.Port))
	if err != nil {
		return network.Port{}, network.PortBinding{}, err
	}
	hostIP := defaultHostIP
	if m.Binding.HostIP != "" {
		hostIP, err = netip.ParseAddr(m.Binding.HostIP)
		if err != nil {
			return network.Port{}, network.PortBinding{}, err
		}
	}
	return p, network.PortBinding{HostIP: hostIP, HostPort: m.Binding.HostPort}, nil
}

// parsePublishSpecs parses "docker run -p"-style publish specs into
// Docker's ExposedPorts + PortBindings, and the ordered slice of unique
// container ports (insertion order; deduplicated) used by callers that
// need to iterate ports deterministically — currently loopbackBridgeEnv,
// which forwards each to its socat listener. Defaults the host IP to
// 127.0.0.1 (not 0.0.0.0) so OAuth callbacks stay loopback-only instead
// of being exposed to the LAN.
func parsePublishSpecs(specs []string) (network.PortSet, network.PortMap, []string, error) {
	if len(specs) == 0 {
		return nil, nil, nil, nil
	}
	exposed := network.PortSet{}
	bindings := network.PortMap{}
	seenPorts := make(map[string]struct{}, len(specs))
	orderedPorts := make([]string, 0, len(specs))
	for _, spec := range specs {
		mappings, err := nat.ParsePortSpec(spec)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("invalid --publish %q: %w", spec, err)
		}
		for _, m := range mappings {
			p, binding, perr := natMappingToBinding(m)
			if perr != nil {
				return nil, nil, nil, fmt.Errorf("invalid --publish %q: %w", spec, perr)
			}
			exposed[p] = struct{}{}
			bindings[p] = append(bindings[p], binding)

			port := p.Port() // drops "/tcp" suffix
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
// CLAUDE_REMOTE_CONTROL_SESSION_NAME_PREFIX defaults to the machine
// hostname, which inside the container is the Docker container ID — on
// claude.ai (web/mobile) every Remote Control session would show up as
// hex gibberish. The workspace slug (same rule as the container name)
// gives readable session names that match `docker ps` output.
//
// The workspace target itself and the host-path mirror logic live in
// internal/mountplan; sessionplan.Plan consults mountplan.Plan to learn
// workingDir and forwards it here.
func shellEnv(workspace, workingDir string, sddCfg map[string]config.SDDSkill) []string {
	env := []string{
		"TOOLBOX_HOST_WORKSPACE=" + workspace,
		"PWD=" + workingDir,
		"CLAUDE_REMOTE_CONTROL_SESSION_NAME_PREFIX=" + workspaceSlug(workspace),
	}
	env = append(env, sddEnv(workspace, sddCfg)...)
	return env
}

// sddEnv translates the cfg.SDD opt-in map into the env contract consumed
// by internal/build/assets/entrypoint.sh. Unknown keys are silently dropped
// so a typo in .toolbox.yaml (e.g. `sdd.gds: true`) never aborts the shell
// — the bash bootstrap runs before any user-facing diagnostic surface.
// (Entries with an explicit steps: override are the exception: config's
// ValidateSDD rejects unknown keys there at load time, so they never reach
// this point.)
//
// Field-per-env-var encoding (vs a single pipe-packed value) keeps the
// host/container boundary typed: bash decodes by reading one variable per
// field with no fragile splitting.
func sddEnv(workspace string, sddCfg map[string]config.SDDSkill) []string {
	if len(sddCfg) == 0 {
		return nil
	}
	enabled := resolveEnabledSkills(sddCfg)
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
// registry entries, after filtering sddCfg through the registry to drop
// unknown keys and disabled entries. A per-skill steps: override from
// .toolbox.yaml replaces the registry's InstallSteps wholesale (#317):
// partial merge semantics would leave the user unable to drop a default
// step, which is exactly the gsd --claude --local case the override
// exists for.
func resolveEnabledSkills(sddCfg map[string]config.SDDSkill) map[string]sdd.Skill {
	out := make(map[string]sdd.Skill, len(sddCfg))
	for k, v := range sddCfg {
		if !v.Enabled {
			continue
		}
		s, ok := sdd.Lookup(k)
		if !ok {
			continue
		}
		if v.Steps != nil {
			s.InstallSteps = v.Steps
		}
		out[k] = s
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
