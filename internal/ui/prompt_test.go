package ui

import (
	"os"
	"os/signal"
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

// TestConfirmCountdownAnswers pins the answers the start-up refresh prompt has
// to tell apart. A decisive key ends the read on its own, with or without the
// Return the terminal used to require. Only an explicit "n" declines; a bare Return is a
// yes, because the visible default is what a developer who is not reading the
// question ends up choosing either way.
func TestConfirmCountdownAnswers(t *testing.T) {
	for _, tc := range []struct {
		name  string
		typed string
		want  bool
	}{
		{"declined", "n\n", false},
		{"declined on the key alone", "n", false},
		{"accepted on the key alone", "y", true},
		{"declined spelled out", "No\n", false},
		{"bare return", "\n", true},
		{"accepted", "y\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := promptPipes(t, tc.typed)
			if got, _ := ConfirmCountdown("Download now?", time.Minute); got != tc.want {
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
	if yes, _ := ConfirmCountdown("Download now?", 20*time.Millisecond); !yes {
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

// Raw mode takes ctrl+c away from the terminal driver, which would make the
// question the one moment in the session where ctrl+c does nothing. It has to
// mean both things it means everywhere else: this download is off, and the
// command behind it stops.
func TestConfirmCountdownRaisesAnInterrupt(t *testing.T) {
	raised := make(chan struct{}, 1)
	old := interrupt
	interrupt = func() { raised <- struct{}{} }
	t.Cleanup(func() { interrupt = old })

	promptPipes(t, "\x03")
	yes, interrupted := ConfirmCountdown("Download now?", time.Minute)
	if yes {
		t.Error("a ctrl+c must not be read as a yes")
	}
	if !interrupted {
		t.Error("a ctrl+c must be reported to the caller: it stops the command, not just the download")
	}
	select {
	case <-raised:
	default:
		t.Error("ctrl+c was swallowed: no interrupt was raised")
	}
}

// A developer who has answered a hundred prompts types the Return out of habit,
// behind a key that had already decided. Whatever the prompt leaves on stdin
// becomes the first keystrokes of the session that attaches to it a moment
// later, so the tail of an answer has to die with the question.
func TestConfirmCountdownSwallowsWhatFollowsTheAnswer(t *testing.T) {
	promptPipes(t, "y\rand this too")
	if yes, _ := ConfirmCountdown("Download now?", time.Minute); !yes {
		t.Fatal("ConfirmCountdown() = false, want true")
	}

	// A read that never returns is the pass: there is nothing left to read.
	// The cleanup closes both ends, which is what releases it.
	leftover := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 32)
		if n, err := promptIn.Read(buf); err == nil && n > 0 {
			leftover <- buf[:n]
		}
	}()
	select {
	case tail := <-leftover:
		t.Errorf("left %q behind for the session to inherit", tail)
	case <-time.After(100 * time.Millisecond):
	}
}

// interrupt is stubbed everywhere else, so its real body — the one that runs in
// front of a developer — would otherwise never be executed. Notify keeps the
// signal from being fatal to the test binary, which is what cmd's signal
// context does to it in production.
func TestInterruptRaisesASignalTheProcessCanCatch(t *testing.T) {
	caught := make(chan os.Signal, 1)
	signal.Notify(caught, os.Interrupt)
	t.Cleanup(func() { signal.Stop(caught) })

	interrupt()

	select {
	case <-caught:
	case <-time.After(2 * time.Second):
		t.Error("interrupt() raised no signal")
	}
}
