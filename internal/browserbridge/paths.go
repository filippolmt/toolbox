// Package browserbridge implements the host-side daemon and container-side
// wrapper that let CLIs inside a toolbox shell open URLs in the host's real
// browser (Safari/Chrome on macOS, the user's default browser on Linux).
//
// Architecture:
//
//	┌─ container ──────────┐               ┌─ host (Mac/Linux) ───────────┐
//	│ CLI calls xdg-open   │  HTTP/JSON    │ toolbox browser-bridge daemon │
//	│ wrapper POSTs URL  ──┼─────────────▶ │ 127.0.0.1:<port>             │
//	│ over host.docker.    │  bearer token │ verifies token + allowlist   │
//	│ internal             │               │ execs /usr/bin/open or       │
//	└──────────────────────┘               │ xdg-open on host             │
//	                                       └──────────────────────────────┘
//
// The daemon is installed as a per-user persistent service (LaunchAgent on
// macOS, systemd --user unit on Linux). State lives under ~/.toolbox/browser/:
//
//	~/.toolbox/browser/token   — 32-byte hex bearer token (0600)
//	~/.toolbox/browser/port    — effective TCP port the daemon bound to
//	~/.toolbox/browser/log     — audit + diagnostic log
//	~/.toolbox/browser/pid     — daemon PID for status checks
//
// The whole directory is bind-mounted read-only into the container at
// /home/toolbox/.toolbox/browser/, so the wrapper script picks up token+port
// without any extra env wiring.
//
// Security boundary: 127.0.0.1 bind, bearer token, URL scheme allowlist
// (http/https only), URL length cap, rate limit. See allowlist.go + daemon.go.
package browserbridge

import (
	"fmt"
	"os"
	"path/filepath"
)

// HostDir is the directory under the user's home that holds all
// browser-bridge state on the host. Mounted RO into the container at
// ContainerDir.
const HostDir = ".toolbox/browser"

// ContainerDir is the in-container path that mirrors HostDir read-only.
// Must match the mount Target declared in mountplan.defaults.
const ContainerDir = "/home/toolbox/.toolbox/browser"

// File names inside HostDir / ContainerDir. Kept in one place so the daemon
// (host) and the wrapper script (container) cannot drift apart.
const (
	tokenFile = "token"
	portFile  = "port"
	logFile   = "log"
	pidFile   = "pid"
)

// HostState is the resolved set of absolute paths under HostDir for the
// current user. Daemon and CLI subcommands consume this; the wrapper script
// reads the container-side mirror directly.
type HostState struct {
	Dir   string // ~/.toolbox/browser
	Token string // <Dir>/token
	Port  string // <Dir>/port
	Log   string // <Dir>/log
	PID   string // <Dir>/pid
}

// ResolveHostState returns the absolute host paths for browser-bridge state.
// It does NOT create any files — callers that need the dir to exist must
// call EnsureHostDir.
func ResolveHostState() (HostState, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return HostState{}, fmt.Errorf("resolve home directory: %w", err)
	}
	dir := filepath.Join(home, HostDir)
	return HostState{
		Dir:   dir,
		Token: filepath.Join(dir, tokenFile),
		Port:  filepath.Join(dir, portFile),
		Log:   filepath.Join(dir, logFile),
		PID:   filepath.Join(dir, pidFile),
	}, nil
}

// EnsureHostDir creates the host state directory with mode 0700 if it does
// not already exist. Idempotent.
func EnsureHostDir(s HostState) error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", s.Dir, err)
	}
	return nil
}
