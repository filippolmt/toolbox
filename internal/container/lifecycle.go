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
// WorkingDir.
// State machine:
//   - running   -> exec directly (no container created)
//   - stopped   -> start + exec
//   - not found -> ensure image, create + start + exec
//
// Image ensure strategy (see build.ResolveImage):
//   - defaults config  -> pull the canonical GHCR image (best-effort)
//   - custom tools     -> auto-build a hash-tagged local image if missing
func Shell(ctx context.Context, cli client.APIClient, cfg *config.Config, workspace string) error {
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

	inspect, err := cli.ContainerInspect(ctx, name)

	switch {
	case err == nil && inspect.State.Running:
		ui.Info("Connecting to running container " + name + "...")
		return execShellFn(ctx, cli, inspect.ID)

	case err == nil && !inspect.State.Running:
		ui.Info("Starting stopped container " + name + "...")
		if startErr := cli.ContainerStart(ctx, inspect.ID, container.StartOptions{}); startErr != nil {
			return fmt.Errorf("failed to start container: %w", startErr)
		}
		return execShellFn(ctx, cli, inspect.ID)

	case cerrdefs.IsNotFound(err):
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
				Binds: binds,
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
		return execShellFn(ctx, cli, resp.ID)

	default:
		return fmt.Errorf("failed to inspect container: %w", err)
	}
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
