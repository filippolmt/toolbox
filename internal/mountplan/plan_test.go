package mountplan

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/filippolmt/toolbox/internal/config"
)

// TestPlanEndToEnd exercises the full pipeline: defaults → applyMountsRoot
// → user merge → resolve (with create + symlink + missing) → workspace
// bind → mirror. This is the deepening payoff — a single test covering
// what previously required four call sites across two packages.
func TestPlanEndToEnd(t *testing.T) {
	// Sandbox HOME so the resolver's create/symlink steps land in tmp,
	// not the real ~/.toolbox/.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	// Pre-create a host symlink target so the ssh default actually links
	// (otherwise it gets skipped with a warning).
	hostSSH := filepath.Join(tmpHome, ".ssh")
	if err := os.Mkdir(hostSSH, 0o700); err != nil {
		t.Fatalf("setup ssh: %v", err)
	}
	hostGitconfig := filepath.Join(tmpHome, ".gitconfig")
	if err := os.WriteFile(hostGitconfig, []byte("[user]\n"), 0o600); err != nil {
		t.Fatalf("setup gitconfig: %v", err)
	}

	workspace := filepath.Join(tmpHome, "projects", "demo")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("setup workspace: %v", err)
	}

	cfg := config.Config{
		// User patches: retarget gws and disable docker-sock — the latter is
		// the most useful real-world disable so the test exercises it.
		Mounts: []config.Mount{
			{Name: "gws", Source: filepath.Join(tmpHome, "custom-gws")},
			{Name: "docker-sock", Disabled: true},
			// Anonymous append.
			{Name: "project-data", Source: workspace, Target: "/data"},
		},
	}

	result, err := Plan(&cfg, workspace)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	// docker-sock disabled by user.
	for _, b := range result.Binds {
		if b.Target == "/var/run/docker.sock" {
			t.Errorf("docker-sock should be disabled, got bind %+v", b)
		}
	}

	// gws retargeted to /tmp/.../custom-gws.
	wantGwsSource := filepath.Join(tmpHome, "custom-gws")
	wantGwsResolved, _ := filepath.EvalSymlinks(wantGwsSource)
	foundGws := false
	for _, b := range result.Binds {
		if b.Target == "/home/toolbox/.config/gws" {
			foundGws = true
			if b.Source != wantGwsResolved && b.Source != wantGwsSource {
				t.Errorf("gws bind Source = %q, want %q", b.Source, wantGwsResolved)
			}
		}
	}
	if !foundGws {
		t.Error("gws bind missing from result")
	}

	// Anonymous append present.
	foundData := false
	for _, b := range result.Binds {
		if b.Target == "/data" {
			foundData = true
		}
	}
	if !foundData {
		t.Error("project-data anonymous append missing from result")
	}

	// Workspace bind always present at WorkspaceTarget.
	wsResolved, _ := filepath.EvalSymlinks(workspace)
	foundWS := false
	for _, b := range result.Binds {
		if b.Target == WorkspaceTarget && (b.Source == workspace || b.Source == wsResolved) {
			foundWS = true
		}
	}
	if !foundWS {
		t.Errorf("workspace bind missing for target %s in %v", WorkspaceTarget, result.Binds)
	}

	// Mirror bind present when workspace is non-reserved (tmpdir under
	// /private/var on macOS is reserved → no mirror; on Linux tmpdir is
	// /tmp which is non-reserved → mirror should appear). Either way,
	// WorkingDir matches the same predicate.
	mirrorPath, mirrorOK := WorkspaceMirrorPath(workspace)
	if mirrorOK {
		foundMirror := false
		for _, b := range result.Binds {
			if b.Target == mirrorPath {
				foundMirror = true
			}
		}
		if !foundMirror {
			t.Errorf("mirror bind missing for target %s in %v", mirrorPath, result.Binds)
		}
		if result.WorkingDir != mirrorPath {
			t.Errorf("WorkingDir = %q, want %q", result.WorkingDir, mirrorPath)
		}
	} else if result.WorkingDir != WorkspaceTarget {
		t.Errorf("WorkingDir = %q, want WorkspaceTarget %q (mirror not safe)",
			result.WorkingDir, WorkspaceTarget)
	}

	// Source dirs created on disk for CreateIfMissing defaults.
	expectCreated := []string{
		filepath.Join(tmpHome, ".toolbox", ".claude"),
		filepath.Join(tmpHome, ".toolbox", "toolbox", "state"),
		filepath.Join(tmpHome, ".toolbox", "go"),
	}
	for _, p := range expectCreated {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected %s to be created, got %v", p, err)
		}
	}

	// SSH symlink created.
	sshSrc := filepath.Join(tmpHome, ".toolbox", "ssh")
	info, err := os.Lstat(sshSrc)
	if err != nil {
		t.Errorf("expected symlink at %s: %v", sshSrc, err)
	} else if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("expected symlink at %s, got mode %v", sshSrc, info.Mode())
	}
}

// TestPlanRejectsBadMountsRoot exercises validation at the seam.
func TestPlanRejectsBadMountsRoot(t *testing.T) {
	cfg := config.Config{MountsRoot: "~"}
	if _, err := Plan(&cfg, "/workspace"); err == nil {
		t.Fatal("Plan should reject bare ~ as mounts_root")
	}
}

// TestPlanRejectsUnknownPatchName exercises the merge-error path: a typo
// in mounts: must surface as a Plan error, not silently bind defaults.
func TestPlanRejectsUnknownPatchName(t *testing.T) {
	cfg := config.Config{
		Mounts: []config.Mount{{Name: "nonexistent", Source: "/x"}},
	}
	_, err := Plan(&cfg, "/workspace")
	if err == nil {
		t.Fatal("Plan should fail when mounts: patches an unknown name")
	}
}

// TestPlanIncludesWorkspaceBindEvenWithReservedPath: reserved paths skip
// the mirror but the canonical workspace bind always survives.
func TestPlanIncludesWorkspaceBindEvenWithReservedPath(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	// /home/toolbox/* is reserved → no mirror.
	workspace := "/home/toolbox/project"

	result, err := Plan(&config.Config{}, workspace)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	wantBind := Bind{Source: workspace, Target: WorkspaceTarget, Mode: "rw"}
	if !slices.Contains(result.Binds, wantBind) {
		t.Errorf("expected canonical workspace bind %+v in %v", wantBind, result.Binds)
	}
	if result.WorkingDir != WorkspaceTarget {
		t.Errorf("WorkingDir = %q, want %q (mirror not safe for reserved path)",
			result.WorkingDir, WorkspaceTarget)
	}
}

func TestMerge_BridgeFalseDropsMounts(t *testing.T) {
	off := false
	got, err := Merge(&config.Config{Bridge: &off})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	for _, m := range got {
		if m.Name == "bridge" || m.Name == "bridge-legacy" {
			t.Errorf("%s mount must be dropped when Bridge=false", m.Name)
		}
	}
}

func TestMerge_BridgeTrueKeepsMounts(t *testing.T) {
	on := true
	got, err := Merge(&config.Config{Bridge: &on})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	found := map[string]bool{}
	for _, m := range got {
		found[m.Name] = true
	}
	for _, name := range []string{"bridge", "bridge-legacy"} {
		if !found[name] {
			t.Errorf("%s mount missing when Bridge=true", name)
		}
	}
}
