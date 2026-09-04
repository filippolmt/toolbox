package bridge

import (
	"context"
	"net"
	"testing"
)

// A caller-supplied TCP listener is served as-is and its real port reported —
// the path tests take, where binding DefaultPort would collide with a running
// daemon.
func TestResolveListenerUsesTheSuppliedListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	want := ln.Addr().(*net.TCPAddr).Port

	got, port, err := resolveListener(DaemonOptions{Listener: ln})
	if err != nil {
		t.Fatalf("resolveListener: %v", err)
	}
	if got != ln {
		t.Error("resolveListener returned a different listener than the one supplied")
	}
	if port != want {
		t.Errorf("port = %d, want %d", port, want)
	}
}

// A non-TCP listener has no port to report. It must still be served rather
// than rejected — that is the unix-socket case.
func TestResolveListenerHandlesNonTCPListener(t *testing.T) {
	ln := nonTCPListener{}
	got, port, err := resolveListener(DaemonOptions{Listener: ln})
	if err != nil {
		t.Fatalf("resolveListener: %v", err)
	}
	if got != ln {
		t.Error("resolveListener should pass a non-TCP listener through unchanged")
	}
	if port != 0 {
		t.Errorf("port = %d, want 0 for a listener with no TCP address", port)
	}
}

// With no listener supplied, the preferred port is bound; zero means "no
// preference" and must fall back to DefaultPort rather than binding port 0,
// which would hand out an arbitrary port no client could find.
func TestResolveListenerBindsPreferredPort(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	free := probe.Addr().(*net.TCPAddr).Port
	_ = probe.Close()

	ln, port, err := resolveListener(DaemonOptions{Preferred: free})
	if err != nil {
		t.Skipf("could not bind the probed port %d: %v", free, err)
	}
	defer func() { _ = ln.Close() }()
	if port != free {
		t.Errorf("port = %d, want the preferred %d", port, free)
	}
}

// Every unset callback gets its production implementation; the ones a test
// supplied are left alone. Without this a partially-populated handlerFns would
// nil-panic on the endpoints the test did not care about.
func TestWithHostDefaultsFillsOnlyTheGaps(t *testing.T) {
	called := false
	supplied := func(context.Context, string) error { called = true; return nil }

	fns := handlerFns{open: supplied}.withHostDefaults(testHost(t))

	if fns.edit == nil || fns.proximo == nil || fns.credential == nil || fns.sound == nil {
		t.Fatal("withHostDefaults left a callback nil")
	}
	if err := fns.open(context.Background(), "https://example.test"); err != nil {
		t.Fatalf("open: %v", err)
	}
	if !called {
		t.Error("withHostDefaults replaced the supplied open callback")
	}
}

// nonTCPListener stands in for the unix socket: a listener whose Addr is not a
// *net.TCPAddr.
type nonTCPListener struct{}

func (nonTCPListener) Accept() (net.Conn, error) {
	return nil, net.ErrClosed
}
func (nonTCPListener) Close() error { return nil }
func (nonTCPListener) Addr() net.Addr {
	return &net.UnixAddr{Name: "/tmp/probe.sock", Net: "unix"}
}
