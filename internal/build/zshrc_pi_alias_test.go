package build

import (
	"strings"
	"testing"
)

// TestZshrcPiAliasRefreshesExtensionsWithoutBlockingPi pins the interactive
// alias that keeps pi's extensions current. Two invariants, both silent when
// broken: `command pi` on BOTH halves, or the alias recurses into itself; and
// the update must not gate the agent behind its own success — chained with &&,
// an offline shell (or any upstream hiccup) leaves the developer with no pi at
// all, which is a far worse failure than a stale extension.
func TestZshrcPiAliasRefreshesExtensionsWithoutBlockingPi(t *testing.T) {
	b, err := Assets.ReadFile("assets/zshrc.sh")
	if err != nil {
		t.Fatalf("read zshrc.sh: %v", err)
	}
	s := string(b)
	const want = `alias pi='command pi update --extensions >/dev/null; command pi'`
	if !strings.Contains(s, want) {
		t.Errorf("zshrc.sh is missing the pi alias %q", want)
	}
	if strings.Contains(s, `pi update --extensions >/dev/null && command pi`) {
		t.Error("the pi alias chains on &&: a failed update would leave the shell with no pi")
	}
}
