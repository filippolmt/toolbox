package mountplan

import (
	"fmt"
	"os"

	"github.com/filippolmt/toolbox/internal/config"
)

// PeerSocketDirName is the Name carried by the bind PlanInput.Peer produces,
// and the last path element of its host source. It names the entry in the
// mount-skip warnings only: `toolbox mounts` renders Classify(cfg), and this
// bind is appended inside Plan, after Merge — a session input, not a
// configurable mount, so it never shows up there.
const PeerSocketDirName = "cc-socks"

// PeerSocketDirTarget is where Claude Code keeps one inbox socket per live
// session. Per-container by default (it lives on /tmp), which is exactly why
// peers cannot reach each other without this bind.
const PeerSocketDirTarget = "/tmp/cc-socks"

// peerSocketMount is the bind that makes opted-in containers share one
// inbox-socket directory. It deliberately follows the config-level
// mounts_root (the whole ~/.toolbox tree relocates together) but NOT a
// --profile root: like the bridge dir, this is host infrastructure rather
// than a per-account credential, and forking it per profile would leave two
// opted-in shells discovering each other through the shared PID namespace
// while silently failing to deliver.
//
// CreateIfMissing rather than a pre-existing path: resolveAll's MkdirAll uses
// 0700, which is what Claude Code requires — it falls back to
// /tmp/cc-socks-<uid>, without saying so, on a looser or foreign-owned
// directory.
func peerSocketMount(mountsRoot string) config.Mount {
	return config.Mount{
		Name:            PeerSocketDirName,
		Source:          mountsRootJoin(mountsRoot, PeerSocketDirName),
		Target:          PeerSocketDirTarget,
		CreateIfMissing: true,
	}
}

// enforcePeerSocketMode holds the 0700 invariant on a socket directory that
// already exists. CreateIfMissing only covers the directory resolveAll
// creates: MkdirAll leaves a pre-existing ~/.toolbox/cc-socks at whatever
// mode it has, and Claude Code answers anything looser by falling back to
// /tmp/cc-socks-<uid> — silently, so the feature dies with no error anywhere.
// Returns a mount-skip warning when the mode cannot be corrected, "" when the
// directory is (now) fine or absent, which is resolveAll's job.
//
// A foreign-owned directory that is already 0700 is not covered: nothing here
// can tell it apart from ours, and the container-side failure (a bind it
// cannot write to) is at least loud.
func enforcePeerSocketMode(src string) string {
	info, err := os.Stat(src)
	if err != nil || info.Mode().Perm() == 0o700 {
		return ""
	}
	if chmodErr := os.Chmod(src, 0o700); chmodErr != nil {
		return fmt.Sprintf("peer socket dir must be 0700 or Claude Code silently stops sharing it, mount skipped: %s: %v", src, chmodErr)
	}
	return ""
}
