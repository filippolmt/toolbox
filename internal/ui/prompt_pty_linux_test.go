package ui

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// promptPTY hands the prompt a real terminal instead of a pipe. Only a tty can
// carry this bug: a pipe has no line discipline, so the canonical mode that
// holds a keystroke back until Return is exactly what a pipe cannot reproduce.
// Linux-only, which is where the suite runs — the golang container and CI.
func promptPTY(t *testing.T) (master *os.File, out *strings.Builder) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Fatalf("unlockpt: %v", err)
	}
	n, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Fatalf("ptsname: %v", err)
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("open pts: %v", err)
	}

	var buf strings.Builder
	oldIn, oldOut := promptIn, promptOut
	promptIn, promptOut = slave, &buf
	t.Cleanup(func() {
		promptIn, promptOut = oldIn, oldOut
		_ = slave.Close()
		_ = master.Close()
	})
	return master, &buf
}

// TestConfirmCountdownAnswersOnOneKeypress is the whole point of the prompt
// being a single question: on a terminal the answer lands on the keystroke,
// with no Return behind it. Left in canonical mode the byte never reaches the
// process at all and the window elapses into a yes.
func TestConfirmCountdownAnswersOnOneKeypress(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  string
		want bool
	}{
		{"declined", "n", false},
		{"accepted", "y", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			master, _ := promptPTY(t)

			done := make(chan bool, 1)
			go func() { done <- ConfirmCountdown("Download now?", 3*time.Second) }()

			// The keystroke has to land after the prompt has taken the
			// terminal, the way a developer's does.
			time.Sleep(100 * time.Millisecond)
			if _, err := master.WriteString(tc.key); err != nil {
				t.Fatalf("type %q: %v", tc.key, err)
			}

			select {
			case got := <-done:
				if got != tc.want {
					t.Errorf("ConfirmCountdown() = %v, want %v", got, tc.want)
				}
			case <-time.After(time.Second):
				t.Fatalf("%q alone never answered: the prompt waited for a Return", tc.key)
			}
		})
	}
}
