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
	fmt.Fprintln(os.Stderr, infoStyle.Render("  "+msg))
}

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
