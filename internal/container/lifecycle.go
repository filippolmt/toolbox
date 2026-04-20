package container

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/jsonmessage"
	"golang.org/x/term"

	"github.com/filippolmt/toolbox/internal/build"
	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/mount"
	"github.com/filippolmt/toolbox/internal/ui"
	"github.com/filippolmt/toolbox/internal/version"
)

// WorkspaceTarget is the fixed in-container path where the host CWD is mounted.
const WorkspaceTarget = "/workspace"

// containerNamePrefix identifies containers managed by toolbox.
const containerNamePrefix = "toolbox-"

var sanitizeRe = regexp.MustCompile(`[^a-z0-9]+`)

// execShellFn attaches an interactive shell to a container.
// Exposed as a package-level var so tests can substitute it.
var execShellFn = execShell

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
func Shell(ctx context.Context, cli client.APIClient, cfg *config.Config, workspace string) (err error) {
	name := ContainerNameFor(workspace)

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

	inspect, inspectErr := cli.ContainerInspect(ctx, name)

	var containerID string
	switch {
	case inspectErr == nil && inspect.State.Running:
		ui.Info("Connecting to running container " + name + "...")
		containerID = inspect.ID

	case inspectErr == nil && !inspect.State.Running:
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
				Image:      ref,
				Tty:        true,
				OpenStdin:  true,
				Cmd:        []string{"/bin/bash"},
				WorkingDir: WorkspaceTarget,
				User:       hostUserSpec(),
			},
			&container.HostConfig{
				Binds:    binds,
				GroupAdd: dockerSockGroups(binds),
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

	// Auto-remove on exit. Use a fresh context so cleanup still runs if the
	// shell exited because ctx was cancelled (Ctrl+C). The shell's own exit
	// error wins over any cleanup error — a failed stop is noisy, not fatal.
	defer func() {
		if stopErr := stopOne(context.Background(), cli, name); stopErr != nil && err == nil {
			err = stopErr
		}
	}()

	return execShellFn(ctx, cli, containerID)
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
func StopAll(ctx context.Context, cli client.APIClient) error {
	args := filters.NewArgs(filters.Arg("name", "toolbox"))
	list, err := cli.ContainerList(ctx, container.ListOptions{All: true, Filters: args})
	if err != nil {
		return fmt.Errorf("failed to list containers: %w", err)
	}

	found := 0
	for _, c := range list {
		if len(c.Names) == 0 {
			continue
		}
		name := strings.TrimPrefix(c.Names[0], "/")
		if name != "toolbox" && !strings.HasPrefix(name, containerNamePrefix) {
			continue
		}
		if err := stopOne(ctx, cli, name); err != nil {
			return err
		}
		found++
	}
	if found == 0 {
		ui.Warning("No toolbox containers found")
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
