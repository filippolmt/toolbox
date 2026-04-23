package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/version"
)

// TestVersionCommandWritesToCmdOut verifies the `version` subcommand honours
// cmd.OutOrStdout so tests (and anything else that overrides the command's
// output) can capture the version line instead of scraping os.Stdout.
func TestVersionCommandWritesToCmdOut(t *testing.T) {
	var buf bytes.Buffer
	versionCmd.SetOut(&buf)
	t.Cleanup(func() { versionCmd.SetOut(nil) })

	versionCmd.Run(versionCmd, nil)

	got := buf.String()
	for _, want := range []string{"toolbox ", version.Version, version.Commit, version.Date} {
		if !strings.Contains(got, want) {
			t.Errorf("version output %q should contain %q", got, want)
		}
	}
}
