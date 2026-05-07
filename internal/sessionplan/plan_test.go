package sessionplan_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/go-connections/nat"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/mountplan"
	"github.com/filippolmt/toolbox/internal/sessionplan"
	"github.com/filippolmt/toolbox/internal/shellcmd"
)

// testConfig returns a *config.Config whose Tools map matches the catalog
// defaults. Pairs with build.ResolveImage's IsDefault short-circuit so the
// happy-path tests resolve to the canonical GHCR registry tag — mirrors
// internal/container/lifecycle_test.go::testConfig.
func testConfig() *config.Config {
	return &config.Config{
		Shell: "zsh",
		Tools: config.DefaultTools(),
	}
}

// --- Plan tier (fs side effects) ---

// TestPlanComposesImage asserts that Plan delegates image resolution to
// build.ResolveImage and lands the result on plan.Image.
func TestPlanComposesImage(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	workspace := filepath.Join(tmpHome, "ws")
	if err := mkdirAll(t, workspace); err != nil {
		t.Fatalf("setup: %v", err)
	}

	t.Run("default tools resolve to registry tag", func(t *testing.T) {
		plan, err := sessionplan.Plan(testConfig(), workspace, nil, "dev")
		if err != nil {
			t.Fatalf("Plan: %v", err)
		}
		if plan.Image.Ref != "ghcr.io/filippolmt/toolbox:latest" {
			t.Errorf("Image.Ref = %q, want canonical GHCR tag", plan.Image.Ref)
		}
		if plan.Image.IsLocal {
			t.Error("Image.IsLocal = true, want false for default tools")
		}
	})

	t.Run("non-default tools resolve to local hash", func(t *testing.T) {
		cfg := testConfig()
		// Flip one tool off to force the local-build path.
		cfg.Tools = config.DefaultTools()
		for k := range cfg.Tools {
			cfg.Tools[k] = false
			break
		}
		plan, err := sessionplan.Plan(cfg, workspace, nil, "dev")
		if err != nil {
			t.Fatalf("Plan: %v", err)
		}
		if !strings.HasPrefix(plan.Image.Ref, "toolbox:local-") {
			t.Errorf("Image.Ref = %q, want toolbox:local- prefix", plan.Image.Ref)
		}
		if !plan.Image.IsLocal {
			t.Error("Image.IsLocal = false, want true for non-default tools")
		}
	})
}

// TestPlanComposesMounts asserts Plan delegates the mount stage to
// mountplan.Plan and propagates Binds + WorkingDir.
func TestPlanComposesMounts(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	workspace := filepath.Join(tmpHome, "projects", "demo")
	if err := mkdirAll(t, workspace); err != nil {
		t.Fatalf("setup: %v", err)
	}

	plan, err := sessionplan.Plan(testConfig(), workspace, nil, "dev")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	if len(plan.Binds) == 0 {
		t.Fatal("Binds is empty; expected mountplan.Plan to populate")
	}

	// Workspace bind always present at WorkspaceTarget.
	wsResolved, _ := filepath.EvalSymlinks(workspace)
	foundWS := false
	for _, b := range plan.Binds {
		if b.Target == mountplan.WorkspaceTarget && (b.Source == workspace || b.Source == wsResolved) {
			foundWS = true
		}
	}
	if !foundWS {
		t.Errorf("workspace bind missing for target %s in %v", mountplan.WorkspaceTarget, plan.Binds)
	}

	// WorkingDir matches mountplan's mirror predicate.
	if mirror, ok := mountplan.WorkspaceMirrorPath(workspace); ok {
		if plan.WorkingDir != mirror {
			t.Errorf("WorkingDir = %q, want mirror %q", plan.WorkingDir, mirror)
		}
	} else if plan.WorkingDir != mountplan.WorkspaceTarget {
		t.Errorf("WorkingDir = %q, want WorkspaceTarget %q", plan.WorkingDir, mountplan.WorkspaceTarget)
	}
}

// TestPlanComposesPorts asserts the port stage parses --publish specs into
// typed ExposedPorts + PortBindings with 127.0.0.1 default HostIP.
func TestPlanComposesPorts(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	workspace := filepath.Join(tmpHome, "ws")
	if err := mkdirAll(t, workspace); err != nil {
		t.Fatalf("setup: %v", err)
	}

	plan, err := sessionplan.Plan(testConfig(), workspace, []string{"7171:7171", "8080:8080"}, "dev")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	if len(plan.ExposedPorts) != 2 {
		t.Errorf("ExposedPorts size = %d, want 2", len(plan.ExposedPorts))
	}
	for _, want := range []nat.Port{"7171/tcp", "8080/tcp"} {
		if _, ok := plan.ExposedPorts[want]; !ok {
			t.Errorf("ExposedPorts missing %s; got %v", want, plan.ExposedPorts)
		}
		bindings := plan.PortBindings[want]
		if len(bindings) == 0 {
			t.Errorf("PortBindings[%s] empty", want)
			continue
		}
		if bindings[0].HostIP != "127.0.0.1" {
			t.Errorf("PortBindings[%s][0].HostIP = %q, want 127.0.0.1", want, bindings[0].HostIP)
		}
	}
}

// TestPlanComputesContainerName asserts plan.ContainerName equals the
// standalone ContainerNameFor helper byte-for-byte (Stop/StopAll
// observability invariant).
func TestPlanComputesContainerName(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	workspace := filepath.Join(tmpHome, "ws")
	if err := mkdirAll(t, workspace); err != nil {
		t.Fatalf("setup: %v", err)
	}

	plan, err := sessionplan.Plan(testConfig(), workspace, nil, "dev")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	want := sessionplan.ContainerNameFor(workspace)
	if plan.ContainerName != want {
		t.Errorf("ContainerName = %q, want %q", plan.ContainerName, want)
	}
	if !strings.HasPrefix(plan.ContainerName, sessionplan.ContainerNamePrefix) {
		t.Errorf("ContainerName missing prefix %q: %q", sessionplan.ContainerNamePrefix, plan.ContainerName)
	}
}

// TestPlanContainerNameDeterministic asserts two calls with the same
// workspace produce identical container names; the format is the documented
// toolbox-<basename>-<hash8>.
func TestPlanContainerNameDeterministic(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	workspace := filepath.Join(tmpHome, "ws")
	if err := mkdirAll(t, workspace); err != nil {
		t.Fatalf("setup: %v", err)
	}

	a, err := sessionplan.Plan(testConfig(), workspace, nil, "dev")
	if err != nil {
		t.Fatalf("Plan a: %v", err)
	}
	b, err := sessionplan.Plan(testConfig(), workspace, nil, "dev")
	if err != nil {
		t.Fatalf("Plan b: %v", err)
	}
	if a.ContainerName != b.ContainerName {
		t.Errorf("ContainerName not deterministic: %q vs %q", a.ContainerName, b.ContainerName)
	}

	// Format: toolbox-<basename>-<hash8>. Hash is exactly 8 hex chars.
	parts := strings.Split(a.ContainerName, "-")
	if len(parts) < 3 {
		t.Fatalf("ContainerName format unexpected: %q", a.ContainerName)
	}
	hash := parts[len(parts)-1]
	if len(hash) != 8 {
		t.Errorf("hash suffix length = %d, want 8 (name=%q)", len(hash), a.ContainerName)
	}
}

// TestPlanComputesEnv asserts the exact two-element env slice is populated.
func TestPlanComputesEnv(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	workspace := filepath.Join(tmpHome, "ws")
	if err := mkdirAll(t, workspace); err != nil {
		t.Fatalf("setup: %v", err)
	}

	plan, err := sessionplan.Plan(testConfig(), workspace, nil, "dev")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	want := []string{
		"TOOLBOX_HOST_WORKSPACE=" + workspace,
		"PWD=" + plan.WorkingDir,
	}
	if !slices.Equal(plan.Env, want) {
		t.Errorf("Env = %v, want %v", plan.Env, want)
	}
}

// TestPlanRejectsBadPort asserts port-parse errors propagate.
func TestPlanRejectsBadPort(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	workspace := filepath.Join(tmpHome, "ws")
	if err := mkdirAll(t, workspace); err != nil {
		t.Fatalf("setup: %v", err)
	}

	_, err := sessionplan.Plan(testConfig(), workspace, []string{"not-a-port"}, "dev")
	if err == nil {
		t.Fatal("Plan should reject malformed --publish spec")
	}
	if !strings.Contains(err.Error(), "not-a-port") {
		t.Errorf("error %q should mention the bad spec", err.Error())
	}
}

// TestPlanRejectsBadMountsRoot proves mount-stage validation errors
// propagate through sessionplan.Plan.
func TestPlanRejectsBadMountsRoot(t *testing.T) {
	cfg := testConfig()
	cfg.MountsRoot = "~" // bare ~ is rejected by config.ValidateMountsRoot
	_, err := sessionplan.Plan(cfg, "/workspace", nil, "dev")
	if err == nil {
		t.Fatal("Plan should reject bare ~ as mounts_root")
	}
}

// TestPlanWorkspaceNormalizationOnce asserts the workspace is filepath.Abs
// + filepath.Clean'd once at the top of Plan, so the container name and any
// downstream consumer see the canonical absolute path.
func TestPlanWorkspaceNormalizationOnce(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	canonical := filepath.Join(tmpHome, "bar")
	if err := mkdirAll(t, canonical); err != nil {
		t.Fatalf("setup: %v", err)
	}
	// A path with a redundant ".." that filepath.Clean collapses to canonical.
	dirty := filepath.Join(tmpHome, "foo", "..", "bar")

	plan, err := sessionplan.Plan(testConfig(), dirty, nil, "dev")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	want := sessionplan.ContainerNameFor(canonical)
	if plan.ContainerName != want {
		t.Errorf("ContainerName for dirty path = %q, want canonical %q", plan.ContainerName, want)
	}
}

// --- Merge tier (pure data, NO fs side effects) ---

// TestMergeIsPure asserts Merge does not require HOME setup or t.TempDir
// — composing mountplan.Merge instead of mountplan.Plan keeps the call
// purely data. NO t.Setenv("HOME", ...), NO t.TempDir() in this test.
func TestMergeIsPure(t *testing.T) {
	merged, err := sessionplan.Merge(testConfig(), "/workspace", nil, "dev")
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if merged.Image.Ref == "" {
		t.Error("Merge.Image.Ref empty; expected ResolveImage output")
	}
	if merged.ContainerName == "" {
		t.Error("Merge.ContainerName empty; expected ContainerNameFor output")
	}
	if len(merged.Binds) == 0 {
		t.Error("Merge.Binds empty; expected post-merge defaults")
	}
	// Binds is the post-merge config.Mount slice (asymmetric vs Plan's
	// []mountplan.Bind). Type-check via assignment to a typed var; the
	// staticcheck-friendly form uses an explicit blank identifier
	// rather than a redundant type declaration.
	_ = []config.Mount(merged.Binds)

	// WorkingDir defaults to mountplan.WorkspaceTarget at the pure-merge
	// tier (mirror predicate is fs-aware, only available in Plan).
	if merged.WorkingDir != mountplan.WorkspaceTarget {
		t.Errorf("Merge.WorkingDir = %q, want %q", merged.WorkingDir, mountplan.WorkspaceTarget)
	}

	// Env reflects the same workspace + workingDir contract.
	wantEnv := []string{
		"TOOLBOX_HOST_WORKSPACE=/workspace",
		"PWD=" + mountplan.WorkspaceTarget,
	}
	if !slices.Equal(merged.Env, wantEnv) {
		t.Errorf("Merge.Env = %v, want %v", merged.Env, wantEnv)
	}
}

// TestMergeRejectsBadMountsRoot exercises mountplan.Merge validation
// surfacing through sessionplan.Merge — pure-data path, no fs.
func TestMergeRejectsBadMountsRoot(t *testing.T) {
	cfg := testConfig()
	cfg.MountsRoot = "~"
	_, err := sessionplan.Merge(cfg, "/workspace", nil, "dev")
	if err == nil {
		t.Fatal("Merge should reject bare ~ as mounts_root")
	}
}

// TestMergeRejectsBadPort asserts port-parse errors propagate at the
// pure-data tier as well.
func TestMergeRejectsBadPort(t *testing.T) {
	_, err := sessionplan.Merge(testConfig(), "/workspace", []string{"not-a-port"}, "dev")
	if err == nil {
		t.Fatal("Merge should reject malformed --publish spec")
	}
}

// --- MissingPublishPorts ---

// TestMissingPublishPortsTable absorbs the publish-mismatch table
// previously living in internal/container/lifecycle_test.go::TestShellPublishMismatchWarning.
// Pure-data assertions on the missing-ports list, no Docker SDK invoked.
func TestMissingPublishPortsTable(t *testing.T) {
	cases := []struct {
		name        string
		wanted      nat.PortMap
		existing    nat.PortMap
		nilBase     bool
		nilHostCfg  bool
		wantMissing []string
	}{
		{
			name:        "no_mismatch_when_all_bound",
			wanted:      nat.PortMap{"7171/tcp": nil, "8080/tcp": nil},
			existing:    nat.PortMap{"7171/tcp": nil, "8080/tcp": nil},
			wantMissing: nil,
		},
		{
			name:        "missing_port_reported",
			wanted:      nat.PortMap{"7171/tcp": nil, "8080/tcp": nil},
			existing:    nat.PortMap{"7171/tcp": nil},
			wantMissing: []string{"8080/tcp"},
		},
		{
			name:        "empty_existing_reports_all",
			wanted:      nat.PortMap{"7171/tcp": nil},
			existing:    nat.PortMap{},
			wantMissing: []string{"7171/tcp"},
		},
		{
			name:        "nil_base_returns_nil",
			wanted:      nat.PortMap{"7171/tcp": nil},
			nilBase:     true,
			wantMissing: nil,
		},
		{
			name:        "nil_hostconfig_returns_nil",
			wanted:      nat.PortMap{"7171/tcp": nil},
			nilHostCfg:  true,
			wantMissing: nil,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var inspect container.InspectResponse
			switch {
			case tc.nilBase:
				inspect = container.InspectResponse{} // ContainerJSONBase=nil
			case tc.nilHostCfg:
				inspect = container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{}, // HostConfig=nil
				}
			default:
				inspect = container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						HostConfig: &container.HostConfig{PortBindings: tc.existing},
					},
				}
			}

			got := sessionplan.MissingPublishPorts(tc.wanted, inspect)
			sort.Strings(got)
			want := append([]string(nil), tc.wantMissing...)
			sort.Strings(want)
			if !slices.Equal(got, want) {
				t.Errorf("MissingPublishPorts = %v, want %v", got, want)
			}
		})
	}
}

// --- Cmd / SecurityOpt / Cfg ---

// TestPlanComputesCmd asserts the shell command resolution rides through
// Plan: bash returns /bin/bash, and shell:zsh + tools.zsh:false fails
// with a wrapped *shellcmd.ShellMismatchError before any Docker work
// could happen.
func TestPlanComputesCmd(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	workspace := filepath.Join(tmpHome, "ws")
	if err := mkdirAll(t, workspace); err != nil {
		t.Fatalf("setup: %v", err)
	}

	t.Run("bash with bash enabled returns /bin/bash", func(t *testing.T) {
		cfg := testConfig()
		cfg.Shell = "bash"
		cfg.Tools["bash"] = true
		plan, err := sessionplan.Plan(cfg, workspace, nil, "dev")
		if err != nil {
			t.Fatalf("Plan: %v", err)
		}
		if !slices.Equal(plan.Cmd, []string{"/bin/bash"}) {
			t.Errorf("Cmd = %v, want [/bin/bash]", plan.Cmd)
		}
	})

	t.Run("zsh disabled returns ShellMismatchError", func(t *testing.T) {
		cfg := &config.Config{
			Shell: "zsh",
			Tools: map[string]bool{"zsh": false},
		}
		_, err := sessionplan.Plan(cfg, workspace, nil, "dev")
		if err == nil {
			t.Fatal("Plan should reject shell:zsh + tools.zsh:false")
		}
		var mismatch *shellcmd.ShellMismatchError
		if !errors.As(err, &mismatch) {
			t.Fatalf("expected *shellcmd.ShellMismatchError, got %T: %v", err, err)
		}
	})
}

// TestPlanComputesSecurityOpt: codex enabled (or absent) → seccomp opts;
// codex explicitly disabled → nil.
func TestPlanComputesSecurityOpt(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	workspace := filepath.Join(tmpHome, "ws")
	if err := mkdirAll(t, workspace); err != nil {
		t.Fatalf("setup: %v", err)
	}

	t.Run("codex enabled emits seccomp=unconfined", func(t *testing.T) {
		plan, err := sessionplan.Plan(testConfig(), workspace, nil, "dev")
		if err != nil {
			t.Fatalf("Plan: %v", err)
		}
		if !slices.Equal(plan.SecurityOpt, []string{"seccomp=unconfined"}) {
			t.Errorf("SecurityOpt = %v, want [seccomp=unconfined]", plan.SecurityOpt)
		}
	})

	t.Run("codex disabled emits nil SecurityOpt", func(t *testing.T) {
		cfg := testConfig()
		cfg.Tools["codex"] = false
		plan, err := sessionplan.Plan(cfg, workspace, nil, "dev")
		if err != nil {
			t.Fatalf("Plan: %v", err)
		}
		if plan.SecurityOpt != nil {
			t.Errorf("SecurityOpt = %v, want nil", plan.SecurityOpt)
		}
	})
}

// TestPlanComputesBuildArgs asserts cfg.Tools is materialised into
// plan.BuildArgs once at Plan time so lifecycle.ensureImage stays a pure
// consumer of the typed plan.
func TestPlanComputesBuildArgs(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	workspace := filepath.Join(tmpHome, "ws")
	if err := mkdirAll(t, workspace); err != nil {
		t.Fatalf("setup: %v", err)
	}

	cfg := testConfig()
	cfg.Tools["gcloud"] = false
	plan, err := sessionplan.Plan(cfg, workspace, nil, "dev")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	v, ok := plan.BuildArgs["INSTALL_GCLOUD"]
	if !ok || v == nil || *v != "false" {
		t.Errorf("BuildArgs[INSTALL_GCLOUD] = %v, want pointer to \"false\"", v)
	}
}

// TestMergeAlsoComputesCmd: same shell-resolution semantics surface at
// the pure-data Merge tier (no t.TempDir / HOME setup needed).
func TestMergeAlsoComputesCmd(t *testing.T) {
	t.Run("bash returns /bin/bash", func(t *testing.T) {
		cfg := testConfig()
		cfg.Shell = "bash"
		cfg.Tools["bash"] = true
		merged, err := sessionplan.Merge(cfg, "/workspace", nil, "dev")
		if err != nil {
			t.Fatalf("Merge: %v", err)
		}
		if !slices.Equal(merged.Cmd, []string{"/bin/bash"}) {
			t.Errorf("Merge.Cmd = %v, want [/bin/bash]", merged.Cmd)
		}
	})

	t.Run("zsh disabled errors", func(t *testing.T) {
		cfg := &config.Config{
			Shell: "zsh",
			Tools: map[string]bool{"zsh": false},
		}
		_, err := sessionplan.Merge(cfg, "/workspace", nil, "dev")
		if err == nil {
			t.Fatal("Merge should reject shell:zsh + tools.zsh:false")
		}
		var mismatch *shellcmd.ShellMismatchError
		if !errors.As(err, &mismatch) {
			t.Fatalf("expected *shellcmd.ShellMismatchError, got %T: %v", err, err)
		}
	})
}

// --- Helpers ---

// mkdirAll is a tiny wrapper over os.MkdirAll so test bodies stay terse.
func mkdirAll(t *testing.T, path string) error {
	t.Helper()
	return os.MkdirAll(path, 0o755)
}
