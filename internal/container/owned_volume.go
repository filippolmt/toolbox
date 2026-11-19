package container

import (
	"context"
	"fmt"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/dockeridentity"
	"github.com/filippolmt/toolbox/internal/sessionplan"
	"github.com/filippolmt/toolbox/internal/ui"
)

// ownedVolume is a Docker named volume that must belong to the session's
// runtime user before any session binds it: the peer inbox-socket volume, and
// each Orca Shell's data volume and Herdr Core Shell's state volume. All exist because a Host Share bind cannot
// hold Unix sockets (see mountplan.PeerSocketVolumeName).
type ownedVolume struct {
	name, target string
	// initName is the throwaway initialiser container; see
	// peerSocketInitContainerName for why it carries the toolbox prefix.
	initName string
	// label names the volume in progress lines and errors.
	label string
}

// dataVolume is a singleton Shell's data volume, as sessionplan names it.
func dataVolume(plan *sessionplan.SessionPlan) ownedVolume {
	v := plan.DataVolume
	return ownedVolume{name: v.Name, target: v.Target, initName: v.Init, label: v.Label}
}

// ensureOwnedVolume makes v exist, owned by the runtime user with mode 0700.
//
// The init is tied to the volume's creation rather than run on every shell: a
// volume that already exists went through this same path, and confirming it
// otherwise would cost a container start per session. The tradeoff only holds
// because a failed init removes the volume again — see below.
func ensureOwnedVolume(ctx context.Context, cli client.APIClient, base sessionplan.Image, v ownedVolume) error {
	name := v.name

	switch _, err := cli.VolumeInspect(ctx, name, client.VolumeInspectOptions{}); {
	case err == nil:
		return nil
	case !cerrdefs.IsNotFound(err):
		// Anything but a clean "no such volume" leaves the volume's existence
		// unknown, and guessing "absent" is the dangerous guess: VolumeCreate
		// returns the *existing* volume, so a failing init below would then
		// force-remove one live sessions are using.
		return fmt.Errorf("failed to inspect %s %s: %w", v.label, name, err)
	}

	ui.Info("Creating " + v.label + " " + name + "...")
	if _, err := cli.VolumeCreate(ctx, client.VolumeCreateOptions{Name: name}); err != nil {
		return fmt.Errorf("failed to create %s: %w", v.label, err)
	}

	if err := initOwnedVolume(ctx, cli, base, v); err != nil {
		// A volume left behind root-owned would satisfy the VolumeInspect above
		// on every later shell, so the init would never run again and each
		// session would fail on it instead — silently, which is the failure
		// mode this whole subsystem is written to avoid. Removing it keeps the
		// next shell on the same self-healing path. Like the initialiser's
		// removal, it outlives a Ctrl-C under a deadline of its own.
		rbCtx, rbCancel := context.WithTimeout(context.WithoutCancel(ctx), ownedVolumeInitTimeout)
		defer rbCancel()
		if _, rmErr := cli.VolumeRemove(rbCtx, name, client.VolumeRemoveOptions{Force: true}); rmErr != nil {
			return fmt.Errorf("%w — and %s could not be removed (%v), so every later shell would reuse it "+
				"as-is: remove it with `docker volume rm %s` once no shell using it is running", err, name, rmErr, name)
		}
		return err
	}
	return nil
}

// ownedVolumeInitTimeout bounds the initialiser, and on their own its removal
// and the volume's rollback: a chown and chmod of an empty volume answer at
// once, so one still running is a stuck daemon. Tests swap it.
var ownedVolumeInitTimeout = time.Minute

// initOwnedVolume chowns the freshly created volume to the host UID/GID
// and tightens it to 0700, in a throwaway container that runs as root.
//
// Root is the point: a Docker volume is created root-owned, the session
// container runs as the unprivileged host UID (see the host-UID mapping), and
// nothing in a bind spec can hand over ownership. For the peer volume 0700
// matters just as much as the owner — Claude Code answers a looser directory
// by falling back to /tmp/cc-socks-<uid>, without saying so, which leaves
// every peer alone in a private directory.
//
// It runs the toolbox runtime image with that image's own entrypoint
// overridden: none of the shell-start init belongs in a container that only
// fixes a mode bit. Like the anchor, it takes the session's *base* ref and
// never the `:local` overlay — the volume it initialises outlives any one
// session, and see ensureAnchor for why the base is present locally by then.
func initOwnedVolume(ctx context.Context, cli client.APIClient, base sessionplan.Image, v ownedVolume) error {
	ctx, cancel := context.WithTimeout(ctx, ownedVolumeInitTimeout)
	defer cancel()
	target := v.target
	// Resolve(nil) reads only os.Getuid/os.Getgid; GroupAdd is bind-derived and
	// this container mounts nothing but the volume.
	owner := dockeridentity.Resolve(nil).UserSpec

	created, err := cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: v.initName,
		Config: &container.Config{
			Image:      base.Ref,
			User:       "0:0",
			Entrypoint: []string{"sh", "-c"},
			Cmd:        []string{fmt.Sprintf("chown %s %s && chmod 0700 %s", owner, target, target)},
		},
		HostConfig: &container.HostConfig{
			Binds: []string{v.name + ":" + target},
			// Not AutoRemove: the exit status is the only report this container
			// produces, and the daemon may reap an auto-removed container before
			// ContainerWait can read it.
			AutoRemove: false,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create %s initialiser: %w", v.label, err)
	}
	defer func() {
		// WithoutCancel: a Ctrl-C reaches this defer through a cancelled ctx,
		// and passing that straight on would leave the container alive — which
		// keeps the volume in use, so the rollback in ensureOwnedVolume
		// could not remove it either, and the name above would collide on the
		// next shell. WithoutCancel drops the deadline too, so the removal
		// takes one of its own: a stuck daemon must not hang the shell here.
		rmCtx, rmCancel := context.WithTimeout(context.WithoutCancel(ctx), ownedVolumeInitTimeout)
		defer rmCancel()
		_, _ = cli.ContainerRemove(rmCtx, created.ID, client.ContainerRemoveOptions{Force: true})
	}()

	// Wait before Start: ContainerWait blocks until the daemon acknowledges the
	// request, so subscribing first cannot miss the exit of a container this
	// short-lived. next-exit, not not-running: a created container is already
	// not running, so that wait answers 0 before the init runs, and the
	// deferred force-remove then kills the chown, leaving the volume root-owned.
	wait := cli.ContainerWait(ctx, created.ID, client.ContainerWaitOptions{
		Condition: container.WaitConditionNextExit,
	})
	if _, startErr := cli.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); startErr != nil {
		return fmt.Errorf("failed to start %s initialiser: %w", v.label, startErr)
	}

	select {
	case waitErr := <-wait.Error:
		return fmt.Errorf("failed to wait for %s initialiser: %w", v.label, waitErr)
	case res := <-wait.Result:
		if res.StatusCode != 0 {
			return fmt.Errorf("%s initialiser exited %d: could not take ownership of %s", v.label, res.StatusCode, target)
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("%s init interrupted: %w", v.label, ctx.Err())
	}
}
