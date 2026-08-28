package container

import (
	"context"
	"fmt"

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

// ensureAnchor makes the peer-messaging anchor container exist and run, so
// opted-in sessions have a PID namespace to join. It reuses runplan.Compute
// for the connect / start / create branch — the same three-way decision the
// session container goes through, read off the same inspect snapshot.
//
// The anchor runs the toolbox runtime image (already guaranteed present
// locally by imageplan.Ensure on this path) rather than a second base image:
// the layers are shared, and there is no registry round-trip to fail on an
// offline host. Its entrypoint is overridden to a bare sleep — none of the
// image's shell-start init belongs in a container that only holds a namespace.
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
				Entrypoint: []string{"sleep"},
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
	if err := ensurePeerRuntimeParts(ctx, cli, plan); err != nil {
		ui.Warning(peerWarnPrefix + err.Error() + " — starting this shell without it")
		return "", dropPeerSocketBind(plan.Binds)
	}
	return plan.PidMode, plan.Binds
}

// ensurePeerRuntimeParts prepares the anchor's PID namespace and the shared
// socket volume, in that order: the anchor is the half a stale container name
// can already be diagnosed against (peerMismatchWarning), so failing on it
// first keeps the warning the user sees pointed at the same thing across runs.
func ensurePeerRuntimeParts(ctx context.Context, cli client.APIClient, plan *sessionplan.SessionPlan) error {
	if err := ensureAnchor(ctx, cli, plan.Image); err != nil {
		return err
	}
	return ensurePeerSocketVolume(ctx, cli, plan.Image)
}

// dropPeerSocketBind returns binds without the shared socket mount, for a
// session that turned out not to be participating. Matching on the target
// rather than the volume name keeps this in step with a renamed volume.
func dropPeerSocketBind(binds []mountplan.Bind) []mountplan.Bind {
	kept := make([]mountplan.Bind, 0, len(binds))
	for _, b := range binds {
		if b.Target == mountplan.PeerSocketDirTarget {
			continue
		}
		kept = append(kept, b)
	}
	return kept
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

	// The targeted recreate, not `toolbox stop --all`: that one would take the
	// anchor and every sibling shell down with it. `toolbox stop` accepts a
	// full container name verbatim, which is what this plan holds.
	recreate := " — stop it with `toolbox stop " + plan.ContainerName + "`, then start the shell again"
	if plan.PidMode == "" {
		return peerWarnPrefix + plan.ContainerName + " already runs in the shared PID namespace, " +
			"so this session can see the process table of every opted-in shell" + recreate
	}
	return peerWarnPrefix + plan.ContainerName + " was created without the shared PID namespace, " +
		"so this session will see no peers" + recreate
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
