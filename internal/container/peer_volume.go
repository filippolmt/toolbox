package container

import (
	"context"

	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/mountplan"
	"github.com/filippolmt/toolbox/internal/sessionplan"
)

// peerSocketInitContainerName is the throwaway container that takes ownership
// of a freshly created socket volume. It carries the ContainerNamePrefix for
// the same reason the anchor does: one that outlives its own cleanup — a
// daemon restart mid-init — holds the volume in use, and the prefix is what
// lets `toolbox list` show it and `toolbox stop --all` sweep it up.
const peerSocketInitContainerName = sessionplan.ContainerNamePrefix + "cc-socks-init"

var peerSocketVolume = ownedVolume{
	name:     mountplan.PeerSocketVolumeName,
	target:   mountplan.PeerSocketDirTarget,
	initName: peerSocketInitContainerName,
	label:    "peer socket volume",
}

// ensurePeerSocketVolume makes the shared inbox-socket volume exist and carry
// the ownership Claude Code requires, so an opted-in session finds a directory
// it can bind sockets in.
func ensurePeerSocketVolume(ctx context.Context, cli client.APIClient, base sessionplan.Image) error {
	return ensureOwnedVolume(ctx, cli, base, peerSocketVolume)
}
