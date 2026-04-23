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
