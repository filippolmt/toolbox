package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/config"
	"gopkg.in/yaml.v3"
)

func TestResolveShellWorkspaceUsesConfiguredNamedShell(t *testing.T) {
	dir := t.TempDir()
	cfg = &config.Config{
		Shells: map[string]config.NamedShell{
			"infra": {Path: dir},
		},
	}
	t.Cleanup(func() { cfg = nil })

	ws, name, err := resolveShellWorkspace([]string{"infra"}, false, "")
	if err != nil {
		t.Fatalf("resolveShellWorkspace: %v", err)
	}
	if ws != dir {
		t.Fatalf("workspace = %q, want %q", ws, dir)
	}
	if name != "infra" {
		t.Fatalf("name = %q, want infra", name)
	}
}

func TestResolveShellWorkspaceMissingNameNonInteractiveShowsHint(t *testing.T) {
	cfg = &config.Config{}
	t.Cleanup(func() { cfg = nil })

	orig := shellStdinIsTerminal
	shellStdinIsTerminal = func(int) bool { return false }
	t.Cleanup(func() { shellStdinIsTerminal = orig })

	_, _, err := resolveShellWorkspace([]string{"qa"}, false, "")
	if err == nil {
		t.Fatal("expected error for missing shell without --create")
	}
	msg := err.Error()
	if !strings.Contains(msg, `error: shell "qa" not configured`) {
		t.Fatalf("missing-shell error does not include heading: %q", msg)
	}
	if !strings.Contains(msg, "toolbox shell qa --create") {
		t.Fatalf("missing-shell error does not include --create hint: %q", msg)
	}
}

func TestResolveShellWorkspaceMissingPathShowsHint(t *testing.T) {
	cfg = &config.Config{
		Shells: map[string]config.NamedShell{
			"infra": {Path: filepath.Join(t.TempDir(), "missing")},
		},
	}
	t.Cleanup(func() { cfg = nil })

	_, _, err := resolveShellWorkspace([]string{"infra"}, false, "")
	if err == nil {
		t.Fatal("expected missing-path error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "mkdir -p") || !strings.Contains(msg, "toolbox shell infra --create") {
		t.Fatalf("missing-path hint mismatch: %q", msg)
	}
}

func TestResolveShellWorkspaceCreateWritesConfigAndCreatesDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	target := filepath.Join(t.TempDir(), "qa")

	cfg = &config.Config{}
	t.Cleanup(func() { cfg = nil })

	ws, name, err := resolveShellWorkspace([]string{"qa"}, true, target)
	if err != nil {
		t.Fatalf("resolveShellWorkspace: %v", err)
	}
	if ws != target || name != "qa" {
		t.Fatalf("unexpected resolve output: ws=%q name=%q", ws, name)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("expected created directory %q: %v", target, err)
	}

	raw, err := os.ReadFile(filepath.Join(home, ".toolbox.yaml"))
	if err != nil {
		t.Fatalf("read ~/.toolbox.yaml: %v", err)
	}
	var parsed map[string]any
	if err := yaml.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse written yaml: %v", err)
	}
	shells, ok := parsed["shells"].(map[string]any)
	if !ok {
		t.Fatalf("shells block missing in written config: %s", string(raw))
	}
	qa, ok := shells["qa"].(map[string]any)
	if !ok || qa["path"] != target {
		t.Fatalf("shells.qa.path mismatch in written config: %s", string(raw))
	}
}

func TestUpsertShellInUserConfigPreservesExistingFields(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".toolbox.yaml")
	orig := "# user comment\ntools:\n  gh: false\n"
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	if err := upsertShellInUserConfig("infra", "/tmp/infra"); err != nil {
		t.Fatalf("upsertShellInUserConfig: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, "tools:") || !strings.Contains(text, "gh: false") {
		t.Fatalf("existing config keys were not preserved: %s", text)
	}
	if !strings.Contains(text, "shells:") || !strings.Contains(text, "infra:") {
		t.Fatalf("shells entry missing after upsert: %s", text)
	}
}
