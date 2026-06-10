package bridge

import (
	"errors"
	"io/fs"
	"os"
	"testing"
)

func TestLoadOrCreateToken_GeneratesWhenMissing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s, _ := ResolveHostState()
	if err := EnsureHostDir(s); err != nil {
		t.Fatal(err)
	}
	tok, err := LoadOrCreateToken(s)
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}
	if len(tok) != tokenBytes*2 {
		t.Errorf("token hex length = %d, want %d", len(tok), tokenBytes*2)
	}
	info, err := os.Stat(s.Token)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("token mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestLoadOrCreateToken_PreservesExisting(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s, _ := ResolveHostState()
	_ = EnsureHostDir(s)
	first, _ := LoadOrCreateToken(s)
	second, err := LoadOrCreateToken(s)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Errorf("token rotated unexpectedly: %q vs %q", first, second)
	}
}

func TestLoadToken_NotExist(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s, _ := ResolveHostState()
	_ = EnsureHostDir(s)
	_, err := LoadToken(s)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("err = %v, want fs.ErrNotExist", err)
	}
}
