package configui

import (
	"errors"
	"os"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/term"
)

// ErrNotTTY is returned when config ui is invoked without an interactive
// terminal on both stdin and stdout, so it degrades predictably in CI/pipes
// instead of hanging or emitting control codes.
var ErrNotTTY = errors.New("config ui requires an interactive terminal (stdin and stdout must be a TTY)")

// Run launches the config UI for the given working directory and optional
// explicit --config override. It gates on an interactive TTY first and makes no
// changes when the gate fails.
func Run(cwd, explicit string) error {
	if !isInteractive() {
		return ErrNotTTY
	}
	p := tea.NewProgram(New(cwd, explicit)) // alt screen is set on the View (v2)
	_, err := p.Run()
	return err
}

func isInteractive() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}
