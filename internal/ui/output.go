package ui

import (
	"fmt"

	"charm.land/lipgloss/v2"
)

// Stili output colorati (D-10) coerenti con entrypoint.sh Phase 1.
var (
	successStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#00FF00"))
	warningStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFAA00"))
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0000"))
	infoStyle    = lipgloss.NewStyle().Bold(true)
)

// Success stampa un messaggio di successo con prefisso "OK: ".
func Success(msg string) {
	fmt.Println(successStyle.Render("  OK: " + msg))
}

// Warning stampa un messaggio di avviso con prefisso "WARN: ".
func Warning(msg string) {
	fmt.Println(warningStyle.Render("  WARN: " + msg))
}

// Error stampa un messaggio di errore con prefisso "FAIL: ".
func Error(msg string) {
	fmt.Println(errorStyle.Render("  FAIL: " + msg))
}

// Info stampa un messaggio informativo senza prefisso.
func Info(msg string) {
	fmt.Println(infoStyle.Render("  " + msg))
}
