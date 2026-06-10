//go:build linux

package bridge

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
)

// bindUnixListener binds the daemon's unix socket under RunDir. This is the
// transport containers use on native Linux: the TCP listener is loopback-only
// and `host.docker.internal:host-gateway` resolves to the docker0 gateway IP,
// where nothing listens. The run/ subdir gets its own RW bind mount
// (bridge-run in mountplan.defaults) because connect() on a socket inside a
// read-only mount fails with EROFS.
//
// A stale socket file (left behind by a crash — clean shutdown unlinks it) is
// removed before binding so the systemd unit restarts cleanly. The socket is
// chmod'd 0600: the container runs with the host user's UID via --user, so
// owner-only access is both sufficient and the tightest boundary available.
// socketStatus reports the unix-transport state for `toolbox bridge status`:
// the socket path and whether the daemon currently has it bound.
func socketStatus(s HostState) (string, bool) {
	fi, err := os.Lstat(s.Socket)
	return s.Socket, err == nil && fi.Mode()&os.ModeSocket != 0
}

func bindUnixListener(s HostState) (net.Listener, error) {
	if err := os.MkdirAll(s.RunDir, 0o700); err != nil {
		return nil, fmt.Errorf("create %s: %w", s.RunDir, err)
	}
	if err := os.Remove(s.Socket); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("remove stale socket %s: %w", s.Socket, err)
	}
	ln, err := net.Listen("unix", s.Socket)
	if err != nil {
		return nil, fmt.Errorf("bind unix %s: %w", s.Socket, err)
	}
	if err := os.Chmod(s.Socket, 0o600); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("chmod socket %s: %w", s.Socket, err)
	}
	return ln, nil
}
