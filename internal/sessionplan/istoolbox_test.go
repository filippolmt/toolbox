package sessionplan_test

import (
	"testing"

	"github.com/filippolmt/toolbox/internal/sessionplan"
)

func TestIsToolboxContainerName(t *testing.T) {
	cases := map[string]bool{
		"toolbox":                 true, // legacy singleton
		"toolbox-myproj-a1b2c3d4": true, // workspace-hash form
		"toolbox-named-infra":     true, // named-shell form
		"toolbox-":                true, // prefix alone still ours
		"":                        false,
		"toolboxx":                false, // no separator, not the prefix
		"my-toolbox":              false, // prefix not at start
		"redis":                   false,
	}
	for name, want := range cases {
		if got := sessionplan.IsToolboxContainerName(name); got != want {
			t.Errorf("IsToolboxContainerName(%q) = %v, want %v", name, got, want)
		}
	}
}
