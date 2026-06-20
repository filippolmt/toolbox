package sessionplan_test

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/mountplan"
	"github.com/filippolmt/toolbox/internal/sessionplan"
	"github.com/filippolmt/toolbox/internal/version"
)

// testConfig returns a baseline *config.Config that resolves to the canonical
// GHCR registry tag — mirrors internal/container/lifecycle_test.go::testConfig.
func testConfig() *config.Config {
	return &config.Config{Shell: "zsh"}
}

// --- Plan tier (fs side effects) ---

// TestPlanComposesImage asserts every config resolves to the canonical
// registry tag (single canonical image — no per-tool opt-out, no local
// hash fallback).
func TestPlanComposesImage(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	workspace := filepath.Join(tmpHome, "ws")
	if err := mkdirAll(t, workspace); err != nil {
		t.Fatalf("setup: %v", err)
	}

	plan, err := sessionplan.Plan(testConfig(), workspace, nil, false, "")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.Image.Ref != "ghcr.io/filippolmt/toolbox:latest" {
		t.Errorf("Image.Ref = %q, want canonical GHCR tag", plan.Image.Ref)
	}
}

// TestMergeImageSelectionAndPullPolicy asserts registry_mirror relocates the
// host (path+tag preserved) and the pull policy propagates onto the Image.
func TestMergeImageSelectionAndPullPolicy(t *testing.T) {
	cfg := &config.Config{Shell: "zsh", RegistryMirror: "harbor.corp.io/ghcr-proxy", Pull: "never"}
	merged, err := sessionplan.Merge(cfg, "/tmp/ws", nil, false)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if merged.Image.Ref != "harbor.corp.io/ghcr-proxy/filippolmt/toolbox:latest" {
		t.Errorf("Image.Ref = %q, want mirror-swapped ref", merged.Image.Ref)
	}
	if merged.Image.PullPolicy != "never" {
		t.Errorf("PullPolicy = %q, want never", merged.Image.PullPolicy)
	}
}

// TestMergeFullImageOverrideWins asserts an explicit image: ref beats a
// configured registry_mirror.
func TestMergeFullImageOverrideWins(t *testing.T) {
	cfg := &config.Config{Shell: "zsh", Image: "ghcr.io/x/y:dev", RegistryMirror: "ignored.example/proxy"}
	merged, err := sessionplan.Merge(cfg, "/tmp/ws", nil, false)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if merged.Image.Ref != "ghcr.io/x/y:dev" {
		t.Errorf("Image.Ref = %q, want full override to win", merged.Image.Ref)
	}
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

	plan, err := sessionplan.Plan(testConfig(), workspace, nil, false, "")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	if len(plan.Binds) == 0 {
		t.Fatal("Binds is empty; expected mountplan.Plan to populate")
	}

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

	plan, err := sessionplan.Plan(testConfig(), workspace, []string{"7171:7171", "8080:8080"}, false, "")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	if len(plan.ExposedPorts) != 2 {
		t.Errorf("ExposedPorts size = %d, want 2", len(plan.ExposedPorts))
	}
	for _, want := range []network.Port{network.MustParsePort("7171/tcp"), network.MustParsePort("8080/tcp")} {
		if _, ok := plan.ExposedPorts[want]; !ok {
			t.Errorf("ExposedPorts missing %s; got %v", want, plan.ExposedPorts)
		}
		bindings := plan.PortBindings[want]
		if len(bindings) == 0 {
			t.Errorf("PortBindings[%s] empty", want)
			continue
		}
		if bindings[0].HostIP.String() != "127.0.0.1" {
			t.Errorf("PortBindings[%s][0].HostIP = %q, want 127.0.0.1", want, bindings[0].HostIP)
		}
	}
}

// TestPlanComputesContainerName asserts plan.ContainerName equals the
// standalone ContainerNameFor helper byte-for-byte.
func TestPlanComputesContainerName(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	workspace := filepath.Join(tmpHome, "ws")
	if err := mkdirAll(t, workspace); err != nil {
		t.Fatalf("setup: %v", err)
	}

	plan, err := sessionplan.Plan(testConfig(), workspace, nil, false, "")
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

func TestPlanContainerNameDeterministic(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	workspace := filepath.Join(tmpHome, "ws")
	if err := mkdirAll(t, workspace); err != nil {
		t.Fatalf("setup: %v", err)
	}

	a, err := sessionplan.Plan(testConfig(), workspace, nil, false, "")
	if err != nil {
		t.Fatalf("Plan a: %v", err)
	}
	b, err := sessionplan.Plan(testConfig(), workspace, nil, false, "")
	if err != nil {
		t.Fatalf("Plan b: %v", err)
	}
	if a.ContainerName != b.ContainerName {
		t.Errorf("ContainerName not deterministic: %q vs %q", a.ContainerName, b.ContainerName)
	}

	parts := strings.Split(a.ContainerName, "-")
	if len(parts) < 3 {
		t.Fatalf("ContainerName format unexpected: %q", a.ContainerName)
	}
	hash := parts[len(parts)-1]
	if len(hash) != 8 {
		t.Errorf("hash suffix length = %d, want 8 (name=%q)", len(hash), a.ContainerName)
	}
}

func TestPlanComputesEnv(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	workspace := filepath.Join(tmpHome, "ws")
	if err := mkdirAll(t, workspace); err != nil {
		t.Fatalf("setup: %v", err)
	}

	plan, err := sessionplan.Plan(testConfig(), workspace, nil, false, "")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	want := []string{
		"TOOLBOX_HOST_WORKSPACE=" + workspace,
		"PWD=" + plan.WorkingDir,
		"CLAUDE_REMOTE_CONTROL_SESSION_NAME_PREFIX=" + filepath.Base(workspace),
		"TOOLBOX_CLI_VERSION=" + version.Version,
	}
	if !slices.Equal(plan.Env, want) {
		t.Errorf("Env = %v, want %v", plan.Env, want)
	}
}

// TestPlanInjectsImageIdentity asserts the self-identity contract the
// in-container update poller depends on: TOOLBOX_CLI_VERSION is always
// emitted, and TOOLBOX_IMAGE_DIGEST appears verbatim when a digest is
// supplied but is omitted entirely (not emitted empty) when it is not.
func TestPlanInjectsImageIdentity(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	workspace := filepath.Join(tmpHome, "ws")
	if err := mkdirAll(t, workspace); err != nil {
		t.Fatalf("setup: %v", err)
	}

	digest := "sha256:" + strings.Repeat("a", 64)
	plan, err := sessionplan.Plan(testConfig(), workspace, nil, false, digest)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if !slices.Contains(plan.Env, "TOOLBOX_IMAGE_DIGEST="+digest) {
		t.Errorf("Env missing TOOLBOX_IMAGE_DIGEST=%s; got %v", digest, plan.Env)
	}
	if !slices.Contains(plan.Env, "TOOLBOX_CLI_VERSION="+version.Version) {
		t.Errorf("Env missing TOOLBOX_CLI_VERSION=%s; got %v", version.Version, plan.Env)
	}

	bare, err := sessionplan.Plan(testConfig(), workspace, nil, false, "")
	if err != nil {
		t.Fatalf("Plan (no digest): %v", err)
	}
	for _, e := range bare.Env {
		if strings.HasPrefix(e, "TOOLBOX_IMAGE_DIGEST=") {
			t.Errorf("Env should omit TOOLBOX_IMAGE_DIGEST when digest unresolved; got %q", e)
		}
	}
	if !slices.Contains(bare.Env, "TOOLBOX_CLI_VERSION="+version.Version) {
		t.Errorf("Env missing TOOLBOX_CLI_VERSION when digest unresolved; got %v", bare.Env)
	}
}

// TestPlanManagedStatuslineOptOut asserts the managed-statusline opt-out
// contract that init.d/35-statusline.sh reads: TOOLBOX_MANAGED_STATUSLINE=0 is
// emitted only when managed_statusline is explicitly false; nil (default) and
// true emit nothing so the boot hook's default-on path runs.
func TestPlanManagedStatuslineOptOut(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	workspace := filepath.Join(tmpHome, "ws")
	if err := mkdirAll(t, workspace); err != nil {
		t.Fatalf("setup: %v", err)
	}

	hasOptOut := func(env []string) bool {
		return slices.Contains(env, "TOOLBOX_MANAGED_STATUSLINE=0")
	}

	optOut := false
	on := true
	cases := []struct {
		name    string
		managed *bool
		want    bool
	}{
		{"default nil → managed", nil, false},
		{"true → managed", &on, false},
		{"false → opt out", &optOut, true},
	}
	for _, tc := range cases {
		cfg := testConfig()
		cfg.ManagedStatusline = tc.managed
		plan, err := sessionplan.Plan(cfg, workspace, nil, false, "")
		if err != nil {
			t.Fatalf("%s: Plan: %v", tc.name, err)
		}
		if got := hasOptOut(plan.Env); got != tc.want {
			t.Errorf("%s: TOOLBOX_MANAGED_STATUSLINE=0 present=%v, want %v; env=%v", tc.name, got, tc.want, plan.Env)
		}
	}
}

// TestPlanSanitizesRemoteControlPrefix asserts the Remote Control session
// prefix shares ContainerNameFor's slug rule (lowercase, [^a-z0-9]+ → "-")
// instead of forwarding the raw basename.
func TestPlanSanitizesRemoteControlPrefix(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	workspace := filepath.Join(tmpHome, "My Project!")
	if err := mkdirAll(t, workspace); err != nil {
		t.Fatalf("setup: %v", err)
	}

	plan, err := sessionplan.Plan(testConfig(), workspace, nil, false, "")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	if !slices.Contains(plan.Env, "CLAUDE_REMOTE_CONTROL_SESSION_NAME_PREFIX=my-project") {
		t.Errorf("Env = %v, want sanitized prefix my-project", plan.Env)
	}
}

// TestPlanUserEnvAppendedAfterCurated asserts cfg.Env pairs are emitted after
// the curated TOOLBOX_*/PWD entries, sorted by key for deterministic output.
func TestPlanUserEnvAppendedAfterCurated(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	workspace := filepath.Join(tmpHome, "ws")
	if err := mkdirAll(t, workspace); err != nil {
		t.Fatalf("setup: %v", err)
	}

	cfg := testConfig()
	cfg.Env = map[string]string{"ZED": "z", "CLAUDE_CODE_WORKFLOWS": "1", "EMPTY": ""}

	plan, err := sessionplan.Plan(cfg, workspace, nil, false, "")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	want := []string{
		"TOOLBOX_HOST_WORKSPACE=" + workspace,
		"PWD=" + plan.WorkingDir,
		"CLAUDE_REMOTE_CONTROL_SESSION_NAME_PREFIX=ws",
		"TOOLBOX_CLI_VERSION=" + version.Version,
		"CLAUDE_CODE_WORKFLOWS=1",
		"EMPTY=",
		"ZED=z",
	}
	if !slices.Equal(plan.Env, want) {
		t.Errorf("Env = %v, want %v", plan.Env, want)
	}
}

// TestMergeUserEnvAppended mirrors the fs-free Merge path: user env survives
// the pure-data plan shape too.
func TestMergeUserEnvAppended(t *testing.T) {
	cfg := testConfig()
	cfg.Env = map[string]string{"FOO": "bar"}

	merged, err := sessionplan.Merge(cfg, "/workspace", nil, false)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if got := indexEnv(merged.Env)["FOO"]; got != "bar" {
		t.Errorf("Env[FOO] = %q, want %q (full: %v)", got, "bar", merged.Env)
	}
}

// TestPlanSDDEnvAppendedWhenEnabled asserts the field-per-env-var contract:
// each enabled skill emits a TOOLBOX_SDD_<KEY>_{PKG,VERSION,BIN,STEPS,MARKER}
// quintet on top of TOOLBOX_SDD_ENABLED + TOOLBOX_SDD_WORKSPACE_HASH.
func TestPlanSDDEnvAppendedWhenEnabled(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	workspace := filepath.Join(tmpHome, "ws")
	if err := mkdirAll(t, workspace); err != nil {
		t.Fatalf("setup: %v", err)
	}

	cfg := testConfig()
	cfg.SDD = map[string]config.SDDSkill{"gsd": {Enabled: true}}

	plan, err := sessionplan.Plan(cfg, workspace, nil, false, "")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	envByKey := indexEnv(plan.Env)
	for _, want := range []struct{ key, prefix string }{
		{"TOOLBOX_SDD_ENABLED", "gsd"},
		{"TOOLBOX_SDD_WORKSPACE_HASH", ""},
		{"TOOLBOX_SDD_GSD_PKG", "@opengsd/gsd-core"},
		{"TOOLBOX_SDD_GSD_VERSION", ""},
		{"TOOLBOX_SDD_GSD_BIN", "gsd-core"},
		// Claude step is the workspace-local skill-form layout (#317):
		// hyphen-routable /gsd-<cmd> skills under ./.claude/skills/.
		{"TOOLBOX_SDD_GSD_STEPS", "--claude --global --config-dir ./.claude;--codex --local"},
		{"TOOLBOX_SDD_GSD_MARKER", ""},
	} {
		got, ok := envByKey[want.key]
		if !ok {
			t.Errorf("missing env %s in %v", want.key, plan.Env)
			continue
		}
		if want.prefix != "" && got != want.prefix {
			t.Errorf("env %s = %q, want %q", want.key, got, want.prefix)
		}
	}
	if got := envByKey["TOOLBOX_SDD_GSD_VERSION"]; got == "" {
		t.Error("TOOLBOX_SDD_GSD_VERSION must be populated from the registry")
	}
	if got := envByKey["TOOLBOX_SDD_WORKSPACE_HASH"]; len(got) != sessionplan.WorkspaceHashLen {
		t.Errorf("TOOLBOX_SDD_WORKSPACE_HASH = %q, want %d hex chars", got, sessionplan.WorkspaceHashLen)
	}
}

// TestPlanSDDEnvCarriesBMADMarker locks in the bmad-specific gate.
func TestPlanSDDEnvCarriesBMADMarker(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	workspace := filepath.Join(tmpHome, "ws")
	if err := mkdirAll(t, workspace); err != nil {
		t.Fatalf("setup: %v", err)
	}

	cfg := testConfig()
	cfg.SDD = map[string]config.SDDSkill{"bmad": {Enabled: true}}

	plan, err := sessionplan.Plan(cfg, workspace, nil, false, "")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	envByKey := indexEnv(plan.Env)
	if got := envByKey["TOOLBOX_SDD_BMAD_MARKER"]; got != "_bmad" {
		t.Errorf("TOOLBOX_SDD_BMAD_MARKER = %q, want %q", got, "_bmad")
	}
}

// TestPlanSDDEnvOpenSpec locks in the static install steps for openspec
// (claude + codex are always installed, no per-tool opt-out).
func TestPlanSDDEnvOpenSpec(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	workspace := filepath.Join(tmpHome, "ws")
	if err := mkdirAll(t, workspace); err != nil {
		t.Fatalf("setup: %v", err)
	}

	cfg := testConfig()
	cfg.SDD = map[string]config.SDDSkill{"openspec": {Enabled: true}}
	plan, err := sessionplan.Plan(cfg, workspace, nil, false, "")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	envByKey := indexEnv(plan.Env)
	want := "init --tools=claude,codex --force;update"
	if got := envByKey["TOOLBOX_SDD_OPENSPEC_STEPS"]; got != want {
		t.Errorf("TOOLBOX_SDD_OPENSPEC_STEPS = %q, want %q", got, want)
	}
}

// TestPlanSDDEnvDropsUnknownKeys covers the silent-drop contract for typos.
func TestPlanSDDEnvDropsUnknownKeys(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	workspace := filepath.Join(tmpHome, "ws")
	if err := mkdirAll(t, workspace); err != nil {
		t.Fatalf("setup: %v", err)
	}

	cfg := testConfig()
	cfg.SDD = map[string]config.SDDSkill{"gds": {Enabled: true}, "gsd": {Enabled: false}}

	plan, err := sessionplan.Plan(cfg, workspace, nil, false, "")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	for _, e := range plan.Env {
		if strings.HasPrefix(e, "TOOLBOX_SDD_") {
			t.Errorf("expected no TOOLBOX_SDD_* env when only unknown/false flags present, got %q", e)
		}
	}
}

// TestPlanSDDEnvStepsOverride locks in the per-skill steps override (#317):
// a .toolbox.yaml steps: list replaces the registry's InstallSteps wholesale
// in the emitted STEPS env var, while the rest of the quintet stays
// registry-sourced.
func TestPlanSDDEnvStepsOverride(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	workspace := filepath.Join(tmpHome, "ws")
	if err := mkdirAll(t, workspace); err != nil {
		t.Fatalf("setup: %v", err)
	}

	cfg := testConfig()
	cfg.SDD = map[string]config.SDDSkill{"gsd": {
		Enabled: true,
		Steps:   [][]string{{"--claude", "--local"}},
	}}

	plan, err := sessionplan.Plan(cfg, workspace, nil, false, "")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	envByKey := indexEnv(plan.Env)
	if got, want := envByKey["TOOLBOX_SDD_GSD_STEPS"], "--claude --local"; got != want {
		t.Errorf("TOOLBOX_SDD_GSD_STEPS = %q, want override %q", got, want)
	}
	if got := envByKey["TOOLBOX_SDD_GSD_PKG"]; got != "@opengsd/gsd-core" {
		t.Errorf("TOOLBOX_SDD_GSD_PKG = %q, want registry package", got)
	}
}

func TestPlanRejectsBadPort(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	workspace := filepath.Join(tmpHome, "ws")
	if err := mkdirAll(t, workspace); err != nil {
		t.Fatalf("setup: %v", err)
	}

	_, err := sessionplan.Plan(testConfig(), workspace, []string{"not-a-port"}, false, "")
	if err == nil {
		t.Fatal("Plan should reject malformed --publish spec")
	}
	if !strings.Contains(err.Error(), "not-a-port") {
		t.Errorf("error %q should mention the bad spec", err.Error())
	}
}

func TestPlanRejectsBadMountsRoot(t *testing.T) {
	cfg := testConfig()
	cfg.MountsRoot = "~"
	_, err := sessionplan.Plan(cfg, "/workspace", nil, false, "")
	if err == nil {
		t.Fatal("Plan should reject bare ~ as mounts_root")
	}
}

func TestPlanWorkspaceNormalizationOnce(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	canonical := filepath.Join(tmpHome, "bar")
	if err := mkdirAll(t, canonical); err != nil {
		t.Fatalf("setup: %v", err)
	}
	dirty := filepath.Join(tmpHome, "foo", "..", "bar")

	plan, err := sessionplan.Plan(testConfig(), dirty, nil, false, "")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	want := sessionplan.ContainerNameFor(canonical)
	if plan.ContainerName != want {
		t.Errorf("ContainerName for dirty path = %q, want canonical %q", plan.ContainerName, want)
	}
}

func TestNamedContainerName(t *testing.T) {
	if got := sessionplan.NamedContainerName("infra"); got != "toolbox-named-infra" {
		t.Fatalf("NamedContainerName(infra) = %q, want toolbox-named-infra", got)
	}
	if got := sessionplan.NamedContainerName("AI Scratch"); got != "toolbox-named-ai-scratch" {
		t.Fatalf("NamedContainerName(AI Scratch) = %q, want toolbox-named-ai-scratch", got)
	}
}

func TestNamedContainerNameDisjointFromHashFormat(t *testing.T) {
	got := sessionplan.NamedContainerName("1a2b3c4d")
	if !strings.HasPrefix(got, sessionplan.ContainerNamePrefix+sessionplan.NamedContainerNameInfix) {
		t.Fatalf("NamedContainerName(hash-shaped) = %q, want toolbox-named- prefix", got)
	}
}

// --- Merge tier (pure data, NO fs side effects) ---

func TestMergeIsPure(t *testing.T) {
	merged, err := sessionplan.Merge(testConfig(), "/workspace", nil, false)
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
	_ = []config.Mount(merged.Binds)

	if merged.WorkingDir != mountplan.WorkspaceTarget {
		t.Errorf("Merge.WorkingDir = %q, want %q", merged.WorkingDir, mountplan.WorkspaceTarget)
	}

	wantEnv := []string{
		"TOOLBOX_HOST_WORKSPACE=/workspace",
		"PWD=" + mountplan.WorkspaceTarget,
		"CLAUDE_REMOTE_CONTROL_SESSION_NAME_PREFIX=workspace",
		"TOOLBOX_CLI_VERSION=" + version.Version,
	}
	if !slices.Equal(merged.Env, wantEnv) {
		t.Errorf("Merge.Env = %v, want %v", merged.Env, wantEnv)
	}
}

func TestMergeRejectsBadMountsRoot(t *testing.T) {
	cfg := testConfig()
	cfg.MountsRoot = "~"
	_, err := sessionplan.Merge(cfg, "/workspace", nil, false)
	if err == nil {
		t.Fatal("Merge should reject bare ~ as mounts_root")
	}
}

func TestMergeRejectsBadPort(t *testing.T) {
	_, err := sessionplan.Merge(testConfig(), "/workspace", []string{"not-a-port"}, false)
	if err == nil {
		t.Fatal("Merge should reject malformed --publish spec")
	}
}

// --- MissingPublishPorts ---

func TestMissingPublishPortsTable(t *testing.T) {
	cases := []struct {
		name        string
		wanted      network.PortMap
		existing    network.PortMap
		nilHostCfg  bool
		wantMissing []string
	}{
		{
			name:        "no_mismatch_when_all_bound",
			wanted:      network.PortMap{network.MustParsePort("7171/tcp"): nil, network.MustParsePort("8080/tcp"): nil},
			existing:    network.PortMap{network.MustParsePort("7171/tcp"): nil, network.MustParsePort("8080/tcp"): nil},
			wantMissing: nil,
		},
		{
			name:        "missing_port_reported",
			wanted:      network.PortMap{network.MustParsePort("7171/tcp"): nil, network.MustParsePort("8080/tcp"): nil},
			existing:    network.PortMap{network.MustParsePort("7171/tcp"): nil},
			wantMissing: []string{"8080/tcp"},
		},
		{
			name:        "empty_existing_reports_all",
			wanted:      network.PortMap{network.MustParsePort("7171/tcp"): nil},
			existing:    network.PortMap{},
			wantMissing: []string{"7171/tcp"},
		},
		{
			name:        "nil_hostconfig_returns_nil",
			wanted:      network.PortMap{network.MustParsePort("7171/tcp"): nil},
			nilHostCfg:  true,
			wantMissing: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var inspect container.InspectResponse
			if !tc.nilHostCfg {
				inspect = container.InspectResponse{
					HostConfig: &container.HostConfig{PortBindings: tc.existing},
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

// --- Cmd / SecurityOpt ---

func TestPlanComputesCmd(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	workspace := filepath.Join(tmpHome, "ws")
	if err := mkdirAll(t, workspace); err != nil {
		t.Fatalf("setup: %v", err)
	}

	cfg := testConfig()
	cfg.Shell = "zsh"
	plan, err := sessionplan.Plan(cfg, workspace, nil, false, "")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if !slices.Equal(plan.Cmd, []string{"/bin/zsh"}) {
		t.Errorf("Cmd = %v, want [/bin/zsh]", plan.Cmd)
	}
}

// TestPlanComputesSecurityOpt: codex is always installed → always
// returns seccomp=unconfined.
func TestPlanComputesSecurityOpt(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	workspace := filepath.Join(tmpHome, "ws")
	if err := mkdirAll(t, workspace); err != nil {
		t.Fatalf("setup: %v", err)
	}

	plan, err := sessionplan.Plan(testConfig(), workspace, nil, false, "")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if !slices.Equal(plan.SecurityOpt, []string{"seccomp=unconfined"}) {
		t.Errorf("SecurityOpt = %v, want [seccomp=unconfined]", plan.SecurityOpt)
	}
}

func TestMergeAlsoComputesCmd(t *testing.T) {
	cfg := testConfig()
	cfg.Shell = "zsh"
	merged, err := sessionplan.Merge(cfg, "/workspace", nil, false)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if !slices.Equal(merged.Cmd, []string{"/bin/zsh"}) {
		t.Errorf("Merge.Cmd = %v, want [/bin/zsh]", merged.Cmd)
	}
}

// --- Loopback bridge env emission ---

func TestPlanLoopbackBridgeOff(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	workspace := filepath.Join(tmpHome, "ws")
	if err := mkdirAll(t, workspace); err != nil {
		t.Fatalf("setup: %v", err)
	}

	plan, err := sessionplan.Plan(testConfig(), workspace, []string{"13387:13387"}, false, "")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	envByKey := indexEnv(plan.Env)
	for _, key := range []string{"TOOLBOX_LOOPBACK_BRIDGE_PORTS", "TOOLBOX_LOOPBACK_BRIDGE_NO_PUBLISH"} {
		if _, present := envByKey[key]; present {
			t.Errorf("env contains %s with bridgeLoopback=false; should be absent", key)
		}
	}
}

func TestPlanLoopbackBridgeSinglePort(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	workspace := filepath.Join(tmpHome, "ws")
	if err := mkdirAll(t, workspace); err != nil {
		t.Fatalf("setup: %v", err)
	}

	plan, err := sessionplan.Plan(testConfig(), workspace, []string{"13387:13387"}, true, "")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	envByKey := indexEnv(plan.Env)
	if got := envByKey["TOOLBOX_LOOPBACK_BRIDGE_PORTS"]; got != "13387" {
		t.Errorf("TOOLBOX_LOOPBACK_BRIDGE_PORTS = %q, want \"13387\"", got)
	}
	if _, present := envByKey["TOOLBOX_LOOPBACK_BRIDGE_NO_PUBLISH"]; present {
		t.Error("TOOLBOX_LOOPBACK_BRIDGE_NO_PUBLISH must be absent when ports are published")
	}
}

func TestPlanLoopbackBridgeMultiPortPreservesOrder(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	workspace := filepath.Join(tmpHome, "ws")
	if err := mkdirAll(t, workspace); err != nil {
		t.Fatalf("setup: %v", err)
	}

	plan, err := sessionplan.Plan(testConfig(), workspace, []string{"13387:13387", "8976:8976"}, true, "")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	envByKey := indexEnv(plan.Env)
	if got := envByKey["TOOLBOX_LOOPBACK_BRIDGE_PORTS"]; got != "13387,8976" {
		t.Errorf("TOOLBOX_LOOPBACK_BRIDGE_PORTS = %q, want %q (insertion order preserved)", got, "13387,8976")
	}
}

func TestPlanLoopbackBridgeDeduplicatesContainerPorts(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	workspace := filepath.Join(tmpHome, "ws")
	if err := mkdirAll(t, workspace); err != nil {
		t.Fatalf("setup: %v", err)
	}

	plan, err := sessionplan.Plan(testConfig(), workspace, []string{"13387:13387", "9999:13387"}, true, "")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	envByKey := indexEnv(plan.Env)
	if got := envByKey["TOOLBOX_LOOPBACK_BRIDGE_PORTS"]; got != "13387" {
		t.Errorf("TOOLBOX_LOOPBACK_BRIDGE_PORTS = %q, want %q (duplicates collapsed)", got, "13387")
	}
}

func TestPlanLoopbackBridgeEmptyPublish(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	workspace := filepath.Join(tmpHome, "ws")
	if err := mkdirAll(t, workspace); err != nil {
		t.Fatalf("setup: %v", err)
	}

	plan, err := sessionplan.Plan(testConfig(), workspace, nil, true, "")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	envByKey := indexEnv(plan.Env)
	if envByKey["TOOLBOX_LOOPBACK_BRIDGE_NO_PUBLISH"] != "1" {
		t.Errorf("TOOLBOX_LOOPBACK_BRIDGE_NO_PUBLISH = %q, want \"1\"", envByKey["TOOLBOX_LOOPBACK_BRIDGE_NO_PUBLISH"])
	}
	if _, present := envByKey["TOOLBOX_LOOPBACK_BRIDGE_PORTS"]; present {
		t.Errorf("TOOLBOX_LOOPBACK_BRIDGE_PORTS must be absent when no -p; got %q", envByKey["TOOLBOX_LOOPBACK_BRIDGE_PORTS"])
	}
}

// --- Helpers ---

func indexEnv(env []string) map[string]string {
	out := make(map[string]string, len(env))
	for _, e := range env {
		if k, v, ok := strings.Cut(e, "="); ok {
			out[k] = v
		}
	}
	return out
}

func mkdirAll(t *testing.T, path string) error {
	t.Helper()
	return os.MkdirAll(path, 0o755)
}
