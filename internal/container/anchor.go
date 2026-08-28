package container

import (
	"context"
	"fmt"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

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

// ensurePeerPidMode resolves the PID namespace the session container is
// created with, materialising the anchor on the way — the daemon I/O is the
// reason it is not the pure sessionplan.peerPidMode. An unusable anchor
// degrades to the container's own namespace with a warning rather than
// blocking the shell — the same posture the repo takes for a missing proximo
// stack. The shell still works; only peer messaging is gone.
func ensurePeerPidMode(ctx context.Context, cli client.APIClient, plan *sessionplan.SessionPlan) string {
	if plan.PidMode == "" {
		return ""
	}
	if err := ensureAnchor(ctx, cli, plan.Image); err != nil {
		ui.Warning(peerWarnPrefix + err.Error() + " — starting this shell without it")
		return ""
	}
	return plan.PidMode
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
