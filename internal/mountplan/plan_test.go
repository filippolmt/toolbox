package mountplan

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/fsx"
	"github.com/filippolmt/toolbox/internal/proximo"
)

// TestPlanEndToEnd exercises the full pipeline: defaults → applyMountsRoot
// → user merge → resolve (with create + symlink + missing) → workspace
// bind → mirror. This is the deepening payoff — a single test covering
// what previously required four call sites across two packages.
func TestPlanEndToEnd(t *testing.T) {
	// Sandbox HOME so the resolver's create/symlink steps land in tmp,
	// not the real ~/.toolbox/.
	tmpHome := t.TempDir()

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

	result, err := Plan(PlanInput{Host: fsx.Host{Home: tmpHome}, Cfg: &cfg, Workspace: workspace})
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
	if _, err := Plan(PlanInput{Host: testHost(t), Cfg: &cfg, Workspace: "/workspace"}); err == nil {
		t.Fatal("Plan should reject bare ~ as mounts_root")
	}
}

// TestPlanRejectsUnknownPatchName exercises the merge-error path: a typo
// in mounts: must surface as a Plan error, not silently bind defaults.
func TestPlanRejectsUnknownPatchName(t *testing.T) {
	cfg := config.Config{
		Mounts: []config.Mount{{Name: "nonexistent", Source: "/x"}},
	}
	_, err := Plan(PlanInput{Host: testHost(t), Cfg: &cfg, Workspace: "/workspace"})
	if err == nil {
		t.Fatal("Plan should fail when mounts: patches an unknown name")
	}
}

// TestPlanIncludesWorkspaceBindEvenWithReservedPath: reserved paths skip
// the mirror but the canonical workspace bind always survives.
func TestPlanIncludesWorkspaceBindEvenWithReservedPath(t *testing.T) {
	tmpHome := t.TempDir()

	// /home/toolbox/* is reserved → no mirror.
	workspace := "/home/toolbox/project"

	result, err := Plan(PlanInput{Host: fsx.Host{Home: tmpHome}, Cfg: &config.Config{}, Workspace: workspace})
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
	got, err := Merge(testHost(t), &config.Config{Bridge: &off}, nil, proximo.Gate{})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	for _, m := range got {
		if m.Name == "bridge" || m.Name == "bridge-legacy" || m.Name == "bridge-run" {
			t.Errorf("%s mount must be dropped when Bridge=false", m.Name)
		}
	}
}

// TestPlanWithProfile drives the full pipeline (not just Merge) with a profile:
// the claude default binds under ~/.toolbox/profiles/work, while ssh still
// symlinks to the host ~/.ssh — isolated auth, shared identity, end to end.
func TestPlanWithProfile(t *testing.T) {
	tmpHome := t.TempDir()

	hostSSH := filepath.Join(tmpHome, ".ssh")
	if err := os.Mkdir(hostSSH, 0o700); err != nil {
		t.Fatalf("setup ssh: %v", err)
	}
	workspace := filepath.Join(tmpHome, "proj")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("setup workspace: %v", err)
	}

	result, err := Plan(PlanInput{Host: fsx.Host{Home: tmpHome}, Cfg: &config.Config{}, Workspace: workspace, Profile: &Profile{Name: "work"}})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	profileClaude := filepath.Join(tmpHome, ".toolbox", "profiles", "work", ".claude")
	hostSSHResolved, _ := filepath.EvalSymlinks(hostSSH)

	var gotClaude, gotSSH string
	for _, b := range result.Binds {
		switch b.Target {
		case "/home/toolbox/.claude":
			gotClaude = b.Source
		case "/home/toolbox/.ssh":
			gotSSH = b.Source
		}
	}
	if gotClaude != profileClaude {
		t.Errorf("claude bind Source = %q, want %q (isolated in profile)", gotClaude, profileClaude)
	}
	if gotSSH != hostSSHResolved && gotSSH != hostSSH {
		t.Errorf("ssh bind Source = %q, want host %q (shared identity)", gotSSH, hostSSHResolved)
	}
}

func TestMerge_BridgeTrueKeepsMounts(t *testing.T) {
	on := true
	got, err := Merge(testHost(t), &config.Config{Bridge: &on}, nil, proximo.Gate{})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	found := map[string]bool{}
	for _, m := range got {
		found[m.Name] = true
	}
	for _, name := range []string{"bridge", "bridge-legacy", "bridge-run"} {
		if !found[name] {
			t.Errorf("%s mount missing when Bridge=true", name)
		}
	}
}

// TestStateDirPath resolves the host side of the directory the container sees
// as ~/.toolbox-state. It is the seam the host-side update prefetch writes
// through and the in-container prompt hook reads back, so it has to follow
// the same retargeting the bind itself follows — deriving it from the merged
// mount set rather than re-deriving a path is what makes that hold.
func TestStateDirPath(t *testing.T) {
	tmpHome := t.TempDir()

	profile, err := NewProfile("work", nil)
	if err != nil {
		t.Fatalf("NewProfile: %v", err)
	}

	for _, tc := range []struct {
		name    string
		cfg     *config.Config
		profile *Profile
		want    string
	}{
		{"default", &config.Config{}, nil, filepath.Join(tmpHome, ".toolbox", "toolbox", "state")},
		{"mounts_root", &config.Config{MountsRoot: "/custom/root"}, nil, "/custom/root/toolbox/state"},
		{"profile wins over mounts_root", &config.Config{MountsRoot: "/custom/root"}, profile,
			filepath.Join(tmpHome, ".toolbox", "profiles", "work", "toolbox", "state")},
		{"mount disabled", &config.Config{Mounts: []config.Mount{{Name: "state", Disabled: true}}}, nil, ""},
		// The lookup keys on the container target, not the name: a rename is
		// the user's business, but a mount that no longer lands on
		// ~/.toolbox-state is one the container cannot read the cache from.
		{"renamed but same target", &config.Config{Mounts: []config.Mount{
			{Name: "state", Source: "~/elsewhere"},
		}}, nil, filepath.Join(tmpHome, "elsewhere")},
		{"retargeted elsewhere", &config.Config{Mounts: []config.Mount{
			{Name: "state", Source: "~/.toolbox/toolbox/state", Target: "/home/toolbox/somewhere-else"},
		}}, nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := StateDirPath(fsx.Host{Home: tmpHome}, tc.cfg, tc.profile, proximo.Gate{})
			if err != nil {
				t.Fatalf("StateDirPath: %v", err)
			}
			if got != tc.want {
				t.Errorf("StateDirPath = %q, want %q", got, tc.want)
			}
		})
	}
}
