package mountplan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/catalog"
	"github.com/filippolmt/toolbox/internal/config"
)

// seedHostAuthPaths materialises every catalog HostAuthMount.HostPath under
// home so applyInheritHostAuth's pre-stat passes. Returns the resolved home.
func seedHostAuthPaths(t *testing.T, keys []string) string {
	t.Helper()
	home := t.TempDir()
	for _, k := range keys {
		entry, ok := catalog.Find(k)
		if !ok || entry.HostAuthMount == nil {
			continue
		}
		p := entry.HostAuthMount.HostPath
		if strings.HasPrefix(p, "~/") {
			p = filepath.Join(home, p[2:])
		}
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatalf("mkdir host auth seed %q: %v", p, err)
		}
	}
	return home
}

func TestApplyInheritHostAuthEmpty(t *testing.T) {
	base := defaults()
	got, err := applyInheritHostAuth(base, nil, "")
	if err != nil {
		t.Fatalf("nil keys err: %v", err)
	}
	if len(got) != len(base) {
		t.Errorf("nil keys must leave default set unchanged: got %d mounts, want %d", len(got), len(base))
	}

	got, err = applyInheritHostAuth(base, []string{}, "")
	if err != nil {
		t.Fatalf("empty keys err: %v", err)
	}
	if len(got) != len(base) {
		t.Errorf("empty keys must leave default set unchanged: got %d mounts, want %d", len(got), len(base))
	}
}

func TestApplyInheritHostAuthReplacesIsolatedMount(t *testing.T) {
	home := seedHostAuthPaths(t, []string{"gh"})
	base := defaults()
	got, err := applyInheritHostAuth(base, []string{"gh"}, home)
	if err != nil {
		t.Fatalf("applyInheritHostAuth err: %v", err)
	}

	var ghCount int
	var ghMount config.Mount
	for _, m := range got {
		if m.Name == "gh" {
			ghCount++
			ghMount = m
		}
	}
	if ghCount != 1 {
		t.Fatalf("expected exactly one mount named gh, got %d", ghCount)
	}
	if ghMount.Source != "~/.config/gh" {
		t.Errorf("Source = %q, want %q", ghMount.Source, "~/.config/gh")
	}
	if ghMount.Target != "/home/toolbox/.config/gh" {
		t.Errorf("Target = %q, want %q", ghMount.Target, "/home/toolbox/.config/gh")
	}
	if ghMount.ReadOnly {
		t.Error("ReadOnly = true, want false (host inheritance is RW so CLIs can refresh tokens / write session state)")
	}
}

func TestApplyInheritHostAuthMultiple(t *testing.T) {
	keys := []string{"gh", "gcloud", "docker"}
	home := seedHostAuthPaths(t, keys)
	base := defaults()
	got, err := applyInheritHostAuth(base, keys, home)
	if err != nil {
		t.Fatalf("applyInheritHostAuth err: %v", err)
	}

	for _, k := range keys {
		var found *config.Mount
		for i := range got {
			if got[i].Name == k {
				found = &got[i]
				break
			}
		}
		if found == nil {
			t.Errorf("mount %q missing after inherit_host_auth", k)
			continue
		}
		if found.ReadOnly {
			t.Errorf("mount %q ReadOnly = true, want false", k)
		}
	}
}

func TestApplyInheritHostAuthNoTargetCollision(t *testing.T) {
	keys := []string{"gh", "gcloud", "docker", "claude", "codex", "azure", "oci"}
	home := seedHostAuthPaths(t, keys)
	base := defaults()
	got, err := applyInheritHostAuth(base, keys, home)
	if err != nil {
		t.Fatalf("applyInheritHostAuth err: %v", err)
	}

	seen := make(map[string]string)
	for _, m := range got {
		if prev, ok := seen[m.Target]; ok {
			t.Errorf("two mounts share Target %q: %q and %q", m.Target, prev, m.Name)
		}
		seen[m.Target] = m.Name
	}
}

func TestApplyInheritHostAuthIgnoresUnknown(t *testing.T) {
	base := defaults()
	got, err := applyInheritHostAuth(base, []string{"no-such-tool"}, "")
	if err != nil {
		t.Fatalf("unknown key should be a no-op, got err: %v", err)
	}
	if len(got) != len(base) {
		t.Errorf("unknown key must leave default set unchanged, got %d mounts (want %d)", len(got), len(base))
	}
}

// TestApplyInheritHostAuthMissingHostPath asserts the pre-stat catches a
// host path that does not exist — silent soft-skip would leave the
// container with no credential mount at all.
func TestApplyInheritHostAuthMissingHostPath(t *testing.T) {
	emptyHome := t.TempDir() // no ~/.config/gh seeded
	base := defaults()
	_, err := applyInheritHostAuth(base, []string{"gh"}, emptyHome)
	if err == nil {
		t.Fatal("missing host path must produce an error, got nil")
	}
	if !strings.Contains(err.Error(), "gh") || !strings.Contains(err.Error(), "not accessible") {
		t.Errorf("error should name the key and reason, got: %v", err)
	}
}
