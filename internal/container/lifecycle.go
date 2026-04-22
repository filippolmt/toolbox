package container

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/jsonmessage"
	"github.com/docker/go-connections/nat"
	"golang.org/x/term"

	"github.com/filippolmt/toolbox/internal/build"
	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/mount"
	"github.com/filippolmt/toolbox/internal/ui"
	"github.com/filippolmt/toolbox/internal/version"
)

// cleanupTimeout bounds the best-effort container stop+remove that runs on
// exit with a fresh context. Long enough for Docker's own 10s stop grace
// period plus remove overhead; short enough that a frozen daemon doesn't
// hang the CLI forever.
const cleanupTimeout = 30 * time.Second

// WorkspaceTarget is the canonical in-container path where the host CWD is
// mounted. When it is safe to do so, the same host directory is also mirrored
// at its own absolute host path (see workspaceMirrorPath) and used as the
// shell WorkingDir, so `$PWD`-based bind mounts from inside the container
// resolve to a path the host daemon knows under DooD.
const WorkspaceTarget = "/workspace"

// reservedMirrorPrefixes lists in-container directories that must not be
// shadowed by the host-path mirror of the workspace. A host path equal to or
// nested under any of these is mounted only at WorkspaceTarget.
var reservedMirrorPrefixes = []string{
	WorkspaceTarget,
	"/home/toolbox",
	"/root",
	"/bin", "/sbin", "/lib", "/lib32", "/lib64", "/libx32",
	"/boot", "/dev", "/etc", "/proc", "/run", "/sys", "/usr", "/var",
}

// containerNamePrefix identifies containers managed by toolbox.
const containerNamePrefix = "toolbox-"

var sanitizeRe = regexp.MustCompile(`[^a-z0-9]+`)

// execShellFn attaches an interactive shell to a container.
// Exposed as a package-level var so tests can substitute it.
var execShellFn = execShell

// ShellMismatchError is returned when the requested shell cannot be launched
// because the corresponding tools entry is disabled. Callers pattern-match
// on this type to print a remediation message and exit non-zero (SHELL-03,
// D-18). The Error() message MUST include both the `shell: <name>` and
// `tools.<name>: false` substrings — a smoke assertion greps for them.
type ShellMismatchError struct {
	Shell string
}

func (e *ShellMismatchError) Error() string {
	return fmt.Sprintf(
		"toolbox: shell %q requested but tools.%s is disabled.\n"+
			"  shell: %s\n"+
			"  tools.%s: false\n"+
			"  • set `tools.%s: true` in ~/.toolbox.yaml, OR\n"+
			"  • set `shell: bash` to use bash instead",
		e.Shell, e.Shell, e.Shell, e.Shell, e.Shell)
}

// ResolveShellCmd returns the container command for the configured shell, or a
// typed *ShellMismatchError when the combination is incoherent (SHELL-02 +
// SHELL-03, D-17). Re-validates cfg.Shell defensively: Load() already rejects
// unsupported values, but callers that bypass Load() (tests, future entry
// points) must not be able to smuggle an arbitrary string into /bin/<x>.
func ResolveShellCmd(cfg *config.Config) ([]string, error) {
	if err := config.ValidateShell(cfg.Shell); err != nil {
		return nil, err
	}
	if cfg.Shell == "zsh" {
		if enabled, ok := cfg.Tools["zsh"]; ok && !enabled {
			return nil, &ShellMismatchError{Shell: "zsh"}
		}
	}
	return []string{"/bin/" + cfg.Shell}, nil
}

// NewClient returns a Docker client configured from the environment.
func NewClient() (client.APIClient, error) {
	return client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
}

// ContainerNameFor builds the container name for a given workspace path.
// Format: toolbox-<basename>-<hash8>. The hash is over the absolute path so
// that two directories sharing the same basename do not collide.
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

	return containerNamePrefix + base + "-" + hash
}

// Shell manages the container lifecycle and attaches a bash session.
// The workspace host path is always mounted at /workspace and used as the
// WorkingDir. When the attached shell exits the container is stopped and
// removed — all persistent state lives on bind-mounted volumes under
// ~/.toolbox/ (creds, bash history, caches), so nothing is lost by trashing
// the container itself.
//
// State machine:
//   - running   -> exec directly (recover from a concurrent shell / crash)
//   - stopped   -> start + exec (same)
//   - not found -> ensure image, create + start + exec (common path)
//
// Image ensure strategy (see build.ResolveImage):
//   - defaults config  -> pull the canonical GHCR image (best-effort)
//   - custom tools     -> auto-build a hash-tagged local image if missing
//
// Multi-session caveat: if two terminals open a shell into the same
// workspace, both attach to the same container. When either exits the
// container is removed and the other session dies with it.
func Shell(ctx context.Context, cli client.APIClient, cfg *config.Config, workspace string, publish []string) (err error) {
	// Normalize once so every downstream consumer (container name, bind
	// sources, env vars) sees the same absolute, cleaned path. Callers
	// already do this, but defensive normalization keeps bind source and
	// mirror target identical regardless of input quirks.
	if abs, absErr := filepath.Abs(workspace); absErr == nil {
		workspace = filepath.Clean(abs)
	}
	name := ContainerNameFor(workspace)

	exposed, bindings, parseErr := parsePublishSpecs(publish)
	if parseErr != nil {
		return parseErr
	}

	// SHELL-02 + SHELL-03: resolve the shell BEFORE any Docker work so an
	// incoherent config (shell: zsh + tools.zsh: false) exits early with a
	// clear message and no container/image side-effects (D-17, D-18).
	shellCmd, resolveErr := ResolveShellCmd(cfg)
	if resolveErr != nil {
		fmt.Fprintln(os.Stderr, resolveErr)
		return resolveErr
	}

	ref, isLocal := build.ResolveImage(cfg, version.Version)

	if !isLocal {
		// Canonical registry image: refresh on every shell, best-effort.
		pullImage(ctx, cli, ref)
	}

	binds, warnings := mount.ResolveMounts(cfg.Mounts)
	for _, w := range warnings {
		ui.Warning("mount skipped: " + w)
	}

	// Workspace mount is always enabled: host CWD -> /workspace.
	binds = append(binds, workspace+":"+WorkspaceTarget+":rw")

	// Mirror the workspace at its own absolute host path when safe, so that
	// $PWD-based bind mounts issued from inside the shell (DooD) pass a path
	// the host daemon can resolve. WorkingDir falls back to WorkspaceTarget
	// when the host path would shadow a reserved container directory.
	workingDir := WorkspaceTarget
	if mirror, ok := workspaceMirrorPath(workspace); ok {
		binds = append(binds, workspace+":"+mirror+":rw")
		workingDir = mirror
	}

	inspect, inspectErr := cli.ContainerInspect(ctx, name)

	// Docker SDK guarantees inspect.State is non-nil on success, but mocks
	// and unexpected daemon edge cases could violate that — treat a nil
	// State as "not running" instead of panicking on the dereference.
	running := inspectErr == nil && inspect.State != nil && inspect.State.Running

	if inspectErr == nil && len(bindings) > 0 {
		warnMissingPublish(inspect, bindings)
	}

	var containerID string
	switch {
	case inspectErr == nil && running:
		ui.Info("Connecting to running container " + name + "...")
		containerID = inspect.ID

	case inspectErr == nil && !running:
		ui.Info("Starting stopped container " + name + "...")
		if startErr := cli.ContainerStart(ctx, inspect.ID, container.StartOptions{}); startErr != nil {
			return fmt.Errorf("failed to start container: %w", startErr)
		}
		containerID = inspect.ID

	case cerrdefs.IsNotFound(inspectErr):
		if ensureErr := ensureImage(ctx, cli, cfg, ref, isLocal); ensureErr != nil {
			return ensureErr
		}

		ui.Info("Creating container " + name + "...")
		resp, createErr := cli.ContainerCreate(ctx,
			&container.Config{
				Image:        ref,
				Tty:          true,
				OpenStdin:    true,
				Cmd:          shellCmd,
				WorkingDir:   workingDir,
				User:         hostUserSpec(),
				ExposedPorts: exposed,
				Env:          shellEnv(workspace, workingDir),
			},
			&container.HostConfig{
				Binds:        binds,
				GroupAdd:     dockerSockGroups(binds),
				PortBindings: bindings,
			},
			nil, // network config
			nil, // platform
			name,
		)
		if createErr != nil {
			return fmt.Errorf("failed to create container: %w", createErr)
		}

		if startErr := cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); startErr != nil {
			return fmt.Errorf("failed to start container: %w", startErr)
		}
		ui.Success("Container started")
		containerID = resp.ID

	default:
		return fmt.Errorf("failed to inspect container: %w", inspectErr)
	}

	// Auto-remove on exit, unless another shell is still attached to the
	// same container (same workspace opened in multiple terminals). In that
	// case leaving the container running keeps the sibling sessions alive.
	// Use a fresh context (with a bounded timeout) so cleanup still runs if
	// the shell exited because ctx was cancelled (Ctrl+C), but a frozen
	// Docker daemon can't hang the CLI. The shell's own exit error wins
	// over any cleanup error — a failed stop is noisy, not fatal.
	defer func() {
		cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancelCleanup()
		if hasActiveExecs(cleanupCtx, cli, name) {
			ui.Info("Container " + name + " still has active sessions — leaving it running")
			return
		}
		if stopErr := stopOne(cleanupCtx, cli, name); stopErr != nil && err == nil {
			err = stopErr
		}
	}()

	return execShellFn(ctx, cli, cfg, containerID)
}

// ensureImage guarantees the image referenced by ref exists locally.
//   - registry tags: a failed pull already logged a warning; error now if the
//     image is still absent.
//   - local hash tags: auto-build from the embedded context using the config's
//     tools map to derive the INSTALL_* build args.
var ensureImage = func(ctx context.Context, cli client.APIClient, cfg *config.Config, ref string, isLocal bool) error {
	if _, err := cli.ImageInspect(ctx, ref); err == nil {
		return nil
	}

	if !isLocal {
		return fmt.Errorf("image %q not available locally and pull failed — check registry access", ref)
	}

	ui.Info("Image not found locally — building " + ref + " for current tools config...")
	return build.BuildImage(ctx, cli, build.Options{
		Tag:       ref,
		BuildArgs: build.BuildArgsFromTools(cfg.Tools),
	})
}

// hostUserSpec returns the "<uid>:<gid>" of the user invoking toolbox, so the
// container runs with the host user's identity. This keeps bind-mounted files
// (Claude credentials, ssh keys) readable/writable without uid mismatch.
func hostUserSpec() string {
	return fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
}

// dockerSockGroups returns supplementary group IDs to grant the runtime user
// access to /var/run/docker.sock when it is bind-mounted. Without this, a
// container running as the host UID cannot talk to the Docker API because the
// socket is group-owned (typically mode 660).
//
// Two GIDs are added to cover both deployment modes:
//   - "0" (root): Docker Desktop on macOS/Windows reprojects the socket as
//     root:root inside the container regardless of host ownership.
//   - host sock GID: on Linux the socket keeps the host group (usually
//     "docker"), so the container must join that GID.
//
// Returns nil when docker.sock is not in binds — don't grant extra groups
// users didn't ask for.
func dockerSockGroups(binds []string) []string {
	const sockPath = "/var/run/docker.sock"

	mounted := false
	for _, b := range binds {
		// Bind format: "<source>:<target>[:<opts>]". Match on target.
		parts := strings.SplitN(b, ":", 3)
		if len(parts) >= 2 && parts[1] == sockPath {
			mounted = true
			break
		}
	}
	if !mounted {
		return nil
	}

	groups := []string{"0"}
	if gid, ok := statSockGID(sockPath); ok && gid != 0 {
		groups = append(groups, fmt.Sprintf("%d", gid))
	}
	return groups
}

// statSockGID returns the GID owning the given path on the host, following
// symlinks. Returns (0, false) on any error — the caller falls back to gid 0.
var statSockGID = func(path string) (uint32, bool) {
	info, err := os.Stat(path) // Stat follows symlinks; docker.sock is often a symlink on macOS.
	if err != nil {
		return 0, false
	}
	sys, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return sys.Gid, true
}

// pullImage attempts to pull the image from its remote registry. Errors are
// logged as warnings and swallowed: the caller proceeds with the local image.
// The pull stream is rendered with per-layer progress bars on a TTY, or as
// plain status lines otherwise — the caller gets real-time feedback instead
// of a silent hang while layers download.
func pullImage(ctx context.Context, cli client.APIClient, ref string) {
	ui.Info("Checking for image updates: " + ref + "...")
	rc, err := cli.ImagePull(ctx, ref, image.PullOptions{})
	if err != nil {
		ui.Warning("image pull failed, using local image if present: " + err.Error())
		return
	}
	defer rc.Close()

	fd := os.Stdout.Fd()
	isTerm := term.IsTerminal(int(fd))
	if err := jsonmessage.DisplayJSONMessagesStream(rc, os.Stdout, fd, isTerm, nil); err != nil {
		ui.Warning("image pull stream error, using local image if present: " + err.Error())
		return
	}
	ui.Success("Image up to date: " + ref)
}

// Stop stops and removes the toolbox container associated with the workspace.
func Stop(ctx context.Context, cli client.APIClient, workspace string) error {
	return stopOne(ctx, cli, ContainerNameFor(workspace))
}

// StopAll stops and removes every toolbox-managed container on the host.
// Matches the "toolbox-" prefix as well as the legacy singleton name "toolbox".
// Failures on a single container don't short-circuit the rest — partial
// cleanup beats fail-fast when `--all` is meant to be a bulk sweep.
func StopAll(ctx context.Context, cli client.APIClient) error {
	args := filters.NewArgs(filters.Arg("name", "toolbox"))
	list, err := cli.ContainerList(ctx, container.ListOptions{All: true, Filters: args})
	if err != nil {
		return fmt.Errorf("failed to list containers: %w", err)
	}

	found := 0
	var errs []error
	for _, c := range list {
		if len(c.Names) == 0 {
			continue
		}
		name := strings.TrimPrefix(c.Names[0], "/")
		if name != "toolbox" && !strings.HasPrefix(name, containerNamePrefix) {
			continue
		}
		if err := stopOne(ctx, cli, name); err != nil {
			errs = append(errs, err)
			continue
		}
		found++
	}
	if found == 0 && len(errs) == 0 {
		ui.Warning("No toolbox containers found")
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

func stopOne(ctx context.Context, cli client.APIClient, name string) error {
	timeout := 10
	stopErr := cli.ContainerStop(ctx, name, container.StopOptions{Timeout: &timeout})

	if cerrdefs.IsNotFound(stopErr) {
		ui.Warning("Container " + name + " not found")
		return nil
	}
	if stopErr != nil {
		return fmt.Errorf("failed to stop container %s: %w", name, stopErr)
	}

	rmErr := cli.ContainerRemove(ctx, name, container.RemoveOptions{Force: true})
	if rmErr != nil && !cerrdefs.IsNotFound(rmErr) {
		return fmt.Errorf("failed to remove container %s: %w", name, rmErr)
	}

	ui.Success("Container " + name + " stopped and removed")
	return nil
}

// shellEnv returns the env vars injected into every shell spawned by the
// container. TOOLBOX_HOST_WORKSPACE holds the absolute host path mounted at
// /workspace so that Makefiles and compose files can pass a host-resolvable
// path to `docker run -v` under the bind-mounted socket (DooD): a literal
// "/workspace/foo" is meaningless to the host daemon. PWD is set explicitly
// to workingDir so that scripts reading $PWD directly (without a getcwd
// fallback) see the same path bash exposes after starting in WorkingDir.
func shellEnv(workspace, workingDir string) []string {
	return []string{
		"TOOLBOX_HOST_WORKSPACE=" + workspace,
		"PWD=" + workingDir,
	}
}

// workspaceMirrorPath returns the in-container path at which the workspace
// should be mirrored in addition to WorkspaceTarget, plus true when the
// mirror is safe to create. The mirror is the workspace's own absolute host
// path, which makes `$PWD` inside the shell match what the host daemon sees
// — the key ingredient for DooD bind mounts to resolve without rewriting.
//
// The mirror is skipped when:
//   - the path is empty, relative, or equal to the filesystem root;
//   - the path equals WorkspaceTarget (already mounted there);
//   - the path would shadow a reserved container directory (see
//     reservedMirrorPrefixes) — e.g. /home/toolbox, /usr, /etc.
func workspaceMirrorPath(workspace string) (string, bool) {
	if workspace == "" || !filepath.IsAbs(workspace) {
		return "", false
	}
	abs := filepath.Clean(workspace)
	if abs == "/" || abs == WorkspaceTarget {
		return "", false
	}
	for _, r := range reservedMirrorPrefixes {
		if abs == r || strings.HasPrefix(abs, r+"/") {
			return "", false
		}
	}
	return abs, true
}

// hasActiveExecs reports whether the named container has any still-running
// exec session. Called on shell exit: the invoking exec has already drained
// (io.Copy returned) and is Running:false by the time this runs, so it does
// not self-report. A true result means a sibling terminal is still attached,
// and stopping the container would kill it — the caller skips teardown.
// Inspect errors are treated as "no active execs" so a transient daemon
// hiccup does not strand a container that nobody will ever clean up.
func hasActiveExecs(ctx context.Context, cli client.APIClient, name string) bool {
	inspect, err := cli.ContainerInspect(ctx, name)
	if err != nil {
		return false
	}
	if inspect.ContainerJSONBase == nil {
		return false
	}
	for _, execID := range inspect.ExecIDs {
		exec, err := cli.ContainerExecInspect(ctx, execID)
		if err != nil {
			continue
		}
		if exec.Running {
			return true
		}
	}
	return false
}

// parsePublishSpecs parses "docker run -p"-style publish specs into Docker's
// ExposedPorts + PortBindings. Defaults the host IP to 127.0.0.1 (not 0.0.0.0)
// so OAuth callbacks stay loopback-only instead of being exposed to the LAN.
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

// missingPublishPorts returns the wanted ports that the existing container was
// not created with. PortBindings are fixed at create time, so "--publish" on a
// reused container is a silent no-op for any port not in this list.
func missingPublishPorts(inspect container.InspectResponse, wanted nat.PortMap) []string {
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

func warnMissingPublish(inspect container.InspectResponse, wanted nat.PortMap) {
	missing := missingPublishPorts(inspect, wanted)
	if len(missing) == 0 {
		return
	}
	ui.Warning(fmt.Sprintf(
		"existing container has no publish for %s — run 'toolbox stop' then retry to apply",
		strings.Join(missing, ", "),
	))
}
