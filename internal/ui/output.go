package ui

import (
	"fmt"
	"os"

	"charm.land/lipgloss/v2"
)

// Colored output styles (D-10), consistent with entrypoint.sh Phase 1.
var (
	successStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#00FF00"))
	warningStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFAA00"))
	infoStyle    = lipgloss.NewStyle().Bold(true)
)

// Diagnostic output is written to stderr so that stdout stays reserved for
// program output (pipeable). Logs on stdout corrupt the data stream for the
// next command in a pipeline.

// Success prints a success message with an "OK: " prefix.
func Success(msg string) {
	fmt.Fprintln(os.Stderr, successStyle.Render("  OK: "+msg))
}

// Warning prints a warning message with a "WARN: " prefix.
func Warning(msg string) {
	fmt.Fprintln(os.Stderr, warningStyle.Render("  WARN: "+msg))
}

// Info prints an informational message with no prefix.
func Info(msg string) {
	fmt.Fprintln(os.Stderr, infoLine(msg))
}

// infoLine renders one info line. Extracted so Info and InfoAsyncf cannot
// drift on the shared indent and style while differing only in how they
// terminate the line.
func infoLine(msg string) string { return infoStyle.Render("  " + msg) }

// Successf, Warningf and Infof are the printf forms. Callers interpolating a
// value use these rather than wrapping the message in fmt.Sprintf themselves —
// and rather than concatenating, which duplicates the shared prefix literal
// ("Container %s …", used four times in internal/teardown) across call sites.

// Successf prints a formatted success message with an "OK: " prefix.
func Successf(format string, a ...any) { Success(fmt.Sprintf(format, a...)) }

// Warningf prints a formatted warning message with a "WARN: " prefix.
func Warningf(format string, a ...any) { Warning(fmt.Sprintf(format, a...)) }

// Infof prints a formatted informational message with no prefix.
func Infof(format string, a ...any) { Info(fmt.Sprintf(format, a...)) }

// InfoAsyncf is Infof for a line a *background* act emits — one written while
// an interactive shell may already be attached. Such a shell puts the tty in
// raw mode (term.MakeRaw clears ONLCR), where the bare LF the other writers
// here end on drops a line without returning the carriage, so every later
// line starts one column further right until something repaints. This one
// returns the carriage itself, which costs a redundant CR on a cooked tty and
// nothing else.
//
// Not folded into Info: ui writes to stderr, and stderr is redirected to
// files often enough that CRLF-terminating every diagnostic line in the CLI
// would be a worse trade than naming the one case that needs it.
func InfoAsyncf(format string, a ...any) {
	// Written whole rather than through Info: the terminator has to be the
	// last thing on the wire, and Info's Fprintln would put the style's reset
	// sequence between the carriage return and the newline.
	fmt.Fprint(os.Stderr, infoLine(fmt.Sprintf(format, a...))+"\r\n")
}
