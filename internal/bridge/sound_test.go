package bridge

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// waitGone polls until path disappears. The removal belongs to the reaping
// goroutine, so it lands after the player exits rather than before playSound
// returns.
func waitGone(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("temp file %s still there — one leaks per chime", path)
}

// The payload carries content, so the daemon is the one that materialises a
// file: the player must see exactly the posted bytes under an .mp3 name (the
// probe chain holds players that sniff the format from the extension), and the
// reap must take the file away once the player exits.
func TestPlaySoundWritesTheBytesThenReapsTheFile(t *testing.T) {
	var gotPath string
	var gotBody []byte
	player := func(ctx context.Context, path string) (*exec.Cmd, error) {
		gotPath = path
		gotBody, _ = os.ReadFile(path)
		return exec.CommandContext(ctx, "/bin/sh", "-c", "exit 0"), nil
	}

	if err := playSoundWith(player, []byte("ID3-chime")); err != nil {
		t.Fatalf("playSoundWith: %v", err)
	}

	if string(gotBody) != "ID3-chime" {
		t.Errorf("player was handed %q, want the posted bytes", gotBody)
	}
	if filepath.Ext(gotPath) != ".mp3" {
		t.Errorf("temp file %s has no .mp3 extension", gotPath)
	}
	waitGone(t, gotPath)
}

// A player that cannot be started is the failure the shim turns into a
// non-zero exit, and the temp file must not survive it — the reaping goroutine
// never runs when Start fails.
func TestPlaySoundLeavesNoFileWhenThePlayerCannotStart(t *testing.T) {
	absent := filepath.Join(t.TempDir(), "no-such-player")
	var gotPath string
	player := func(ctx context.Context, path string) (*exec.Cmd, error) {
		gotPath = path
		return exec.CommandContext(ctx, absent, path), nil
	}

	if err := playSoundWith(player, []byte("x")); err == nil {
		t.Fatal("playSoundWith reported success for a player that cannot start")
	}
	if _, err := os.Stat(gotPath); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("stat %s = %v, want the file gone", gotPath, err)
	}
}

// No player on the host at all is the chooser's error, and it must reach the
// caller without a temp file left behind.
func TestPlaySoundReportsAChooserThatFindsNoPlayer(t *testing.T) {
	want := errors.New("no player")
	player := func(context.Context, string) (*exec.Cmd, error) { return nil, want }

	if err := playSoundWith(player, []byte("x")); !errors.Is(err, want) {
		t.Errorf("err = %v, want it to wrap the chooser's error", err)
	}
}

// The chain is walked in order and the daemon picks the player, never the
// caller — so there is no third allowlist to maintain. A host with a later
// entry only must still get a sound.
func TestPickSoundPlayerWalksTheChainInOrder(t *testing.T) {
	installed := func(names ...string) func(string) (string, error) {
		set := map[string]struct{}{}
		for _, n := range names {
			set[n] = struct{}{}
		}
		return func(name string) (string, error) {
			if _, ok := set[name]; ok {
				return "/usr/bin/" + name, nil
			}
			return "", exec.ErrNotFound
		}
	}

	first, _, err := pickSoundPlayer(installed("mpv", "paplay"))
	if err != nil || first != "paplay" {
		t.Errorf("player = %q, err = %v — want the first chain entry present", first, err)
	}

	last, args, err := pickSoundPlayer(installed("mpg123"))
	if err != nil || last != "mpg123" {
		t.Fatalf("player = %q, err = %v — a host with only a late entry must still play", last, err)
	}
	if len(args) == 0 {
		t.Error("mpg123 was picked without its quiet flag — the daemon has no terminal to print to")
	}
}

// No player at all is the one failure the shim must turn into a non-zero exit,
// so herdr falls through its own chain and writes the aggregate warning that
// diagnosed this bug.
func TestPickSoundPlayerRefusesWhenTheHostHasNone(t *testing.T) {
	none := func(string) (string, error) { return "", exec.ErrNotFound }

	if _, _, err := pickSoundPlayer(none); err == nil {
		t.Error("pickSoundPlayer found a player on a host with none installed")
	}
}
