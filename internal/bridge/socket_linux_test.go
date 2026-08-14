//go:build linux

package bridge

import (
	"context"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestBindUnixListener_CreatesSocket(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s, err := ResolveHostState()
	if err != nil {
		t.Fatalf("ResolveHostState: %v", err)
	}
	ln, err := bindUnixListener(s)
	if err != nil {
		t.Fatalf("bindUnixListener: %v", err)
	}
	defer func() { _ = ln.Close() }()

	fi, err := os.Lstat(s.Socket)
	if err != nil {
		t.Fatalf("Lstat socket: %v", err)
	}
	if fi.Mode()&os.ModeSocket == 0 {
		t.Errorf("mode = %v, want socket", fi.Mode())
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("socket perm = %v, want 0600", fi.Mode().Perm())
	}
	di, err := os.Stat(s.RunDir)
	if err != nil {
		t.Fatalf("Stat run dir: %v", err)
	}
	if di.Mode().Perm() != 0o700 {
		t.Errorf("run dir perm = %v, want 0700", di.Mode().Perm())
	}
}

func TestBindUnixListener_RemovesStaleSocket(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s, err := ResolveHostState()
	if err != nil {
		t.Fatalf("ResolveHostState: %v", err)
	}
	// Simulate a crashed daemon: bind once and skip the unlink-on-close.
	stale, err := bindUnixListener(s)
	if err != nil {
		t.Fatalf("first bind: %v", err)
	}
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = stale.Close()
	if _, err := os.Lstat(s.Socket); err != nil {
		t.Fatalf("stale socket should remain: %v", err)
	}

	ln, err := bindUnixListener(s)
	if err != nil {
		t.Fatalf("rebind over stale socket: %v", err)
	}
	_ = ln.Close()
}

func TestStatus_ReportsSocket(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s, err := ResolveHostState()
	if err != nil {
		t.Fatalf("ResolveHostState: %v", err)
	}

	rep, err := Status(&fakeAgent{})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if rep.SocketPath != s.Socket {
		t.Errorf("SocketPath = %q, want %q", rep.SocketPath, s.Socket)
	}
	if rep.SocketPresent {
		t.Error("SocketPresent = true before the daemon bound the socket")
	}

	ln, err := bindUnixListener(s)
	if err != nil {
		t.Fatalf("bindUnixListener: %v", err)
	}
	defer func() { _ = ln.Close() }()
	rep, err = Status(&fakeAgent{})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !rep.SocketPresent {
		t.Error("SocketPresent = false with a bound socket")
	}
}

// TestRun_ServesOverUnixSocket drives the full daemon through the unix
// transport: Run binds the socket itself (the injected Listener only covers
// TCP), a unix-dialing client exercises /healthz and an authenticated /open,
// and shutdown must unlink the socket.
func TestRun_ServesOverUnixSocket(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s, err := ResolveHostState()
	if err != nil {
		t.Fatalf("ResolveHostState: %v", err)
	}

	tcpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("tcp listen: %v", err)
	}
	opened := make(chan string, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, DaemonOptions{
			Listener: tcpLn,
			Open: func(_ context.Context, url string) error {
				opened <- url
				return nil
			},
		})
	}()

	waitFor := func(what string, ok func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !ok() {
			if time.Now().After(deadline) {
				t.Fatalf("timeout waiting for %s", what)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	waitFor("unix socket", func() bool {
		fi, err := os.Lstat(s.Socket)
		return err == nil && fi.Mode()&os.ModeSocket != 0
	})

	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", s.Socket)
		},
	}}

	resp, err := client.Get("http://bridge" + RouteHealth)
	if err != nil {
		t.Fatalf("GET /healthz over unix socket: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("healthz status = %d, want 200", resp.StatusCode)
	}

	token, err := os.ReadFile(s.Token)
	if err != nil {
		t.Fatalf("read token: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, "http://bridge"+RouteOpen,
		strings.NewReader(`{"url":"https://example.com"}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	req.Header.Set("Content-Type", "application/json")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("POST /open over unix socket: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("open status = %d, want 204", resp.StatusCode)
	}
	select {
	case url := <-opened:
		if url != "https://example.com" {
			t.Errorf("opened url = %q", url)
		}
	case <-time.After(time.Second):
		t.Error("open handler never invoked")
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if _, err := os.Lstat(s.Socket); !os.IsNotExist(err) {
		t.Errorf("socket should be removed on shutdown, Lstat err = %v", err)
	}
}
