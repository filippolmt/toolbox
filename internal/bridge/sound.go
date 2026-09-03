package bridge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// soundTimeout bounds a spawned player's life. Nothing waits on it, so the
// only thing that can stop a player wedged on a broken audio device is this
// deadline plus the reap that follows it — mirroring herdr's own
// terminate_and_reap rather than leaving a process per chime behind.
const soundTimeout = 30 * time.Second

// soundPlayerChain is the ordered chain the Linux daemon probes for a player,
// carrying the same five names and flags herdr's own src/sound.rs spawns — the
// host is where they exist and the container is where they never will. Kept
// untagged, even though only sound_linux.go reads it, so pickSoundPlayer stays
// under test on every platform CI runs on (the same reason editorApps is).
var soundPlayerChain = []struct {
	name string
	args []string
}{
	{name: "paplay"},
	{name: "pw-play"},
	{name: "ffplay", args: []string{"-nodisp", "-autoexit", "-loglevel", "quiet"}},
	{name: "mpg123", args: []string{"-q"}},
	{name: "mpv", args: []string{"--no-video", "--really-quiet"}},
}

// ErrNoSoundPlayer is returned when no entry of soundPlayerChain is installed on
// the host. The daemon answers 502, the shim exits non-zero, and herdr falls
// through to the other four names it knows — writing the aggregate "no
// mp3-capable audio player available" line that made this defect diagnosable
// in the first place.
var ErrNoSoundPlayer = errors.New("no mp3-capable audio player on the host")

// pickSoundPlayer returns the first chain entry lookPath resolves, together
// with the flags that keep it quiet and windowless. The daemon chooses, never
// the caller — a container-supplied player name would be a third allowlist to
// maintain and a host exec to gate.
func pickSoundPlayer(lookPath func(string) (string, error)) (name string, args []string, err error) {
	for _, p := range soundPlayerChain {
		if _, err := lookPath(p.name); err == nil {
			return p.name, p.args, nil
		}
	}
	return "", nil, ErrNoSoundPlayer
}

// playSound writes an MP3 payload to a temp file and plays it with the host's
// own player. It is the production Sound callback.
func playSound(data []byte) error { return playSoundWith(hostSoundCommand, data) }

// playSoundWith writes an MP3 payload to a temp file the daemon names itself
// and spawns player on it, returning as soon as the player is running. The
// container's own temp file is unreachable from the host, so /sound carries
// the bytes and this is where they land (ADR-0009). The player builder is a
// parameter because the per-OS hostSoundCommand is the one part CI cannot run
// on every platform.
func playSoundWith(player func(ctx context.Context, path string) (*exec.Cmd, error), data []byte) error {
	// The .mp3 suffix is load-bearing: some players in the probe chain infer
	// the format from the name rather than from the bytes.
	f, err := os.CreateTemp("", "toolbox-sound-*.mp3")
	if err != nil {
		return fmt.Errorf("create sound temp file: %w", err)
	}
	path := f.Name()
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("write sound temp file: %w", err)
	}

	// Detached from the request: the response is already on its way, so the
	// player's deadline is this context and not the caller's.
	ctx, cancel := context.WithTimeout(context.Background(), soundTimeout)
	cmd, err := player(ctx, path)
	if err == nil {
		err = cmd.Start()
	}
	if err != nil {
		cancel()
		_ = os.Remove(path)
		return fmt.Errorf("start sound player: %w", err)
	}

	go func() {
		defer cancel()
		_ = cmd.Wait()
		_ = os.Remove(path)
	}()
	return nil
}
