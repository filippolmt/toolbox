package browserbridge

import (
	"net"
	"testing"
)

func TestBindListener_PreferredFreePort(t *testing.T) {
	ln, port, err := BindListener(0)
	if err != nil {
		t.Fatalf("BindListener: %v", err)
	}
	defer ln.Close()
	if port <= 0 {
		t.Errorf("port = %d", port)
	}
}

func TestBindListener_FallsBackWhenBusy(t *testing.T) {
	first, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("setup listener: %v", err)
	}
	defer first.Close()
	busy := first.Addr().(*net.TCPAddr).Port

	ln, port, err := BindListener(busy)
	if err != nil {
		t.Fatalf("BindListener fallback: %v", err)
	}
	defer ln.Close()
	if port == busy {
		t.Errorf("expected fallback port, got busy port %d", port)
	}
}

func TestWriteLoadClearPort(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s, err := ResolveHostState()
	if err != nil {
		t.Fatalf("ResolveHostState: %v", err)
	}
	if err := EnsureHostDir(s); err != nil {
		t.Fatalf("EnsureHostDir: %v", err)
	}
	if err := WritePort(s, 17654); err != nil {
		t.Fatal(err)
	}
	got, err := LoadPort(s)
	if err != nil || got != 17654 {
		t.Fatalf("LoadPort = %d, %v", got, err)
	}
	if err := ClearPort(s); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPort(s); err == nil {
		t.Errorf("LoadPort after Clear should error")
	}
}

func TestWritePort_RejectsInvalid(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s, err := ResolveHostState()
	if err != nil {
		t.Fatalf("ResolveHostState: %v", err)
	}
	if err := EnsureHostDir(s); err != nil {
		t.Fatalf("EnsureHostDir: %v", err)
	}
	for _, p := range []int{-1, 0, 65536} {
		if err := WritePort(s, p); err == nil {
			t.Errorf("WritePort(%d) returned nil", p)
		}
	}
}
