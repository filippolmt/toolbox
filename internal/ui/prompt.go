package ui

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/muesli/cancelreader"
	"golang.org/x/term"
)

// promptIn and promptOut are the prompt's two ends of the terminal, named so
// the tests can drive them through a pipe. Input must be an *os.File rather
// than an io.Reader: cancelling a read needs a file descriptor, and every path
// out of the question ends in one — an abandoned read would still be waiting
// on stdin when the session attaches, stealing the developer's first keystroke
// from the container.
var (
	promptIn  *os.File  = os.Stdin
	promptOut io.Writer = os.Stderr
)

// countdownTick is how often the remaining time is redrawn. A var, not a
// const, so a prompt test runs in milliseconds instead of seconds — the same
// seam imageprefetch uses for its own alarm.
var countdownTick = time.Second

// drainWindow is how long the answered prompt keeps swallowing input before it
// lets go of stdin. A developer who types "y" out of habit puts a Return behind
// it, and that Return is still on its way when the key has already decided:
// whatever is left unread becomes the first keystrokes of the session that
// attaches to the same stdin a moment later. Long enough for the tail of an
// answer, short enough to disappear into the container start it precedes.
var drainWindow = 20 * time.Millisecond

// answerHead bounds what is kept of the answer. Only the first word decides,
// so a pasted paragraph is read to its newline and remembered no further.
const answerHead = 16

// keyInterrupt is the byte a ctrl+c becomes once raw mode has taken the signal
// away from the terminal driver. Raising it again by hand is what keeps the
// question from being the one place in the session where ctrl+c does nothing.
const keyInterrupt = 0x03

// interrupt re-raises what raw mode swallowed. A var so a test can watch it
// fire without the test binary taking a SIGINT of its own.
var interrupt = func() {
	if p, err := os.FindProcess(os.Getpid()); err == nil {
		_ = p.Signal(os.Interrupt)
	}
}

// answer is one read of stdin: whether the download was accepted, and whether
// the developer asked for the whole command to stop rather than just this
// download. The two are separate because a ctrl+c has to do both — decline the
// download it is standing in front of, and abort the command behind it.
type answer struct{ yes, interrupted bool }

// Askable reports whether there is a developer at the other end to ask. False
// under a pipe, a CI runner or any other tty-less invocation, where a question
// has no answer and waiting for one is pure latency.
func Askable() bool { return term.IsTerminal(int(promptIn.Fd())) }

// Elapsed is the answer a window that runs out gives, and the default the
// question shows while it runs. It belongs to the caller because only the
// caller knows what a yes costs there: a download nobody objected to may
// start on its own, while an answer that also destroys something may not be
// given by a clock.
type Elapsed bool

const (
	// ElapsedYes: an unanswered window accepts, which a caller may nominate
	// only when it is offering something it would otherwise have done
	// unconditionally.
	ElapsedYes Elapsed = true
	// ElapsedNo: an unanswered window declines, for a yes whose cost nobody
	// but a developer may accept.
	ElapsedNo Elapsed = false
)

// hint renders the default as the pair of letters the question offers, the
// capital one being what the elapsing window will choose.
func (e Elapsed) hint() string {
	if e == ElapsedYes {
		return "[Y/n]"
	}
	return "[y/N]"
}

// ConfirmCountdown asks question and answers for a developer who is not
// looking: a single "y" accepts, a single "n" declines, and everything else —
// a bare Return, an answer nobody can parse, the window elapsing — takes the
// caller's nominated default. One keypress is the whole answer — no Return
// behind it, which is why the terminal spends the question in raw mode.
// Reports the answer, and separately whether the developer interrupted: raw
// mode takes ctrl+c from the terminal driver, so the prompt raises it again
// itself and tells the caller, which has a session to abandon rather than a
// download to postpone.
//
// The remaining seconds are redrawn on their own line because silence is
// indistinguishable from a hang — a developer who looks up to find a download
// already running should be able to see why. The default is shown with them,
// because a developer who answers by walking away has to be able to read what
// that does.
//
// The default on every uncertainty too (an unreadable stdin, a closed one),
// which is the same rule as the elapsed window: nothing there is an answer.
func ConfirmCountdown(question string, window time.Duration, elapsed Elapsed) (yes, interrupted bool) {
	// Raw is what makes a single keypress an answer: under the terminal's
	// default line discipline the key is held in the driver until Return, so a
	// developer who answered would sit there watching a countdown they had
	// already stopped. A terminal that refuses is left as it is — the read
	// below still works, one Return later — so the error is not the caller's
	// to handle.
	restore, _, _ := RawTerminal(int(promptIn.Fd()))
	defer restore()

	reader, err := cancelreader.NewReader(promptIn)
	if err != nil {
		return bool(elapsed), false
	}

	reading := make(chan struct{})
	answered := make(chan answer, 1)
	go func() {
		defer close(reading)
		answered <- accepted(reader, elapsed)
		// Past the answer, every byte is the tail of it. Swallow them rather
		// than leave them on stdin — see drainWindow for whose keystrokes they
		// would otherwise become — until stopReading takes the reader away.
		_, _ = io.Copy(io.Discard, reader)
	}()
	defer stopReading(reader, reading)

	deadline := time.NewTimer(window)
	defer deadline.Stop()
	ticker := time.NewTicker(countdownTick)
	defer ticker.Stop()

	left := window
	render(question, left, elapsed)
	for {
		select {
		case a := <-answered:
			if a.interrupted {
				erase()
				// Restore before raising: nothing guarantees the signal is
				// handled rather than fatal, and a process that dies here
				// would leave the developer's terminal in raw mode. No drain
				// window either — ctrl+c is the last key of its session.
				restore()
				interrupt()
				return false, true
			}
			// Hold the terminal for as long as the tail of the answer needs to
			// arrive; the goroutine above is swallowing it.
			time.Sleep(drainWindow)
			erase()
			return a.yes, false
		case <-ticker.C:
			left -= countdownTick
			render(question, left, elapsed)
		case <-deadline.C:
			erase()
			return bool(elapsed), false
		}
	}
}

// stopReading ends the read and closes the reader, in that order and not the
// other: Close waits for a read in flight, and the read in flight is a
// deliberate one — the drain. Cancel is delivered to a reader that is blocked
// on input, so one landing between two reads is a cancel nobody received;
// asking again until the goroutine is actually gone is what makes the wait
// bounded rather than lucky.
func stopReading(r cancelreader.CancelReader, reading <-chan struct{}) {
	for {
		r.Cancel()
		select {
		case <-reading:
			_ = r.Close()
			return
		case <-time.After(time.Millisecond):
		}
	}
}

// render redraws the question in place. Carriage return rather than a fresh
// line, so a five-second countdown does not scroll five lines past whatever
// the developer was reading.
func render(question string, left time.Duration, elapsed Elapsed) {
	if left < 0 {
		left = 0
	}
	_, _ = fmt.Fprintf(promptOut, "\r  %s %s (%ds) ", question, elapsed.hint(), int(left/time.Second))
}

// erase clears the countdown line once it has been answered, so a question
// that is no longer being asked does not stay on screen above whatever the
// answer set in motion. Written to the same line the render owns, never as a
// scroll — the prompt occupies one line for its whole life.
func erase() { _, _ = fmt.Fprint(promptOut, "\r\x1b[K") }

// accepted reads the answer. A decisive first key — y or n, either case — is
// the whole answer and ends the read there, which is what a one-key question
// promises. Anything else is read to the end of its line and judged by its
// head, so a pasted word still answers. A terminator is kept and trimmed off
// rather than branched on: a bare Return is an empty answer, and an empty
// answer is the default — the same one the elapsing window gives, since
// neither is a developer saying anything.
//
// Byte at a time rather than through a bufio.Reader: a buffered read-ahead
// would swallow input typed after the answer, which belongs to the session
// that is about to attach to the same stdin.
func accepted(r io.Reader, elapsed Elapsed) answer {
	var line []byte
	buf := make([]byte, 1)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			c := buf[0]
			if c == keyInterrupt {
				return answer{interrupted: true}
			}
			if len(line) < answerHead {
				line = append(line, c)
			}
			if ends(c, len(line) == 1) {
				break
			}
		}
		if err != nil {
			break
		}
	}
	// Only the two letters the question offers decide; everything else,
	// emptiness included, leaves the default standing.
	yes := bool(elapsed)
	switch head := strings.ToLower(strings.TrimSpace(string(line))); {
	case strings.HasPrefix(head, "y"):
		yes = true
	case strings.HasPrefix(head, "n"):
		yes = false
	}
	return answer{yes: yes}
}

// ends reports whether c closes the answer: a line terminator always, and a
// decisive key when it is the first thing read — after that it may be one
// letter of a longer word, which only its line can settle.
func ends(c byte, first bool) bool {
	return c == '\n' || c == '\r' || (first && decisive(c))
}

// decisive reports whether a key answers the question on its own. Only the two
// the prompt offers.
func decisive(c byte) bool {
	switch c {
	case 'y', 'Y', 'n', 'N':
		return true
	}
	return false
}
