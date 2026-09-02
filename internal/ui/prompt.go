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
// than an io.Reader: cancelling a read that nobody answered needs a file
// descriptor, and without the cancel the abandoned read would still be waiting
// on stdin when the session attaches — stealing the developer's first
// keystroke from the container.
var (
	promptIn  *os.File  = os.Stdin
	promptOut io.Writer = os.Stderr
)

// countdownTick is how often the remaining time is redrawn. A var, not a
// const, so a prompt test runs in milliseconds instead of seconds — the same
// seam imageprefetch uses for its own alarm.
var countdownTick = time.Second

// answerHead bounds what is kept of the answer. Only the first word decides,
// so a pasted paragraph is read to its newline and remembered no further.
const answerHead = 16

// Askable reports whether there is a developer at the other end to ask. False
// under a pipe, a CI runner or any other tty-less invocation, where a question
// has no answer and waiting for one is pure latency.
func Askable() bool { return term.IsTerminal(int(promptIn.Fd())) }

// ConfirmCountdown asks question and answers yes for a developer who is not
// looking: an explicit "n" declines, a bare Return accepts, and so does the
// window elapsing. The remaining seconds are redrawn on their own line
// because silence is indistinguishable from a hang — a developer who looks up
// to find a download already running should be able to see why.
//
// Yes on every uncertainty (an unreadable stdin, a closed one, an answer
// nobody can parse): the caller only asks when it is about to do something it
// would otherwise have done unconditionally.
func ConfirmCountdown(question string, window time.Duration) bool {
	reader, err := cancelreader.NewReader(promptIn)
	if err != nil {
		return true
	}
	defer func() { _ = reader.Close() }()

	answered := make(chan bool, 1)
	go func() { answered <- accepted(reader) }()

	deadline := time.NewTimer(window)
	defer deadline.Stop()
	ticker := time.NewTicker(countdownTick)
	defer ticker.Stop()

	left := window
	render(question, left)
	for {
		select {
		case answer := <-answered:
			erase()
			return answer
		case <-ticker.C:
			left -= countdownTick
			render(question, left)
		case <-deadline.C:
			// Cancel rather than abandon: the read is on the same stdin the
			// session is about to attach to.
			reader.Cancel()
			erase()
			return true
		}
	}
}

// render redraws the question in place. Carriage return rather than a fresh
// line, so a five-second countdown does not scroll five lines past whatever
// the developer was reading.
func render(question string, left time.Duration) {
	if left < 0 {
		left = 0
	}
	_, _ = fmt.Fprintf(promptOut, "\r  %s [Y/n] (%ds) ", question, int(left/time.Second))
}

// erase clears the countdown line once it has been answered, so a question
// that is no longer being asked does not stay on screen above whatever the
// answer set in motion. Written to the same line the render owns, never as a
// scroll — the prompt occupies one line for its whole life.
func erase() { _, _ = fmt.Fprint(promptOut, "\r\x1b[K") }

// accepted reads one line and reports whether it is a yes. Byte at a time
// rather than through a bufio.Reader: a buffered read-ahead would swallow
// input typed after the answer, which belongs to the session that is about to
// attach to the same stdin.
//
// The whole line is consumed even though only its head decides — leaving the
// tail on stdin would hand the session a fragment of an answer as its first
// keystrokes, which is the same leak read-ahead would cause.
func accepted(r io.Reader) bool {
	var line []byte
	buf := make([]byte, 1)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if buf[0] == '\n' {
				break
			}
			if len(line) < answerHead {
				line = append(line, buf[0])
			}
		}
		if err != nil {
			break
		}
	}
	return !strings.HasPrefix(strings.ToLower(strings.TrimSpace(string(line))), "n")
}
