package bridge

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/filippolmt/toolbox/internal/fsx"
)

// DefaultPort is the preferred TCP port the daemon binds to on 127.0.0.1.
// 17654 is in the IANA dynamic/private range (49152-65535 strictly, but
// 17000s are commonly used by per-user daemons too) and unlikely to collide
// with system services. If the port is busy the daemon falls back to an
// ephemeral port (BindListener) and writes the effective port to the port
// file so clients can discover it.
const DefaultPort = 17654

// BindListener tries DefaultPort on 127.0.0.1 first and falls back to an
// ephemeral port assigned by the kernel when the preferred port is busy.
// Returns the bound listener and the effective port number.
//
// Centralising the bind logic here keeps the daemon's startup deterministic
// for tests: callers can substitute a custom port via override and still get
// the fallback behaviour for free.
func BindListener(preferred int) (net.Listener, int, error) {
	if preferred > 0 {
		if ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", preferred)); err == nil {
			return ln, preferred, nil
		}
		// fall through to ephemeral
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, 0, fmt.Errorf("bind 127.0.0.1: %w", err)
	}
	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		_ = ln.Close()
		return nil, 0, fmt.Errorf("listener returned unexpected address type %T", ln.Addr())
	}
	return ln, tcpAddr.Port, nil
}

// WritePort persists the effective daemon port to the port file (mode 0644
// so the container user can read it through the RO bind-mount regardless of
// host UID alignment).
func WritePort(s HostState, port int) error {
	if port <= 0 || port > 65535 {
		return fmt.Errorf("invalid port %d", port)
	}
	if err := fsx.AtomicWriteFile(s.Port, []byte(strconv.Itoa(port)+"\n"), 0o644); err != nil {
		return fmt.Errorf("write port file %s: %w", s.Port, err)
	}
	return nil
}

// LoadPort reads the effective port back from disk. Returns fs.ErrNotExist
// when the daemon has not started yet.
func LoadPort(s HostState) (int, error) {
	b, err := os.ReadFile(s.Port)
	if err != nil {
		return 0, err
	}
	port, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0, fmt.Errorf("parse port file %s: %w", s.Port, err)
	}
	if port <= 0 || port > 65535 {
		return 0, fmt.Errorf("port file %s contains invalid port %d", s.Port, port)
	}
	return port, nil
}

// ClearPort removes the port file. Used by Uninstall + on daemon shutdown
// to avoid stale state confusing the wrapper.
func ClearPort(s HostState) error {
	err := os.Remove(s.Port)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove port file %s: %w", s.Port, err)
	}
	return nil
}
