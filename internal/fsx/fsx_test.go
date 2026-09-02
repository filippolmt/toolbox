package fsx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// The three marker primitives are the shared half of two TTL gates whose
// semantics differ (imagepull stamps successes, imageprefetch stamps every
// attempt), so the mechanism is worth pinning once here rather than twice
// there. The asymmetry between Fresh and OlderThan is the point of the test:
// an absent marker is neither, and reading it as "old" would let a caller
// declare a condition persistent that has never been observed at all.
func TestMarkers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "marker")

	if MarkerFresh(path, time.Hour) {
		t.Error("an absent marker reads as fresh")
	}
	if MarkerOlderThan(path, time.Hour) {
		t.Error("an absent marker reads as old")
	}

	if err := TouchMarker(path); err != nil {
		t.Fatalf("TouchMarker: %v", err)
	}
	if !MarkerFresh(path, time.Hour) {
		t.Error("a marker just written does not read as fresh")
	}
	if MarkerOlderThan(path, time.Hour) {
		t.Error("a marker just written reads as old")
	}

	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}
	if MarkerFresh(path, time.Hour) {
		t.Error("a stale marker reads as fresh")
	}
	if !MarkerOlderThan(path, time.Hour) {
		t.Error("a stale marker does not read as old")
	}

	// Contents are never read — the modification time is the whole record.
	if data, err := os.ReadFile(path); err != nil || len(data) != 0 {
		t.Errorf("marker body = %q (err %v), want empty", data, err)
	}
}

// A marker path whose parent cannot be created is an error the caller has to
// see: for imagepull it means every shell pays a registry round-trip, for
// imageprefetch it means the banner would never be written either.
func TestTouchMarkerReportsAnUnusableParent(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := TouchMarker(filepath.Join(blocker, "marker")); err == nil {
		t.Error("TouchMarker succeeded with a regular file as its parent")
	}
}
