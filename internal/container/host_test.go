package container

import (
	"testing"

	"github.com/filippolmt/toolbox/internal/fsx"
)

// planHost returns a Host rooted at a fresh temp dir.
//
// It used to point the process $HOME at the same directory too: this package's
// plans reach imagepull, whose pull-cache marker resolved its own home, so an
// unsandboxed run wrote under the developer's real ~/.toolbox. imagepull now
// takes the session's resolved state dir, and nothing this package reaches
// reads the environment for a home any more — so the declared Host is the
// whole fixture.
func planHost(t *testing.T) fsx.Host {
	t.Helper()
	return fsx.Host{Home: t.TempDir()}
}
