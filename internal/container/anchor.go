package container

import (
	"context"
	"fmt"
	"slices"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/mountplan"
	"github.com/filippolmt/toolbox/internal/runplan"
	"github.com/filippolmt/toolbox/internal/sessionplan"
	"github.com/filippolmt/toolbox/internal/ui"
)

// peerWarnPrefix labels every warning this file emits about peer messaging, so
// the three degrade paths read as one subsystem in the terminal.
const peerWarnPrefix = "peer messaging: "

// tiniPath is the init the runtime image ships (Dockerfile ENTRYPOINT), reused
// here to give the anchor a PID 1 that reaps. See ensureAnchor.
const tiniPath = "/usr/bin/tini"

// anchorEntrypoint is the anchor's PID 1 and the payload it supervises.
// ContainerCreate writes it and isCurrentAnchor reads it back off the daemon,
// so the spec the connect path checks against cannot drift from the one the
// create path applies.
var anchorEntrypoint = []string{tiniPath, "-g", "--", "sleep"}

// ensureAnchor makes the peer-messaging anchor container exist and run, so
// opted-in sessions have a PID namespace to join. It reuses runplan.Compute
// for the connect / start / create branch — the same three-way decision the
// session container goes through, read off the same inspect snapshot.
//
// The anchor runs the toolbox runtime image (already guaranteed present
// locally by imageplan.Ensure on this path) rather than a second base image:
// the layers are shared, and there is no registry round-trip to fail on an
// offline host. Its entrypoint is overridden past the image's shell-start init
// — none of that belongs in a container that only holds a namespace — but NOT
// past tini: the anchor's PID 1 is PID 1 for every session that joins the
// namespace, and reaping orphans is PID 1's job. Under a bare `sleep`, which
// never calls wait(), every process reparented after its parent exits stays a
// zombie for the anchor's lifetime, one PID slot each, accumulated across every
// shell that ever shared it. The image's own tini cannot cover this from the
// session side: there it is not PID 1, and the baked ENTRYPOINT carries no -s,
// so it never registers as a subreaper. Verified with
// TestShellPeerAnchorReapsOrphans.
//
// An anchor created before this ran carries the old entrypoint, and reusing it
// would keep the reaper-less PID 1 forever. replaceStaleAnchor swaps it out
// whenever that can be done without collateral — see there for why "whenever"
// is not "always".
//
// AutoRemove is deliberately left off: the anchor outlives the sessions
// referencing it, which is the whole reason a session container cannot play
// this role.
func ensureAnchor(ctx context.Context, cli client.APIClient, image sessionplan.Image) error {
	name := sessionplan.PeerAnchorContainerName

	res, inspectErr := cli.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
	op, err := runplan.Compute(res.Container, inspectErr)
	if err != nil {
		return fmt.Errorf("failed to inspect peer anchor: %w", err)
	}

	if op.Action != runplan.ActionCreate && !isCurrentAnchor(res.Container) {
		replaced, replaceErr := replaceStaleAnchor(ctx, cli, res.Container, op.Action)
		if replaceErr != nil {
			return replaceErr
		}
		if replaced {
			op = runplan.Op{Action: runplan.ActionCreate}
		}
	}

	id := op.ExistingID
	switch op.Action {
	case runplan.ActionConnect:
		// Already holding the namespace — nothing to do.
		return nil
	case runplan.ActionCreate:
		ui.Info("Creating peer-messaging anchor " + name + "...")
		created, createErr := cli.ContainerCreate(ctx, client.ContainerCreateOptions{
			Name: name,
			Config: &container.Config{
				Image:      image.Ref,
				Entrypoint: anchorEntrypoint,
				Cmd:        []string{"infinity"},
			},
			HostConfig: &container.HostConfig{AutoRemove: false},
		})
		if createErr != nil {
			return fmt.Errorf("failed to create peer anchor: %w", createErr)
		}
		id = created.ID
	}
	// ActionCreate and ActionStart converge here: a freshly created anchor and
	// a stopped one both need the same start.
	if _, startErr := cli.ContainerStart(ctx, id, client.ContainerStartOptions{}); startErr != nil {
		return fmt.Errorf("failed to start peer anchor: %w", startErr)
	}
	return nil
}

// isCurrentAnchor reports whether an existing anchor was created with the
// entrypoint this build wants.
//
// The whole entrypoint is compared, not just tini: this is the general "the
// anchor predates the current spec" test, so a later change to the anchor's
// PID 1 inherits the replacement without a second check. A record carrying no
// Config counts as stale — an anchor whose spec cannot be read is one whose
// PID 1 cannot be vouched for.
func isCurrentAnchor(anchor container.InspectResponse) bool {
	return anchor.Config != nil && slices.Equal(anchor.Config.Entrypoint, anchorEntrypoint)
}

// replaceStaleAnchor removes an anchor that predates the current spec so the
// caller can create a fresh one, and reports whether it did.
//
// A stopped anchor is free to replace: every session that held its namespace
// died with it, so there is nothing left to break. A running one is the case
// Docker gives no help with — `docker rm -f` on an in-use anchor is not
// refused, it succeeds and leaves every session that held the namespace
// `exited` with 137 — so it is replaced only once anchorHeld can prove nobody
// is on it, and warned about otherwise.
func replaceStaleAnchor(ctx context.Context, cli client.APIClient, anchor container.InspectResponse, action runplan.Action) (bool, error) {
	name := sessionplan.PeerAnchorContainerName
	if action == runplan.ActionConnect && anchorHeld(ctx, cli, anchor.ID) {
		ui.Warning(peerWarnPrefix + name + " predates the reaping init, so orphaned processes in the " +
			"shared PID namespace stay as zombies — it is replaced automatically once no other toolbox " +
			"shell holds it, so exit them and start this shell again")
		return false, nil
	}
	ui.Info("Replacing peer-messaging anchor " + name + ": its PID 1 does not reap orphans...")
	if _, err := cli.ContainerRemove(ctx, anchor.ID, client.ContainerRemoveOptions{Force: true}); err != nil {
		return false, fmt.Errorf("failed to remove the reaper-less peer anchor: %w", err)
	}
	return true, nil
}

// anchorHeld reports whether any running toolbox container joined the anchor's
// PID namespace. It is the only thing standing between the self-heal above and
// killing a sibling shell, so every uncertainty answers "held": a list or
// inspect that fails leaves the anchor in place and the user with a warning,
// which costs one stale anchor — guessing the other way costs a live session.
//
// The PidMode is read per container rather than through samePidNamespace: the
// anchor's ID is already in hand here, and that helper would re-inspect the
// anchor once per candidate to rediscover it.
func anchorHeld(ctx context.Context, cli client.APIClient, anchorID string) bool {
	items, err := toolboxContainers(ctx, cli)
	if err != nil {
		return true
	}
	want := container.PidMode("container:" + anchorID)
	for _, c := range items {
		if c.ID == anchorID || c.State != container.StateRunning {
			continue
		}
		res, inspectErr := cli.ContainerInspect(ctx, c.ID, client.ContainerInspectOptions{})
		if inspectErr != nil {
			return true
		}
		if res.Container.HostConfig != nil && res.Container.HostConfig.PidMode == want {
			return true
		}
	}
	return false
}

// ensurePeerRuntime materialises both halves of peer messaging and returns
// what ContainerCreate needs: the PID namespace to join, and the bind set to
// create the container with.
//
// The daemon I/O is the reason this is not the pure sessionplan.peerPidMode.
// An unusable half degrades the session to a non-participating one — own
// namespace, socket mount dropped — with a warning rather than blocking the
// shell, the same posture the repo takes for a missing proximo stack. The
// shell still works; only peer messaging is gone.
//
// Both halves fall together on purpose. Half the mechanism is not half the
// feature: a session that mounts the shared socket dir but sits in its own PID
// namespace, or joins the namespace with a private socket dir, believes it is
// reachable and is not — the silent failure the whole subsystem is written to
// avoid.
func ensurePeerRuntime(ctx context.Context, cli client.APIClient, plan *sessionplan.SessionPlan) (string, []mountplan.Bind) {
	if plan.PidMode == "" {
		return "", plan.Binds
	}
	// Anchor first, then the volume: the anchor is the half a stale container
	// name can already be diagnosed against (peerMismatchWarning), so failing
	// on it first keeps the warning the user sees pointed at the same thing
	// across runs.
	err := ensureAnchor(ctx, cli, plan.Image)
	if err == nil {
		err = ensurePeerSocketVolume(ctx, cli, plan.Image)
	}
	if err != nil {
		ui.Warning(peerWarnPrefix + err.Error() + " — starting this shell without it")
		return "", mountplan.WithoutPeerSocketBind(plan.Binds)
	}
	return plan.PidMode, plan.Binds
}

// peerMismatchWarning covers the silent failures the container-name fold
// cannot: a container created while the anchor was unavailable carries no
// PidMode, and one created under an earlier opt-in carries a namespace the
// plan no longer asks for. Both look healthy on reattach — the first sees no
// peers, the second shares its process table with every opted-in shell.
// Returns "" when there is nothing to say.
func peerMismatchWarning(ctx context.Context, cli client.APIClient, plan *sessionplan.SessionPlan, inspect container.InspectResponse) string {
	var have string
	if inspect.HostConfig != nil {
		have = string(inspect.HostConfig.PidMode)
	}
	if samePidNamespace(ctx, cli, plan.PidMode, have) {
		return ""
	}

	recreate := peerRecreateHint(plan)
	if plan.PidMode == "" {
		return peerWarnPrefix + plan.ContainerName + " already runs in the shared PID namespace, " +
			"so this session can see the process table of every opted-in shell" + recreate
	}
	return peerWarnPrefix + plan.ContainerName + " was created without the shared PID namespace, " +
		"so this session will see no peers" + recreate
}

// peerSocketMountWarning is the mount-side sibling of peerMismatchWarning, and
// covers the upgrade the container-name fold cannot see: a container created
// before the socket directory became a Docker volume — or while that volume was
// unavailable — folds to the same name and holds the right PID namespace, so it
// reattaches looking healthy while its inbox sockets sit where no peer looks.
// Returns "" when there is nothing to say.
func peerSocketMountWarning(plan *sessionplan.SessionPlan, inspect container.InspectResponse) string {
	if plan.PidMode == "" {
		return ""
	}
	for _, m := range inspect.Mounts {
		if m.Destination == mountplan.PeerSocketDirTarget && m.Name == mountplan.PeerSocketVolumeName {
			return ""
		}
	}
	return peerWarnPrefix + plan.ContainerName + " does not mount the " + mountplan.PeerSocketVolumeName +
		" volume at " + mountplan.PeerSocketDirTarget + ", so this session can reach no peer" +
		peerRecreateHint(plan)
}

// peerRecreateHint is the tail every peer warning ends with. The targeted
// recreate, not `toolbox stop --all`: that one would take the anchor and every
// sibling shell down with it. `toolbox stop` accepts a full container name
// verbatim, which is what this plan holds.
func peerRecreateHint(plan *sessionplan.SessionPlan) string {
	return " — stop it with `toolbox stop " + plan.ContainerName + "`, then start the shell again"
}

// samePidNamespace reports whether an existing container's
// HostConfig.PidMode (have) is the namespace the plan asks for (want).
//
// The comparison has to go through the daemon: Docker resolves the
// `container:<name>` it is handed at ContainerCreate and stores
// `container:<id>`, so a correct reattach never matches plan.PidMode
// verbatim. An anchor that cannot be inspected counts as a mismatch — its
// namespace is gone either way.
func samePidNamespace(ctx context.Context, cli client.APIClient, want, have string) bool {
	if have == want {
		return true
	}
	if want == "" || have == "" {
		return false
	}
	res, err := cli.ContainerInspect(ctx, sessionplan.PeerAnchorContainerName, client.ContainerInspectOptions{})
	if err != nil {
		return false
	}
	return have == "container:"+res.Container.ID
}
