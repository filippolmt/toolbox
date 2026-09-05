package configedit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/filippolmt/toolbox/internal/config"
)

func TestParseWhere(t *testing.T) {
	if w, err := ParseWhere("global"); err != nil || w != WhereGlobal {
		t.Errorf("ParseWhere(global) = %v, %v", w, err)
	}
	if w, err := ParseWhere("local"); err != nil || w != WhereLocal {
		t.Errorf("ParseWhere(local) = %v, %v", w, err)
	}
	if _, err := ParseWhere("project"); err == nil {
		t.Error("ParseWhere(project) must fail")
	}
}

func TestResolveGlobal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got, err := Resolve(WhereGlobal, t.TempDir())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if want := filepath.Join(home, ".toolbox.yaml"); got != want {
		t.Errorf("Resolve(global) = %q, want %q", got, want)
	}
}

func TestResolveLocalWalkedUp(t *testing.T) {
	workspace := t.TempDir()
	want := filepath.Join(workspace, ".toolbox.yaml")
	if err := os.WriteFile(want, []byte("# project\n"), 0o600); err != nil {
		t.Fatalf("write project: %v", err)
	}
	nested := filepath.Join(workspace, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	t.Setenv("HOME", t.TempDir())

	got, err := Resolve(WhereLocal, nested)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != want {
		t.Errorf("Resolve(local) must patch the walked-up file: got %q, want %q", got, want)
	}
}

func TestResolveLocalNoProjectFile(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("HOME", t.TempDir())

	got, err := Resolve(WhereLocal, cwd)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if want := filepath.Join(cwd, ".toolbox.yaml"); got != want {
		t.Errorf("Resolve(local) without walked-up file = %q, want %q", got, want)
	}
}

// TestWorkspaceOnlyKeys pins the guarded set and holds every member to being a
// real Config Schema key: a typo or a renamed key would leave the global layer
// silently un-guarded, which is the failure this predicate exists to prevent.
func TestWorkspaceOnlyKeys(t *testing.T) {
	if !WorkspaceOnlyKey("sdd") {
		t.Error("sdd must be workspace-only: sentinel, artefacts and fence are all workspace-anchored")
	}
	if WorkspaceOnlyKey("pull") {
		t.Error("pull is a per-user preference and must stay writable in the global layer")
	}
	schema := make(map[string]bool, len(config.SchemaKeys()))
	for _, k := range config.SchemaKeys() {
		schema[k] = true
	}
	for key := range workspaceOnlyKeys {
		if !schema[key] {
			t.Errorf("workspace-only key %q is not a config.SchemaKeys() entry", key)
		}
	}
}
