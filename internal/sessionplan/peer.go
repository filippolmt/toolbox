package sessionplan

// PeerAnchorContainerName is the toolbox-owned container whose PID namespace
// every opted-in session joins. It is not a shell: nothing runs a workspace in
// it, it outlives the sessions that reference it (`AutoRemove: false`), and it
// exists only so the namespace has a stable owner — no session container can
// play that role, since each is auto-removed on exit.
//
// It carries the ContainerNamePrefix so `toolbox stop --all` sweeps it up with
// everything else; internal/container.List excludes it explicitly, because it
// is not a shell the user opened.
const PeerAnchorContainerName = ContainerNamePrefix + "peer-anchor"

// peerNameSuffix marks the opt-in in the named-shell container format
// (`toolbox-named-<name>.peer`). The dot is load-bearing: it is legal in a
// Docker container name and SanitizeShellName cannot produce one, so the fold
// stays injective — with a `-peer` suffix, `toolbox shell infra --peer` and
// `toolbox shell infra-peer` would land on the same container and the second
// would silently reattach into a shared PID namespace it never asked for.
const peerNameSuffix = ".peer"

// peerPidMode returns the docker --pid value for the session: the anchor's
// namespace when opted in, else empty (the container's own namespace).
func peerPidMode(peer bool) string {
	if !peer {
		return ""
	}
	return "container:" + PeerAnchorContainerName
}

// peerDiscriminator folds the peer opt-in into the workspace container's hash
// seed, alongside the profile identity. Non-empty whenever peer is set, so an
// opt-in alone (no profile) still yields a distinct container.
func peerDiscriminator(base string, peer bool) string {
	if !peer {
		return base
	}
	return base + "\x00peer=1"
}
