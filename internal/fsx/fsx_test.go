package fsx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHomeReturnsEnvHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	got, err := Home()
	if err != nil {
		t.Fatalf("Home: %v", err)
	}
	if got != dir {
		t.Fatalf("Home = %q, want %q", got, dir)
	}
}

func TestHomeFailsLoudWhenUnresolvable(t *testing.T) {
	// An empty HOME makes os.UserHomeDir fail on unix; Home must surface it
	// wrapped (never return an empty path that callers would join onto).
	t.Setenv("HOME", "")
	got, err := Home()
	if err == nil {
		t.Fatalf("Home returned %q, want error for empty $HOME", got)
	}
	if got != "" {
		t.Fatalf("Home returned non-empty %q alongside error", got)
	}
	if !strings.Contains(err.Error(), "resolve home directory:") {
		t.Fatalf("error %q missing prefix", err)
	}
}

func TestExpandTilde(t *testing.T) {
	const home = "/home/u"
	cases := []struct {
		in, want string
	}{
		{"~", home},
		{"~/.config", filepath.Join(home, ".config")},
		{"~/a/b", filepath.Join(home, "a", "b")},
		{"/abs/path", "/abs/path"},
		{"relative/path", "relative/path"},
		{"~weird", "~weird"}, // only ~ and ~/ are expanded
		{"", ""},
	}
	for _, c := range cases {
		if got := ExpandTilde(c.in, home); got != c.want {
			t.Errorf("ExpandTilde(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestAtomicWriteFileWritesAndOverwrites(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "f")

	if err := AtomicWriteFile(dest, []byte("v1"), 0o600); err != nil {
		t.Fatalf("AtomicWriteFile v1: %v", err)
	}
	if err := AtomicWriteFile(dest, []byte("v2"), 0o600); err != nil {
		t.Fatalf("AtomicWriteFile v2: %v", err)
	}

	b, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(b) != "v2" {
		t.Fatalf("content = %q, want v2", b)
	}
}

func TestAtomicWriteFileLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "f")
	if err := AtomicWriteFile(dest, []byte("hello"), 0o600); err != nil {
		t.Fatalf("AtomicWriteFile: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("leftover temp file: %s", e.Name())
		}
	}
	if len(entries) != 1 {
		t.Fatalf("dir has %d entries, want 1", len(entries))
	}
}
