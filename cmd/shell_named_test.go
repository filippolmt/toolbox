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
	if !strings.Contains(msg, `shell "qa" not configured`) {
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

// TestResolveShellWorkspaceRejectsHashShapedName guards against named
// shells whose sanitized form (`[a-f0-9]{8}`) would be indistinguishable
// from the trailing hash component of a workspace container name
// (`toolbox-<base>-<8hex>`). Accepting one would let a named container
// silently shadow a workspace-derived container on `toolbox stop`.
func TestResolveShellWorkspaceRejectsHashShapedName(t *testing.T) {
	cfg = &config.Config{}
	t.Cleanup(func() { cfg = nil })

	_, _, err := resolveShellWorkspace([]string{"1a2b3c4d"}, false, "")
	if err == nil {
		t.Fatal("expected error for hash-shaped shell name")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("error %q should mention the ambiguity", err)
	}
}

// TestResolveShellWorkspaceRejectsEmptyName rejects the degenerate
// `toolbox shell ""` invocation that would otherwise sanitize to the empty
// string and force NamedContainerName into its "shell" fallback.
func TestResolveShellWorkspaceRejectsEmptyName(t *testing.T) {
	cfg = &config.Config{}
	t.Cleanup(func() { cfg = nil })

	_, _, err := resolveShellWorkspace([]string{"   "}, false, "")
	if err == nil {
		t.Fatal("expected error for blank shell name")
	}
}

// TestShellPathForErrorsOnNilCfg surfaces a programming error (cobra's
// OnInitialize did not run before the command body executed) instead of
// silently treating every name as "not configured" and triggering the
// bootstrap flow — that mask was the historical failure mode.
func TestShellPathForErrorsOnNilCfg(t *testing.T) {
	cfg = nil
	_, _, err := shellPathFor("infra")
	if err == nil {
		t.Fatal("expected error when cfg is nil")
	}
}

// TestEnsureNamedShellPathRejectsSymlink: a symlink at the final path
// element is refused because a TOCTOU swap between this check and the
// Docker bind-mount stage would redirect the container mount source.
func TestEnsureNamedShellPathRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("setup target: %v", err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported on this platform: %v", err)
	}

	_, _, err := ensureNamedShellPath("infra", link, false)
	if err == nil {
		t.Fatal("expected symlink rejection")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("error %q should mention symlink", err)
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

	if err := upsertShellInUserConfig(home, "infra", "/tmp/infra"); err != nil {
		t.Fatalf("upsertShellInUserConfig: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, "# user comment") {
		t.Fatalf("existing comment was not preserved: %s", text)
	}
	if !strings.Contains(text, "tools:") || !strings.Contains(text, "gh: false") {
		t.Fatalf("existing config keys were not preserved: %s", text)
	}
	if !strings.Contains(text, "shells:") || !strings.Contains(text, "infra:") {
		t.Fatalf("shells entry missing after upsert: %s", text)
	}
}
