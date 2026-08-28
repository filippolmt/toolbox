package mountplan

// PeerSocketVolumeName is the Docker named volume carrying the shared
// inbox-socket directory.
//
// A named volume rather than a bind under ~/.toolbox: Claude Code chmods each
// inbox socket right after binding it, and Docker Desktop for macOS serves a
// host bind over virtiofs, where chmod(2) on a socket inode fails with EINVAL.
// The listener then never starts, so the session publishes no socket path and
// no session is reachable — its own included, and with nothing on screen to
// say so. A volume lives in the daemon's own filesystem on every platform, so
// the bind-then-chmod sequence behaves the same everywhere.
//
// A bind spec cannot carry ownership, and the session container runs as the
// unprivileged host UID, so internal/container initialises the volume before
// the first session mounts it (see ensurePeerSocketVolume).
const PeerSocketVolumeName = "toolbox-cc-socks"

// PeerSocketDirTarget is where Claude Code keeps one inbox socket per live
// session. Per-container by default (it lives on /tmp), which is exactly why
// peers cannot reach each other without this mount.
const PeerSocketDirTarget = "/tmp/cc-socks"

// peerSocketBind is the mount that makes opted-in containers share one
// inbox-socket directory.
//
// A Bind appended after resolveAll rather than a config.Mount: a named volume
// has no host source to expand, create or stat, and no `mounts:` patch should
// be able to retarget or disable a session input.
//
// It deliberately ignores both mounts_root and --profile. The volume is
// toolbox-owned infrastructure rather than a per-account credential, and
// forking it per profile would leave two opted-in shells discovering each
// other through the shared PID namespace while silently failing to deliver.
func peerSocketBind() Bind {
	return Bind{Source: PeerSocketVolumeName, Target: PeerSocketDirTarget, Mode: "rw"}
}

// WithoutPeerSocketBind returns binds without the shared socket mount, for a
// session that turned out not to be participating — the degrade path in
// internal/container, where an unusable anchor or volume drops both halves of
// peer messaging together. Matching on the target rather than the volume name
// keeps this in step with a renamed volume.
func WithoutPeerSocketBind(binds []Bind) []Bind {
	kept := make([]Bind, 0, len(binds))
	for _, b := range binds {
		if b.Target == PeerSocketDirTarget {
			continue
		}
		kept = append(kept, b)
	}
	return kept
}
