package bridge

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

	if err := playSoundWith(log.New(io.Discard, "", 0), player, []byte("ID3-chime")); err != nil {
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

	if err := playSoundWith(log.New(io.Discard, "", 0), player, []byte("x")); err == nil {
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

	if err := playSoundWith(log.New(io.Discard, "", 0), player, []byte("x")); !errors.Is(err, want) {
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

// logSink is the reaping goroutine's log destination: a channel, not a buffer.
// The goroutine writes while the test reads, so a shared bytes.Buffer would be
// the data race `go test -race` exists to catch.
type logSink struct{ lines chan string }

func newLogSink() logSink { return logSink{lines: make(chan string, 4)} }

func (s logSink) Write(p []byte) (int, error) {
	s.lines <- strings.TrimSpace(string(p))
	return len(p), nil
}

// line returns the goroutine's log line, failing the test if it never comes —
// no line means a failing player stays invisible.
func (s logSink) line(t *testing.T) string {
	t.Helper()
	select {
	case l := <-s.lines:
		return l
	case <-time.After(5 * time.Second):
		t.Fatal("the reaping goroutine logged nothing — a dead player stays invisible")
		return ""
	}
}

// The response is already out when the player starts, so a player that dies —
// killed at soundTimeout, or refusing a payload afplay cannot decode — can only
// be reported into the daemon log. Without this line the log's "sound: ok"
// claims a chime the host never made a sound for, which is the same silent
// degradation ADR-0009 was written to end.
func TestPlaySoundLogsAPlayerThatFails(t *testing.T) {
	sink := newLogSink()
	player := func(ctx context.Context, path string) (*exec.Cmd, error) {
		return exec.CommandContext(ctx, "/bin/sh", "-c", "exit 3"), nil
	}

	if err := playSoundWith(log.New(sink, "", 0), player, []byte("x")); err != nil {
		t.Fatalf("playSoundWith: %v", err)
	}

	if line := sink.line(t); !strings.Contains(line, "exit status 3") {
		t.Errorf("log line %q carries no player exit status", line)
	}
}

// A chime lasts seconds and herdr coalesces only what is far closer together
// than that, so the daemon is where the rest has to stop: a second player on
// the same output device replays the same MP3 out of phase, and what the
// listener hears is one garbled chime rather than two.
func TestPlaySoundSkipsAChimeWhileAPlayerIsStillRunning(t *testing.T) {
	sink := newLogSink()
	spawned := 0
	player := func(ctx context.Context, path string) (*exec.Cmd, error) {
		spawned++
		return exec.CommandContext(ctx, "/bin/sh", "-c", "sleep 0.3"), nil
	}
	logger := log.New(sink, "", 0)

	if err := playSoundWith(logger, player, []byte("x")); err != nil {
		t.Fatalf("first chime: %v", err)
	}
	if err := playSoundWith(logger, player, []byte("x")); !errors.Is(err, errSoundBusy) {
		t.Errorf("second chime err = %v, want errSoundBusy so the handler can name it", err)
	}

	if spawned != 1 {
		t.Errorf("spawned %d players, want 1 — the second overlaps the first on one device", spawned)
	}
	// Draining the first player's own line leaves the flag released for the
	// next test.
	sink.line(t)
}

// An exit status alone does not say why a chime was lost, and the player is
// the only one who knows: its stderr must reach the same log, not /dev/null.
func TestPlaySoundLogsWhatThePlayerWroteToStderr(t *testing.T) {
	sink := newLogSink()
	player := func(ctx context.Context, path string) (*exec.Cmd, error) {
		return exec.CommandContext(ctx, "/bin/sh", "-c", "echo cannot-decode >&2; exit 1"), nil
	}

	if err := playSoundWith(log.New(sink, "", 0), player, []byte("x")); err != nil {
		t.Fatalf("playSoundWith: %v", err)
	}

	// Two writes reach the sink — the player's own and the reaping
	// goroutine's — and the order between a child process and a goroutine is
	// not ours to assume.
	got := sink.line(t) + "\n" + sink.line(t)
	if !strings.Contains(got, "cannot-decode") {
		t.Errorf("log %q dropped the player stderr", got)
	}
}

// The successful line is what makes a *cut* chime diagnosable: a host player
// living as long as the MP3 puts the truncation past afplay, in the output
// device, while a player gone in milliseconds puts it in this code.
func TestPlaySoundLogsHowLongThePlayerLived(t *testing.T) {
	sink := newLogSink()
	player := func(ctx context.Context, path string) (*exec.Cmd, error) {
		return exec.CommandContext(ctx, "/bin/sh", "-c", "sleep 0.2"), nil
	}

	if err := playSoundWith(log.New(sink, "", 0), player, []byte("x")); err != nil {
		t.Fatalf("playSoundWith: %v", err)
	}

	if line := sink.line(t); !strings.Contains(line, "player done after") {
		t.Errorf("log line %q does not report the player lifetime", line)
	}
}
