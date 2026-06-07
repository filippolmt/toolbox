package configedit

import (
	"os"
	"path/filepath"
	"testing"
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
