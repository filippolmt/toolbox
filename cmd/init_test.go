package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// chdirTemp swaps CWD to a fresh temp dir, restored on cleanup.
func chdirTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWD) })
	return dir
}

func TestInitWritesAnnotatedYAML(t *testing.T) {
	dir := chdirTemp(t)
	t.Cleanup(func() { initForce = false })

	cmd := initCmd
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := runInit(cmd, nil); err != nil {
		t.Fatalf("runInit: %v", err)
	}

	path := filepath.Join(dir, ".toolbox.yaml")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	got := string(body)
	for _, want := range []string{"shell:", "mounts_root:", "inherit_host_auth", "claude", "Precedence"} {
		if !strings.Contains(got, want) {
			t.Errorf("written yaml missing %q\n---\n%s", want, got)
		}
	}
}

func TestInitRefusesOverwriteWithoutForce(t *testing.T) {
	dir := chdirTemp(t)
	t.Cleanup(func() { initForce = false })

	path := filepath.Join(dir, ".toolbox.yaml")
	if err := os.WriteFile(path, []byte("# user content\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cmd := initCmd
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	err := runInit(cmd, nil)
	if err == nil {
		t.Fatal("expected error when target exists without --force")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("error should mention --force, got %v", err)
	}

	body, _ := os.ReadFile(path)
	if string(body) != "# user content\n" {
		t.Errorf("file should remain untouched, got %q", string(body))
	}
}

func TestInitForceOverwrites(t *testing.T) {
	dir := chdirTemp(t)
	t.Cleanup(func() { initForce = false })

	path := filepath.Join(dir, ".toolbox.yaml")
	if err := os.WriteFile(path, []byte("# user content\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	initForce = true
	cmd := initCmd
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := runInit(cmd, nil); err != nil {
		t.Fatalf("runInit --force: %v", err)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(body), "Precedence") {
		t.Errorf("file should be overwritten with template, got %q", string(body))
	}
}
