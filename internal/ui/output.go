package ui

import (
	"fmt"

	"charm.land/lipgloss/v2"
)

// Colored output styles (D-10), consistent with entrypoint.sh Phase 1.
var (
	successStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#00FF00"))
	warningStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFAA00"))
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0000"))
	infoStyle    = lipgloss.NewStyle().Bold(true)
)

// Success prints a success message with an "OK: " prefix.
func Success(msg string) {
	fmt.Println(successStyle.Render("  OK: " + msg))
}

// Warning prints a warning message with a "WARN: " prefix.
func Warning(msg string) {
	fmt.Println(warningStyle.Render("  WARN: " + msg))
}

// Error prints an error message with a "FAIL: " prefix.
func Error(msg string) {
	fmt.Println(errorStyle.Render("  FAIL: " + msg))
}

// Info prints an informational message with no prefix.
func Info(msg string) {
	fmt.Println(infoStyle.Render("  " + msg))
}
