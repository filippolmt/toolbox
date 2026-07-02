package mountplan

import (
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/config"
)

// TestMergeShareKeepsToolOnHostRoot: under a profile, a --share token keeps
// that tool's mount on the shared ~/.toolbox/ source while the rest are
// retargeted into the profile.
func TestMergeShareKeepsToolOnHostRoot(t *testing.T) {
	merged, err := Merge(&config.Config{}, &Profile{Name: "work", Share: []string{"gh"}})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	if got := findMount(merged, "gh").Source; got != "~/.toolbox/gh" {
		t.Errorf("shared gh Source = %q, want %q (kept on host root)", got, "~/.toolbox/gh")
	}
	if got := findMount(merged, "claude").Source; got != "~/.toolbox/profiles/work/.claude" {
		t.Errorf("unshared claude Source = %q, want %q (retargeted)", got, "~/.toolbox/profiles/work/.claude")
	}
}

// TestMergeSharePrefixCoversSplitMounts: a single token covers a tool whose
// state splits across sibling mounts (cf → cf-auth/cf-config).
func TestMergeSharePrefixCoversSplitMounts(t *testing.T) {
	merged, err := Merge(&config.Config{}, &Profile{Name: "work", Share: []string{"cf"}})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	for _, name := range []string{"cf-auth", "cf-config"} {
		got := findMount(merged, name).Source
		if !strings.HasPrefix(got, "~/.toolbox/") || strings.Contains(got, "profiles/") {
			t.Errorf("shared %q Source = %q, want kept on host ~/.toolbox/ root", name, got)
		}
	}
}

// TestMergeProfileKeepsBridgeOnHostRoot: a profile always keeps the bridge
// mounts on the host root (EffectiveShare appends "bridge") so the daemon
// token/socket stay reachable — even with no explicit --share.
func TestMergeProfileKeepsBridgeOnHostRoot(t *testing.T) {
	merged, err := Merge(&config.Config{}, &Profile{Name: "work"})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	for _, name := range []string{"bridge", "bridge-legacy", "bridge-run"} {
		got := findMount(merged, name).Source
		if strings.Contains(got, "profiles/") {
			t.Errorf("bridge mount %q Source = %q, want kept on host root", name, got)
		}
	}
}

// TestMergeShareUnknownErrors: a --share token matching no shareable mount
// fails loudly rather than silently isolating everything.
func TestMergeShareUnknownErrors(t *testing.T) {
	if _, err := Merge(&config.Config{}, &Profile{Name: "work", Share: []string{"ghh"}}); err == nil {
		t.Fatal("Merge with unknown --share token: want error, got nil")
	}
}

// TestMergeShareSSHNotSelectable: ssh/gitconfig are host-symlinked identity
// mounts, never selectable via --share.
func TestMergeShareSSHNotSelectable(t *testing.T) {
	for _, name := range []string{"ssh", "gitconfig"} {
		if _, err := Merge(&config.Config{}, &Profile{Name: "work", Share: []string{name}}); err == nil {
			t.Errorf("Merge with --share %q: want error (not shareable), got nil", name)
		}
	}
}

// TestMergeProfileSSHStaysHostSymlink: under a profile, ssh/gitconfig keep
// their host SymlinkFrom target even though the source path is retargeted — so
// git/SSH identity stays shared with the host.
func TestMergeProfileSSHStaysHostSymlink(t *testing.T) {
	merged, err := Merge(&config.Config{}, &Profile{Name: "work"})
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

// TestMergeProfileOverridesMountsRoot: a profile wins over a config-level
// mounts_root for the invocation.
func TestMergeProfileOverridesMountsRoot(t *testing.T) {
	cfg := config.Config{MountsRoot: "~/other-toolbox"}

	merged, err := Merge(&cfg, &Profile{Name: "work"})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	if got := findMount(merged, "claude").Source; got != "~/.toolbox/profiles/work/.claude" {
		t.Errorf("claude Source = %q, want profile root to win over config mounts_root", got)
	}
}

// TestMergeShareEmptyTokenErrors: an empty --share token (e.g. `--share a,,b`)
// is rejected explicitly, not silently ignored.
func TestMergeShareEmptyTokenErrors(t *testing.T) {
	if _, err := Merge(&config.Config{}, &Profile{Name: "work", Share: []string{""}}); err == nil {
		t.Fatal("Merge with empty --share token: want error, got nil")
	}
}

// TestContainerDiscriminator: distinct profiles, and distinct --share sets
// within a profile, yield distinct discriminators; token order does not matter;
// nil is empty.
func TestContainerDiscriminator(t *testing.T) {
	if got := ContainerDiscriminator(nil); got != "" {
		t.Errorf("nil discriminator = %q, want empty", got)
	}
	work := ContainerDiscriminator(&Profile{Name: "work"})
	personal := ContainerDiscriminator(&Profile{Name: "personal"})
	if work == personal {
		t.Errorf("distinct profiles share a discriminator: %q", work)
	}
	shareGH := ContainerDiscriminator(&Profile{Name: "work", Share: []string{"gh"}})
	if shareGH == work {
		t.Errorf("--share change did not alter discriminator: %q", shareGH)
	}
	a := ContainerDiscriminator(&Profile{Name: "work", Share: []string{"gh", "docker"}})
	b := ContainerDiscriminator(&Profile{Name: "work", Share: []string{"docker", "gh"}})
	if a != b {
		t.Errorf("share order changed discriminator: %q != %q", a, b)
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
