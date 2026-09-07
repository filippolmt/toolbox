package bridge

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
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
// own player. It is the production Sound callback; logger is the daemon's own,
// because the player outlives the response and the log is the only place its
// outcome can land.
func playSound(logger *log.Logger, data []byte) error {
	return playSoundWith(logger, hostSoundCommand, data)
}

// soundPlaying is held for as long as a spawned player lives. A chime is
// seconds of audio while herdr only coalesces pane states closer together than
// a fraction of a second, so everything in between used to reach the host as
// two players on one output device replaying the same MP3 out of phase — heard
// as a chime that garbles or cuts, not as two chimes. Dropping the later one
// beats truncating the one already playing: a listener cannot tell two
// overlapping chimes apart anyway, so the clean single sound carries more.
//
// One flag for the whole daemon, which is one process per host: per-device
// gating would earn its keep only if a chime ever had to overlap itself.
var soundPlaying atomic.Bool

// errSoundBusy is what playSound returns for a chime dropped by soundPlaying.
// Not a failure — the request was well-formed and the answer is still 200 — so
// the handler renders it as its own log line rather than a 502. It travels as
// an error because the callback has no other channel back, and one line per
// chime beats a "skipped" the handler then contradicts with "ok".
var errSoundBusy = errors.New("a player is still running")

// playSoundWith writes an MP3 payload to a temp file the daemon names itself
// and spawns player on it, returning as soon as the player is running. The
// container's own temp file is unreachable from the host, so /sound carries
// the bytes and this is where they land (ADR-0009). The player builder is a
// parameter because the per-OS hostSoundCommand is the one part CI cannot run
// on every platform.
func playSoundWith(logger *log.Logger, player func(ctx context.Context, path string) (*exec.Cmd, error), data []byte) error {
	if !soundPlaying.CompareAndSwap(false, true) {
		return errSoundBusy
	}
	// From here the flag belongs to the reaping goroutine, and every path
	// that returns without a live player has to hand it back. One release
	// site rather than one per unwind: a later early return that forgot its
	// own would wedge the flag at true and silence every chime after it.
	spawned := false
	defer func() {
		if !spawned {
			soundPlaying.Store(false)
		}
	}()

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

	// The player's own diagnosis — afplay naming a payload it cannot decode,
	// a probe-chain player naming a dead device — belongs in the daemon log
	// rather than in /dev/null, which is where an unset Stderr sends it. It
	// travels through a file the reaper reads back, and not through
	// logger.Writer(): a Stderr that is not an *os.File makes os/exec build a
	// pipe and copy it in a goroutine, so cmd.Wait would return when that
	// pipe closes rather than when the player exits — a player that leaves a
	// child holding the descriptor would hold soundPlaying past its own death
	// and drop every later chime, with soundTimeout reaching only the process
	// it spawned. The file also keeps the child's bytes off logger.Writer(),
	// which is the logger's own unsynchronised writer and would interleave
	// them mid-line with the daemon's.
	errFile, err := os.CreateTemp("", "toolbox-sound-*.stderr")
	if err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("create sound stderr file: %w", err)
	}
	errPath := errFile.Name()

	// Detached from the request: the response is already on its way, so the
	// player's deadline is this context and not the caller's.
	ctx, cancel := context.WithTimeout(context.Background(), soundTimeout)
	cmd, err := player(ctx, path)
	if err == nil {
		cmd.Stderr = errFile
		err = cmd.Start()
	}
	// Start handed the player a descriptor of its own and the reaper reads
	// the file back by name, so this copy has no reader left.
	_ = errFile.Close()
	if err != nil {
		cancel()
		_ = os.Remove(path)
		_ = os.Remove(errPath)
		return fmt.Errorf("start sound player: %w", err)
	}
	spawned = true

	// The 200 is already out, so this goroutine's line is the only place the
	// player's outcome can land — and the lifetime in it is what separates a
	// chime cut inside this code (a player gone in milliseconds, killed at
	// soundTimeout or refusing the payload) from one cut past afplay, in the
	// host's output device. Without it the handler's "sound: ok" claims a
	// chime nobody heard, which is the silent degradation ADR-0009 ended.
	started := time.Now()
	go func() {
		defer cancel()
		waitErr := cmd.Wait()
		lived := time.Since(started).Round(time.Millisecond)
		said := soundPlayerStderr(errPath)
		// Released before the log line, so a caller that has read the line
		// knows the next chime will be spawned and not skipped.
		soundPlaying.Store(false)
		_ = os.Remove(path)
		_ = os.Remove(errPath)
		if waitErr != nil {
			logger.Printf("sound: player failed after %s: %v file=%s%s", lived, waitErr, filepath.Base(path), said)
			return
		}
		logger.Printf("sound: player done after %s file=%s%s", lived, filepath.Base(path), said)
	}()
	return nil
}

// soundPlayerStderr reads back what the player wrote, shaped to append to the
// reaper's line: empty when it said nothing, so a healthy chime stays one
// short line, and capped because a player looping on a dead device would
// otherwise write the daemon log full. A file that cannot be read means the
// same as a silent player — the reaper is its only reader.
func soundPlayerStderr(path string) string {
	raw, _ := os.ReadFile(path)
	said := strings.TrimSpace(string(raw))
	if said == "" {
		return ""
	}
	return " stderr=" + strconv.Quote(truncate(said, maxSoundStderr))
}

// maxSoundStderr caps what one player can add to a log line. Sized for a
// decoder's few lines of complaint, not for a loop on a dead device.
const maxSoundStderr = 512
