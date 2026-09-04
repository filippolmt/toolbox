package container

import (
	"testing"

	"github.com/filippolmt/toolbox/internal/fsx"
)

// sandboxHome returns a Host rooted at a fresh temp dir and points the
// process home at the same directory.
//
// The $HOME half is still load-bearing and will stay until the image seam is
// threaded too: this package's plans reach imagepull, whose pull-cache marker
// resolves its own home, and an unsandboxed run would write it under the
// developer's real ~/.toolbox. The Host half is what the planner reads, so
// both must name the same directory or a test would assert against one home
// while the plan was built for another.
func sandboxHome(t *testing.T) fsx.Host {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return fsx.Host{Home: home}
}
