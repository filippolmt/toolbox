package ui

import (
	"charm.land/huh/v2/spinner"
)

// WithSpinner esegue un'azione mostrando uno spinner (D-11 via huh v2).
func WithSpinner(title string, action func()) error {
	return spinner.New().Title(title).Action(action).Run()
}
