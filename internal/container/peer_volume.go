package container

import (
	"context"
	"fmt"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/dockeridentity"
	"github.com/filippolmt/toolbox/internal/mountplan"
	"github.com/filippolmt/toolbox/internal/sessionplan"
	"github.com/filippolmt/toolbox/internal/ui"
)

// peerSocketInitContainerName is the throwaway container that takes ownership
// of a freshly created socket volume. It carries the ContainerNamePrefix for
// the same reason the anchor does: one that outlives its own cleanup — a
// daemon restart mid-init — holds the volume in use, and the prefix is what
// lets `toolbox list` show it and `toolbox stop --all` sweep it up.
const peerSocketInitContainerName = sessionplan.ContainerNamePrefix + "cc-socks-init"

// ensurePeerSocketVolume makes the shared inbox-socket volume exist and carry
// the ownership Claude Code requires, so an opted-in session finds a directory
// it can bind sockets in.
//
// The init is tied to the volume's creation rather than run on every shell: a
// volume that already exists went through this same path, and confirming it
// otherwise would cost a container start per session. The tradeoff only holds
// because a failed init removes the volume again — see below.
func ensurePeerSocketVolume(ctx context.Context, cli client.APIClient, image sessionplan.Image) error {
	name := mountplan.PeerSocketVolumeName

	switch _, err := cli.VolumeInspect(ctx, name, client.VolumeInspectOptions{}); {
	case err == nil:
		return nil
	case !cerrdefs.IsNotFound(err):
		// Anything but a clean "no such volume" leaves the volume's existence
		// unknown, and guessing "absent" is the dangerous guess: VolumeCreate
		// returns the *existing* volume, so a failing init below would then
		// force-remove one live sessions are binding sockets in.
		return fmt.Errorf("failed to inspect peer socket volume %s: %w", name, err)
	}

	ui.Info("Creating peer-messaging socket volume " + name + "...")
	if _, err := cli.VolumeCreate(ctx, client.VolumeCreateOptions{Name: name}); err != nil {
		return fmt.Errorf("failed to create peer socket volume: %w", err)
	}

	if err := initPeerSocketVolume(ctx, cli, image, name); err != nil {
		// A volume left behind root-owned would satisfy the VolumeInspect above
		// on every later shell, so the init would never run again and each
		// session would fail its bind instead — silently, which is the failure
		// mode this whole subsystem is written to avoid. Removing it keeps the
		// next shell on the same self-healing path.
		if _, rmErr := cli.VolumeRemove(ctx, name, client.VolumeRemoveOptions{Force: true}); rmErr != nil {
			return fmt.Errorf("%w — and %s could not be removed (%v), so every later shell would reuse it "+
				"as-is: remove it with `docker volume rm %s` once no participating shell is running", err, name, rmErr, name)
		}
		return err
	}
	return nil
}

// initPeerSocketVolume chowns the freshly created volume to the host UID/GID
// and tightens it to 0700, in a throwaway container that runs as root.
//
// Root is the point: a Docker volume is created root-owned, the session
// container runs as the unprivileged host UID (see the host-UID mapping), and
// nothing in a bind spec can hand over ownership. 0700 matters just as much as
// the owner — Claude Code answers a looser directory by falling back to
// /tmp/cc-socks-<uid>, without saying so, which leaves every peer alone in a
// private directory.
//
// It runs the toolbox runtime image, already guaranteed present locally on
// this path by imageplan.Ensure, with the image's own entrypoint overridden:
// none of the shell-start init belongs in a container that only fixes a mode
// bit.
func initPeerSocketVolume(ctx context.Context, cli client.APIClient, image sessionplan.Image, volume string) error {
	target := mountplan.PeerSocketDirTarget
	// Resolve(nil) reads only os.Getuid/os.Getgid; GroupAdd is bind-derived and
	// this container mounts nothing but the volume.
	owner := dockeridentity.Resolve(nil).UserSpec

	created, err := cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: peerSocketInitContainerName,
		Config: &container.Config{
			Image:      image.Ref,
			User:       "0:0",
			Entrypoint: []string{"sh", "-c"},
			Cmd:        []string{fmt.Sprintf("chown %s %s && chmod 0700 %s", owner, target, target)},
		},
		HostConfig: &container.HostConfig{
			Binds: []string{volume + ":" + target},
			// Not AutoRemove: the exit status is the only report this container
			// produces, and the daemon may reap an auto-removed container before
			// ContainerWait can read it.
			AutoRemove: false,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create peer socket volume initialiser: %w", err)
	}
	defer func() {
		// WithoutCancel: a Ctrl-C reaches this defer through a cancelled ctx,
		// and passing that straight on would leave the container alive — which
		// keeps the volume in use, so the rollback in ensurePeerSocketVolume
		// could not remove it either, and the name above would collide on the
		// next shell.
		_, _ = cli.ContainerRemove(context.WithoutCancel(ctx), created.ID, client.ContainerRemoveOptions{Force: true})
	}()

	// Wait before Start: ContainerWait blocks until the daemon acknowledges the
	// request, so subscribing first cannot miss the exit of a container this
	// short-lived.
	wait := cli.ContainerWait(ctx, created.ID, client.ContainerWaitOptions{
		Condition: container.WaitConditionNotRunning,
	})
	if _, startErr := cli.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); startErr != nil {
		return fmt.Errorf("failed to start peer socket volume initialiser: %w", startErr)
	}

	select {
	case waitErr := <-wait.Error:
		return fmt.Errorf("failed to wait for peer socket volume initialiser: %w", waitErr)
	case res := <-wait.Result:
		if res.StatusCode != 0 {
			return fmt.Errorf("peer socket volume initialiser exited %d: could not take ownership of %s", res.StatusCode, target)
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("peer socket volume init interrupted: %w", ctx.Err())
	}
}
