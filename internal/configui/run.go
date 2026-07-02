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

// Run launches the config UI for the given working directory. It gates on an
// interactive TTY first and makes no changes when the gate fails. The UI edits
// the Global and Repo layer files only; the global --config override is not an
// editable layer and is intentionally not consulted here.
func Run(cwd string) error {
	if !isInteractive() {
		return ErrNotTTY
	}
	p := tea.NewProgram(New(cwd)) // alt screen is set on the View (v2)
	_, err := p.Run()
	return err
}

func isInteractive() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}
