// Package container owns the toolbox-container lifecycle: ensuring the
// image, creating/starting/stopping the per-workspace container, and
// attaching an interactive shell. Public seams: Shell, Stop, StopAll,
// NewClient. The Shell signature is (ctx, cli, *sessionplan.SessionPlan)
// — every input that used to be parsed inline (publish specs, image
// resolution, mount plan, container name, env, security opts) now rides
// in the typed plan composed by internal/sessionplan.Plan.
//
// ResolveShellCmd / NestedSandboxSecurityOpt / ShellMismatchError live
// in internal/shellcmd (cycle-breaker so sessionplan can compose them).
//
// The orchestration Module lives in lifecycle.go (this file), sectioned
// into Lifecycle and Cleanup helpers. The TTY/signal Adapter lives in
// attach.go and is kept as a separate file because its concern (raw
// mode, signal forwarding) is independent from Docker SDK orchestration.
package container

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/jsonmessage"
	"golang.org/x/term"

	"github.com/filippolmt/toolbox/internal/build"
	"github.com/filippolmt/toolbox/internal/sessionplan"
	"github.com/filippolmt/toolbox/internal/ui"
)

// cleanupTimeout bounds the best-effort container stop+remove that runs on
// exit with a fresh context. Sized to absorb a slow Docker daemon while
// keeping the user-visible exit prompt-to-prompt latency low: stopOne sets
// a 2s SIGTERM grace (see stopShellGrace), remove is sub-second, so 30s is
// pure margin for the daemon socket itself.
const cleanupTimeout = 30 * time.Second

// stopShellGrace is the SIGTERM grace period passed to ContainerStop on
// shell-exit teardown. Kept short because the current image's PID 1 child
// is `sleep infinity` (it terminates instantly on SIGTERM) and persistent
// state lives on bind mounts — nothing to flush. On older images that
// still ship `CMD ["zsh"]`, interactive zsh ignores SIGTERM and Docker
// falls back to SIGKILL after this grace; the user-visible delta is
// "2s tail" instead of the prior 10s, which is the worst case we accept.
const stopShellGrace = 2

// execShellFn attaches an interactive shell to a container.
// Exposed as a package-level var so tests can substitute it.
var execShellFn = execShell

// formatPublishMismatch builds the warning string emitted when a reused
// container does not have every port the user asked for. Returns "" when
// every wanted port is already bound on the existing container, signalling
// the caller to stay quiet. sessionplan.MissingPublishPorts produces the
// typed missing-list; lifecycle composes the human-readable message.
func formatPublishMismatch(plan *sessionplan.SessionPlan, inspect container.InspectResponse, missing []string) string {
	if len(missing) == 0 {
		return ""
	}
	sort.Strings(missing)

	wantedPorts := make([]string, 0, len(plan.PortBindings))
	for port := range plan.PortBindings {
		wantedPorts = append(wantedPorts, string(port))
	}
	sort.Strings(wantedPorts)

	actual := []string{}
	if inspect.HostConfig != nil {
		for port := range inspect.HostConfig.PortBindings {
			actual = append(actual, string(port))
		}
		sort.Strings(actual)
	}
	actualMsg := "none"
	if len(actual) > 0 {
		actualMsg = strings.Join(actual, ", ")
	}

	return fmt.Sprintf(
		"publish mismatch on existing container: wanted [%s], container has [%s], missing [%s] — run 'toolbox stop' then retry to apply",
		strings.Join(wantedPorts, ", "), actualMsg, strings.Join(missing, ", "),
	)
}

// --- Lifecycle ---

// NewClient returns a Docker client configured from the environment.
func NewClient() (client.APIClient, error) {
	return client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
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
func Shell(ctx context.Context, cli client.APIClient, plan *sessionplan.SessionPlan) (err error) {
	for _, w := range plan.Warnings {
		ui.Warning("mount skipped: " + w)
	}

	if !plan.Image.IsLocal {
		// Canonical registry image: refresh on every shell, best-effort —
		// but skip the manifest round-trip when we already pulled the same
		// ref within pullCacheTTL. The cache lives on disk under
		// ~/.toolbox/state/pull-cache/ so it survives across CLI runs; only
		// successful pulls record, so a network blip doesn't poison the
		// next invocation into staleness. See pullCached/recordPull.
		if !pullCached(plan.Image.Ref) {
			if pullImage(ctx, cli, plan.Image.Ref) {
				recordPull(plan.Image.Ref)
			}
		}
	}

	inspect, inspectErr := cli.ContainerInspect(ctx, plan.ContainerName)

	// inspect.State, inspect.ID, and friends are promoted fields through the
	// embedded *ContainerJSONBase pointer, so a nil ContainerJSONBase panics
	// before any `!= nil` check on the inner field ever runs. Guard the
	// embedded pointer first. Daemon protocol and the happy-path SDK contract
	// make ContainerJSONBase non-nil on success, but mocks, future SDK shape
	// changes, and unexpected daemon edge cases can violate that. A nil base
	// means we have no usable container record (no ID to start, no State to
	// inspect) — treat it as the not-found path so Shell creates a fresh
	// container instead of dereferencing a nil pointer.
	hasInspectData := inspectErr == nil && inspect.ContainerJSONBase != nil
	running := hasInspectData && inspect.State != nil && inspect.State.Running

	if hasInspectData && len(plan.PortBindings) > 0 {
		if missing := sessionplan.MissingPublishPorts(plan.PortBindings, inspect); len(missing) > 0 {
			ui.Warning(formatPublishMismatch(plan, inspect, missing))
		}
	}

	var containerID string
	switch {
	case hasInspectData && running:
		ui.Info("Connecting to running container " + plan.ContainerName + "...")
		containerID = inspect.ID

	case hasInspectData && !running:
		ui.Info("Starting stopped container " + plan.ContainerName + "...")
		if startErr := cli.ContainerStart(ctx, inspect.ID, container.StartOptions{}); startErr != nil {
			return fmt.Errorf("failed to start container: %w", startErr)
		}
		containerID = inspect.ID

	case inspectErr == nil || cerrdefs.IsNotFound(inspectErr):
		if ensureErr := ensureImage(ctx, cli, plan.Image.Ref, plan.Image.IsLocal, plan.BuildArgs); ensureErr != nil {
			return ensureErr
		}

		binds := make([]string, len(plan.Binds))
		for i, b := range plan.Binds {
			binds[i] = b.String()
		}

		ui.Info("Creating container " + plan.ContainerName + "...")
		resp, createErr := cli.ContainerCreate(ctx,
			&container.Config{
				Image:        plan.Image.Ref,
				Tty:          true,
				OpenStdin:    true,
				Cmd:          plan.Cmd,
				WorkingDir:   plan.WorkingDir,
				User:         hostUserSpec(),
				ExposedPorts: plan.ExposedPorts,
				Env:          plan.Env,
			},
			&container.HostConfig{
				Binds:        binds,
				GroupAdd:     dockerSockGroups(binds),
				PortBindings: plan.PortBindings,
				SecurityOpt:  plan.SecurityOpt,
			},
			nil, // network config
			nil, // platform
			plan.ContainerName,
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
		if hasActiveExecs(cleanupCtx, cli, plan.ContainerName) {
			ui.Info("Container " + plan.ContainerName + " still has active sessions — leaving it running")
			return
		}
		if stopErr := stopOne(cleanupCtx, cli, plan.ContainerName); stopErr != nil && err == nil {
			err = stopErr
		}
	}()

	return execShellFn(ctx, cli, containerID, plan.Cmd)
}

// Stop stops and removes the toolbox container associated with the workspace.
func Stop(ctx context.Context, cli client.APIClient, workspace string) error {
	return stopOne(ctx, cli, sessionplan.ContainerNameFor(workspace))
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
		if name != "toolbox" && !strings.HasPrefix(name, sessionplan.ContainerNamePrefix) {
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

// --- Cleanup helpers ---

// ensureImage guarantees the image referenced by ref exists locally.
//   - registry tags: a failed pull already logged a warning; error now if the
//     image is still absent.
//   - local hash tags: auto-build from the embedded context using the config's
//     tools map to derive the INSTALL_* build args.
var ensureImage = func(ctx context.Context, cli client.APIClient, ref string, isLocal bool, buildArgs map[string]*string) error {
	if _, err := cli.ImageInspect(ctx, ref); err == nil {
		return nil
	}

	if !isLocal {
		return fmt.Errorf("image %q not available locally and pull failed — check registry access", ref)
	}

	ui.Info("Image not found locally — building " + ref + " for current tools config...")
	return build.BuildImage(ctx, cli, build.Options{
		Tag:       ref,
		BuildArgs: buildArgs,
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
// of a silent hang while layers download. Returns true on a clean pull so
// the caller can record it in the pull cache; returns false on any failure
// path so a poisoned cache never silently masks broken connectivity.
func pullImage(ctx context.Context, cli client.APIClient, ref string) bool {
	ui.Info("Checking for image updates: " + ref + "...")
	rc, err := cli.ImagePull(ctx, ref, image.PullOptions{})
	if err != nil {
		ui.Warning("image pull failed, using local image if present: " + err.Error())
		return false
	}
	defer rc.Close()

	// Pull progress is diagnostic; keep stdout clean for program output.
	fd := os.Stderr.Fd()
	isTerm := term.IsTerminal(int(fd))
	if err := jsonmessage.DisplayJSONMessagesStream(rc, os.Stderr, fd, isTerm, nil); err != nil {
		ui.Warning("image pull stream error, using local image if present: " + err.Error())
		return false
	}
	ui.Success("Image up to date: " + ref)
	return true
}

// pullCacheTTL bounds how long we trust a previous successful manifest check
// before re-asking the registry. One hour is short enough that a freshly
// pushed image lands on developer machines within the same work block, and
// long enough that rapid `toolbox shell` cycles (open → exit → open) don't
// each pay a round-trip to GHCR. Override is intentional fs-only: delete
// ~/.toolbox/state/pull-cache/* to force a fresh pull on next invocation.
const pullCacheTTL = 1 * time.Hour

// pullCachePath returns the cache marker path for a given image ref. The
// ref is hashed because tags can contain characters that are awkward in
// filenames (digests, registry paths with ":" / "/"). os.UserHomeDir errors
// are surfaced so the caller treats them as "no cache" rather than writing
// to an unexpected location.
func pullCachePath(ref string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(ref))
	return filepath.Join(home, ".toolbox", "state", "pull-cache", hex.EncodeToString(sum[:])), nil
}

// pullCached reports whether a successful pull of ref happened within the
// last pullCacheTTL. Any error (no home dir, missing marker, stat failure)
// returns false so the caller falls through to a real pull — never silently
// skip on uncertainty.
func pullCached(ref string) bool {
	path, err := pullCachePath(ref)
	if err != nil {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return time.Since(info.ModTime()) < pullCacheTTL
}

// recordPull stamps a fresh marker after a successful pull. Best-effort:
// any error (no home dir, mkdir/write failure) leaves the cache empty, so
// the next invocation just pulls again. Marker contents are intentionally
// empty — modtime is the timestamp.
func recordPull(ref string) {
	path, err := pullCachePath(ref)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(path, nil, 0o644)
}

func stopOne(ctx context.Context, cli client.APIClient, name string) error {
	timeout := stopShellGrace
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
