package mountplan

import (
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/config"
)

// TestMergeShareKeepsToolOnHostRoot: under a profile root, a --share token
// keeps that tool's mount on the shared ~/.toolbox/ source while the rest are
// retargeted into the profile.
func TestMergeShareKeepsToolOnHostRoot(t *testing.T) {
	cfg := config.Config{MountsRoot: "/custom/root"}

	merged, err := Merge(&cfg, "gh")
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	if got := findMount(merged, "gh").Source; got != "~/.toolbox/gh" {
		t.Errorf("shared gh Source = %q, want %q (kept on host root)", got, "~/.toolbox/gh")
	}
	if got := findMount(merged, "claude").Source; got != "/custom/root/.claude" {
		t.Errorf("unshared claude Source = %q, want %q (retargeted)", got, "/custom/root/.claude")
	}
}

// TestMergeSharePrefixCoversSplitMounts: a single token covers a tool whose
// state splits across sibling mounts (cf → cf-auth/cf-config).
func TestMergeSharePrefixCoversSplitMounts(t *testing.T) {
	cfg := config.Config{MountsRoot: "/custom/root"}

	merged, err := Merge(&cfg, "cf")
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	for _, name := range []string{"cf-auth", "cf-config"} {
		got := findMount(merged, name).Source
		if !strings.HasPrefix(got, "~/.toolbox/") {
			t.Errorf("shared %q Source = %q, want kept under ~/.toolbox/", name, got)
		}
	}
}

// TestMergeShareBridgeKeepsInfraOnHostRoot: "bridge" (added automatically for
// profiles by cmd) keeps all three bridge mounts on the host root so the
// daemon token/socket stay reachable.
func TestMergeShareBridgeKeepsInfraOnHostRoot(t *testing.T) {
	cfg := config.Config{MountsRoot: "/custom/root"}

	merged, err := Merge(&cfg, "bridge")
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	for _, name := range []string{"bridge", "bridge-legacy", "bridge-run"} {
		got := findMount(merged, name).Source
		if !strings.HasPrefix(got, "~/.toolbox/") {
			t.Errorf("shared %q Source = %q, want kept under ~/.toolbox/", name, got)
		}
	}
}

// TestMergeShareUnknownErrors: a --share token matching no shareable mount
// fails loudly rather than silently isolating everything.
func TestMergeShareUnknownErrors(t *testing.T) {
	cfg := config.Config{MountsRoot: "/custom/root"}

	if _, err := Merge(&cfg, "ghh"); err == nil {
		t.Fatal("Merge with unknown --share token: want error, got nil")
	}
}

// TestMergeShareSSHNotSelectable: ssh/gitconfig are host-symlinked identity
// mounts, never selectable via --share.
func TestMergeShareSSHNotSelectable(t *testing.T) {
	cfg := config.Config{MountsRoot: "/custom/root"}

	for _, name := range []string{"ssh", "gitconfig"} {
		if _, err := Merge(&cfg, name); err == nil {
			t.Errorf("Merge with --share %q: want error (not shareable), got nil", name)
		}
	}
}

// TestMergeProfileSSHStaysHostSymlink: under a profile root, ssh/gitconfig
// keep their host SymlinkFrom target even though the source path is retargeted
// — so git/SSH identity stays shared with the host.
func TestMergeProfileSSHStaysHostSymlink(t *testing.T) {
	cfg := config.Config{MountsRoot: "~/.toolbox/profiles/work"}

	merged, err := Merge(&cfg)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	if got := findMount(merged, "ssh").SymlinkFrom; got != "~/.ssh" {
		t.Errorf("ssh SymlinkFrom = %q, want %q (host-shared under profile)", got, "~/.ssh")
	}
	if got := findMount(merged, "gitconfig").SymlinkFrom; got != "~/.gitconfig" {
		t.Errorf("gitconfig SymlinkFrom = %q, want %q (host-shared under profile)", got, "~/.gitconfig")
	}
}

func TestShareCovers(t *testing.T) {
	cases := []struct {
		shared []string
		name   string
		want   bool
	}{
		{[]string{"gh"}, "gh", true},
		{[]string{"cf"}, "cf-auth", true},
		{[]string{"cf"}, "cf-config", true},
		{[]string{"rtk"}, "rtk", true},
		{[]string{"rtk"}, "rtk-data", true},
		{[]string{"gh"}, "ghost", false}, // prefix match requires a '-' boundary
		{[]string{"gh"}, "claude", false},
		{nil, "gh", false},
	}
	for _, c := range cases {
		if got := shareCovers(c.shared, c.name); got != c.want {
			t.Errorf("shareCovers(%v, %q) = %v, want %v", c.shared, c.name, got, c.want)
		}
	}
}
