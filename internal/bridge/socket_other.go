//go:build !linux

package bridge

import "net"

// bindUnixListener is a no-op off Linux: Docker Desktop (macOS/Windows)
// cannot share host unix sockets with containers, so the TCP transport via
// host.docker.internal stays the only path there. Returning (nil, nil) lets
// Run skip the unix listener without logging a degradation.
func bindUnixListener(HostState) (net.Listener, error) { return nil, nil }

// socketStatus returns the zero state off Linux so `toolbox bridge status`
// omits the socket line entirely.
func socketStatus(HostState) (string, bool) { return "", false }
