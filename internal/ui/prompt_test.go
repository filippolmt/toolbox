package ui

import (
	"os"
	"strings"
	"testing"
	"time"
)

// promptPipes swaps the prompt's input and output for a pipe and a buffer,
// restoring both afterwards. The reader must be a real *os.File: the cancel
// that unblocks a timed-out read needs a file descriptor, and a pipe is the
// only *os.File a test can write into.
func promptPipes(t *testing.T, typed string) *strings.Builder {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	var out strings.Builder

	oldIn, oldOut := promptIn, promptOut
	promptIn, promptOut = r, &out
	t.Cleanup(func() {
		promptIn, promptOut = oldIn, oldOut
		_ = r.Close()
		_ = w.Close()
	})

	if typed != "" {
		if _, err := w.WriteString(typed); err != nil {
			t.Fatalf("write typed answer: %v", err)
		}
	}
	return &out
}

// TestConfirmCountdownAnswers pins the three answers the start-up refresh
// prompt has to tell apart. Only an explicit "n" declines; a bare Return is a
// yes, because the visible default is what a developer who is not reading the
// question ends up choosing either way.
func TestConfirmCountdownAnswers(t *testing.T) {
	for _, tc := range []struct {
		name  string
		typed string
		want  bool
	}{
		{"declined", "n\n", false},
		{"declined spelled out", "No\n", false},
		{"bare return", "\n", true},
		{"accepted", "y\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := promptPipes(t, tc.typed)
			if got := ConfirmCountdown("Download now?", time.Minute); got != tc.want {
				t.Errorf("ConfirmCountdown() = %v, want %v", got, tc.want)
			}
			if !strings.Contains(out.String(), "Download now?") {
				t.Errorf("the question was never printed: %q", out.String())
			}
		})
	}
}

// TestConfirmCountdownTimesOutIntoYes covers the developer who is not looking:
// the window elapses, the answer is yes, and the countdown was visible while
// it ran — five seconds of silence is indistinguishable from a hang.
func TestConfirmCountdownTimesOutIntoYes(t *testing.T) {
	oldTick := countdownTick
	countdownTick = time.Millisecond
	t.Cleanup(func() { countdownTick = oldTick })

	out := promptPipes(t, "")
	if !ConfirmCountdown("Download now?", 20*time.Millisecond) {
		t.Error("a timed-out prompt must answer yes")
	}
	if strings.Count(out.String(), "\r") < 2 {
		t.Errorf("the countdown never redrew: %q", out.String())
	}
}

// A pipe is nobody to ask, which is the whole point of the check: off a
// terminal the caller must not spend a window waiting for an answer that
// cannot arrive.
func TestAskableIsFalseOffATerminal(t *testing.T) {
	promptPipes(t, "")
	if Askable() {
		t.Error("Askable() = true on a pipe")
	}
}

// The last tick can land after the window has run out, and a countdown that
// then prints a negative second reads like a bug in the very moment the
// developer is being asked to trust it.
func TestRenderClampsAnExpiredCountdown(t *testing.T) {
	out := promptPipes(t, "")
	render("Download now?", -2*time.Second)
	if !strings.Contains(out.String(), "(0s)") {
		t.Errorf("render() = %q, want a clamped (0s)", out.String())
	}
}
