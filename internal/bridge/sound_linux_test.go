//go:build linux

package bridge

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

// The chosen player is invoked with its own flags and the temp file LAST — a
// player that takes the path before its flags plays nothing.
func TestSoundPlayerPutsTheFileAfterTheFlags(t *testing.T) {
	dir := t.TempDir()
	// mpv is the chain's last entry, so stubbing only it also proves the walk
	// reaches the end rather than stopping at the first missing name.
	if err := os.WriteFile(filepath.Join(dir, "mpv"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("stub player: %v", err)
	}
	t.Setenv("PATH", dir)

	cmd, err := hostSoundCommand(context.Background(), "/tmp/chime.mp3")
	if err != nil {
		t.Fatalf("hostSoundCommand: %v", err)
	}
	want := []string{"mpv", "--no-video", "--really-quiet", "/tmp/chime.mp3"}
	if !slices.Equal(cmd.Args, want) {
		t.Errorf("argv = %q, want %q", cmd.Args, want)
	}
}

// playSound is the production callback, so it must route through the per-OS
// chooser rather than a chain of its own: on a host with no player installed
// that is the error the shim propagates.
func TestPlaySoundRoutesThroughTheHostChooser(t *testing.T) {
	if _, _, err := pickSoundPlayer(exec.LookPath); err == nil {
		t.Skip("this host has a player installed — playSound would spawn it on a bogus MP3")
	}

	if err := playSound([]byte("x")); !errors.Is(err, ErrNoSoundPlayer) {
		t.Errorf("err = %v, want it to wrap ErrNoSoundPlayer", err)
	}
}
