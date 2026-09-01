package sessionplan_test

import (
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"

	"github.com/filippolmt/toolbox/internal/bridge"
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

// planWorkspace installs a temp HOME plus an existing workspace directory —
// the fixture the mount stage's filesystem side effects need — and returns
// the workspace path.
func planWorkspace(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	ws := filepath.Join(home, "ws")
	if err := mkdirAll(t, ws); err != nil {
		t.Fatalf("setup: %v", err)
	}
	return ws
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

	plan, err := sessionplan.Plan(sessionplan.PlanInput{Cfg: testConfig(), Workspace: workspace})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.Image.Ref != "ghcr.io/filippolmt/toolbox:latest" {
		t.Errorf("Image.Ref = %q, want canonical GHCR tag", plan.Image.Ref)
	}
}

// TestPlanNameDecidesContainerName asserts the container-name decision lives
// behind the seam: a non-empty PlanInput.Name yields the named form
// (toolbox-named-<name>), while the same workspace with no Name yields the
// path-hash form. Everything else in the plan is identical.
func TestPlanNameDecidesContainerName(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	workspace := filepath.Join(tmpHome, "ws")
	if err := mkdirAll(t, workspace); err != nil {
		t.Fatalf("setup: %v", err)
	}

	named, err := sessionplan.Plan(sessionplan.PlanInput{Cfg: testConfig(), Workspace: workspace, Name: "web"})
	if err != nil {
		t.Fatalf("Plan (named): %v", err)
	}
	if want := "toolbox-named-web"; named.ContainerName != want {
		t.Errorf("ContainerName = %q, want %q", named.ContainerName, want)
	}

	plain, err := sessionplan.Plan(sessionplan.PlanInput{Cfg: testConfig(), Workspace: workspace})
	if err != nil {
		t.Fatalf("Plan (workspace): %v", err)
	}
	if named.ContainerName == plain.ContainerName {
		t.Errorf("named and workspace container names collided: %q", named.ContainerName)
	}
	// Only the container name differs; the rest of the plan matches.
	named.ContainerName = plain.ContainerName
	if !reflect.DeepEqual(named, plain) {
		t.Errorf("named plan diverges beyond ContainerName:\n named=%+v\n plain=%+v", named, plain)
	}
}

// TestPlanNameIsRawAndSanitizedHere asserts PlanInput.Name is the name as the
// user typed it and the sanitization happens behind the seam: case and
// surrounding blanks reach the same container as the canonical spelling, and a
// blanks-only name falls back to the workspace-hash form.
func TestPlanNameIsRawAndSanitizedHere(t *testing.T) {
	workspace := planWorkspace(t)
	name := func(n string) string {
		t.Helper()
		plan, err := sessionplan.Plan(sessionplan.PlanInput{Cfg: testConfig(), Workspace: workspace, Name: n})
		if err != nil {
			t.Fatalf("Plan(%q): %v", n, err)
		}
		return plan.ContainerName
	}

	for _, raw := range []string{"Infra", " infra ", "INFRA"} {
		if got := name(raw); got != "toolbox-named-infra" {
			t.Errorf("ContainerName for %q = %q, want toolbox-named-infra", raw, got)
		}
	}
	if got, want := name("   "), name(""); got != want {
		t.Errorf("blanks-only name = %q, want the workspace form %q", got, want)
	}
}

// TestPlanOverlaysNamedShellEnv asserts the per-shell env: enters through the
// seam — Plan resolves it from the raw Name — and that resolving it leaves the
// caller's Config untouched (the top-level env: used to be overwritten in cmd).
func TestPlanOverlaysNamedShellEnv(t *testing.T) {
	workspace := planWorkspace(t)
	cfg := testConfig()
	cfg.Env = map[string]string{"SHARED": "global", "GLOBAL_ONLY": "g"}
	cfg.Shells = map[string]config.NamedShell{
		"infra": {Path: "/tmp/infra", Env: map[string]string{"SHARED": "shell", "SHELL_ONLY": "s"}},
	}

	plan, err := sessionplan.Plan(sessionplan.PlanInput{Cfg: cfg, Workspace: workspace, Name: "Infra"})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	env := indexEnv(plan.Env)
	for k, want := range map[string]string{"SHARED": "shell", "GLOBAL_ONLY": "g", "SHELL_ONLY": "s"} {
		if env[k] != want {
			t.Errorf("Env[%q] = %q, want %q (full: %v)", k, env[k], want, plan.Env)
		}
	}

	if cfg.Env["SHARED"] != "global" || len(cfg.Env) != 2 {
		t.Errorf("cfg.Env mutated by planning: %v", cfg.Env)
	}

	// A workspace session sees the top-level layer only.
	plain, err := sessionplan.Plan(sessionplan.PlanInput{Cfg: cfg, Workspace: workspace})
	if err != nil {
		t.Fatalf("Plan (workspace): %v", err)
	}
	if got := indexEnv(plain.Env); got["SHARED"] != "global" || got["SHELL_ONLY"] != "" {
		t.Errorf("workspace session env = %v, want the top-level layer only", plain.Env)
	}
}

// TestPlanImageSelectionAndPullPolicy asserts registry_mirror relocates the
// host (path+tag preserved) and the pull policy propagates onto the Image.
func TestPlanImageSelectionAndPullPolicy(t *testing.T) {
	workspace := planWorkspace(t)
	cfg := &config.Config{Shell: "zsh", RegistryMirror: "harbor.corp.io/ghcr-proxy", Pull: "never"}
	plan, err := sessionplan.Plan(sessionplan.PlanInput{Cfg: cfg, Workspace: workspace})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.Image.Ref != "harbor.corp.io/ghcr-proxy/filippolmt/toolbox:latest" {
		t.Errorf("Image.Ref = %q, want mirror-swapped ref", plan.Image.Ref)
	}
	if plan.Image.PullPolicy != "never" {
		t.Errorf("PullPolicy = %q, want never", plan.Image.PullPolicy)
	}
}

// TestPlanFullImageOverrideWins asserts an explicit image: ref beats a
// configured registry_mirror.
func TestPlanFullImageOverrideWins(t *testing.T) {
	workspace := planWorkspace(t)
	cfg := &config.Config{Shell: "zsh", Image: "ghcr.io/x/y:dev", RegistryMirror: "ignored.example/proxy"}
	plan, err := sessionplan.Plan(sessionplan.PlanInput{Cfg: cfg, Workspace: workspace})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.Image.Ref != "ghcr.io/x/y:dev" {
		t.Errorf("Image.Ref = %q, want full override to win", plan.Image.Ref)
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

	plan, err := sessionplan.Plan(sessionplan.PlanInput{Cfg: testConfig(), Workspace: workspace})
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

	plan, err := sessionplan.Plan(sessionplan.PlanInput{Cfg: testConfig(), Workspace: workspace, Ports: []string{"7171:7171", "8080:8080"}})
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

	plan, err := sessionplan.Plan(sessionplan.PlanInput{Cfg: testConfig(), Workspace: workspace})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	want := sessionplan.ContainerNameFor(workspace, "")
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

	a, err := sessionplan.Plan(sessionplan.PlanInput{Cfg: testConfig(), Workspace: workspace})
	if err != nil {
		t.Fatalf("Plan a: %v", err)
	}
	b, err := sessionplan.Plan(sessionplan.PlanInput{Cfg: testConfig(), Workspace: workspace})
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

	plan, err := sessionplan.Plan(sessionplan.PlanInput{Cfg: testConfig(), Workspace: workspace})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	want := []string{
		"TOOLBOX_HOST_WORKSPACE=" + workspace,
		"PWD=" + plan.WorkingDir,
		"CLAUDE_REMOTE_CONTROL_SESSION_NAME_PREFIX=" + filepath.Base(workspace),
		"HERDR_SESSION=" + sessionplan.ContainerNameFor(workspace, ""),
		"TOOLBOX_CLI_VERSION=" + version.Version,
		"TOOLBOX_HOST_OS=" + runtime.GOOS,
		"TOOLBOX_HOST_ARCH=" + runtime.GOARCH,
		bridge.HostAgentHomeEnv + "=" + filepath.Join(tmpHome, ".toolbox"),
		bridge.HostCodexHomeEnv + "=" + filepath.Join(tmpHome, ".toolbox", ".codex"),
	}
	if !slices.Equal(plan.Env, want) {
		t.Errorf("Env = %v, want %v", plan.Env, want)
	}
}

// TestPlanInjectsHostPlatform asserts the host's GOOS/GOARCH reach the
// container unconditionally. They are the only way something running inside
// the container can cross-compile for the host: `uname` there reports the
// container, so a build driven from a toolbox shell would silently target
// linux. Emitted for every session, not just a mirrored or opted-in one.
func TestPlanInjectsHostPlatform(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	workspace := filepath.Join(tmpHome, "ws")
	if err := mkdirAll(t, workspace); err != nil {
		t.Fatalf("setup: %v", err)
	}

	plan, err := sessionplan.Plan(sessionplan.PlanInput{Cfg: testConfig(), Workspace: workspace})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	for _, want := range []string{
		"TOOLBOX_HOST_OS=" + runtime.GOOS,
		"TOOLBOX_HOST_ARCH=" + runtime.GOARCH,
	} {
		if !slices.Contains(plan.Env, want) {
			t.Errorf("Env missing %s; got %v", want, plan.Env)
		}
	}
}

// TestPlanInjectsImageIdentity asserts the self-identity contract the
// container identity depends on: TOOLBOX_CLI_VERSION is always
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
	plan, err := sessionplan.Plan(sessionplan.PlanInput{Cfg: testConfig(), Workspace: workspace, ImageDigest: digest})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if !slices.Contains(plan.Env, "TOOLBOX_IMAGE_DIGEST="+digest) {
		t.Errorf("Env missing TOOLBOX_IMAGE_DIGEST=%s; got %v", digest, plan.Env)
	}
	if !slices.Contains(plan.Env, "TOOLBOX_CLI_VERSION="+version.Version) {
		t.Errorf("Env missing TOOLBOX_CLI_VERSION=%s; got %v", version.Version, plan.Env)
	}

	bare, err := sessionplan.Plan(sessionplan.PlanInput{Cfg: testConfig(), Workspace: workspace})
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
		plan, err := sessionplan.Plan(sessionplan.PlanInput{Cfg: cfg, Workspace: workspace})
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

	plan, err := sessionplan.Plan(sessionplan.PlanInput{Cfg: testConfig(), Workspace: workspace})
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

	plan, err := sessionplan.Plan(sessionplan.PlanInput{Cfg: cfg, Workspace: workspace})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	want := []string{
		"TOOLBOX_HOST_WORKSPACE=" + workspace,
		"PWD=" + plan.WorkingDir,
		"CLAUDE_REMOTE_CONTROL_SESSION_NAME_PREFIX=ws",
		"HERDR_SESSION=" + sessionplan.ContainerNameFor(workspace, ""),
		"TOOLBOX_CLI_VERSION=" + version.Version,
		"TOOLBOX_HOST_OS=" + runtime.GOOS,
		"TOOLBOX_HOST_ARCH=" + runtime.GOARCH,
		bridge.HostAgentHomeEnv + "=" + filepath.Join(tmpHome, ".toolbox"),
		bridge.HostCodexHomeEnv + "=" + filepath.Join(tmpHome, ".toolbox", ".codex"),
		"CLAUDE_CODE_WORKFLOWS=1",
		"EMPTY=",
		"ZED=z",
	}
	if !slices.Equal(plan.Env, want) {
		t.Errorf("Env = %v, want %v", plan.Env, want)
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

	plan, err := sessionplan.Plan(sessionplan.PlanInput{Cfg: cfg, Workspace: workspace})
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

	plan, err := sessionplan.Plan(sessionplan.PlanInput{Cfg: cfg, Workspace: workspace})
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
	plan, err := sessionplan.Plan(sessionplan.PlanInput{Cfg: cfg, Workspace: workspace})
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

	plan, err := sessionplan.Plan(sessionplan.PlanInput{Cfg: cfg, Workspace: workspace})
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

	plan, err := sessionplan.Plan(sessionplan.PlanInput{Cfg: cfg, Workspace: workspace})
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

	_, err := sessionplan.Plan(sessionplan.PlanInput{Cfg: testConfig(), Workspace: workspace, Ports: []string{"not-a-port"}})
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
	_, err := sessionplan.Plan(sessionplan.PlanInput{Cfg: cfg, Workspace: "/workspace"})
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

	plan, err := sessionplan.Plan(sessionplan.PlanInput{Cfg: testConfig(), Workspace: dirty})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	want := sessionplan.ContainerNameFor(canonical, "")
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

// --- ConflictingPublishPorts ---

// hostBinding builds the wanted-side PortMap entry for "<container>/<proto>"
// published on host port hostPort, mirroring what parsePublishSpecs emits.
func hostBinding(containerPort, hostPort string) (network.Port, []network.PortBinding) {
	return network.MustParsePort(containerPort),
		[]network.PortBinding{{HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: hostPort}}
}

func TestConflictingPublishPortsTable(t *testing.T) {
	wantedCF, bindingsCF := hostBinding("8877/tcp", "8877")
	wantedShift, bindingsShift := hostBinding("8877/tcp", "9877")
	wantedUDP, bindingsUDP := hostBinding("53/udp", "53")

	cases := []struct {
		name     string
		wanted   network.PortMap
		occupied map[string]string
		want     []sessionplan.PortConflict
	}{
		{
			name:     "free_port_no_conflict",
			wanted:   network.PortMap{wantedCF: bindingsCF},
			occupied: map[string]string{"9000/tcp": "other"},
			want:     nil,
		},
		{
			name:     "held_port_names_holder",
			wanted:   network.PortMap{wantedCF: bindingsCF},
			occupied: map[string]string{"8877/tcp": "toolbox-other-1234abcd"},
			want:     []sessionplan.PortConflict{{Port: "8877/tcp", Holder: "toolbox-other-1234abcd"}},
		},
		{
			// The host side is what has to be free: a shifted mapping
			// (host 9877 -> container 8877) must not match a holder of 8877.
			name:     "shifted_mapping_matches_host_side_only",
			wanted:   network.PortMap{wantedShift: bindingsShift},
			occupied: map[string]string{"8877/tcp": "other"},
			want:     nil,
		},
		{
			// Protocol is part of the identity: a UDP holder never blocks TCP.
			name:     "protocol_is_part_of_the_key",
			wanted:   network.PortMap{wantedCF: bindingsCF},
			occupied: map[string]string{"8877/udp": "other"},
			want:     nil,
		},
		{
			name:     "udp_conflict_reported",
			wanted:   network.PortMap{wantedUDP: bindingsUDP},
			occupied: map[string]string{"53/udp": "resolver"},
			want:     []sessionplan.PortConflict{{Port: "53/udp", Holder: "resolver"}},
		},
		{
			name: "multiple_conflicts_sorted_by_port",
			wanted: network.PortMap{
				network.MustParsePort("8878/tcp"): {{HostPort: "8878"}},
				network.MustParsePort("8877/tcp"): {{HostPort: "8877"}},
			},
			occupied: map[string]string{"8877/tcp": "a", "8878/tcp": "b"},
			want: []sessionplan.PortConflict{
				{Port: "8877/tcp", Holder: "a"},
				{Port: "8878/tcp", Holder: "b"},
			},
		},
		{
			name:     "no_occupancy_known_no_conflict",
			wanted:   network.PortMap{wantedCF: bindingsCF},
			occupied: nil,
			want:     nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sessionplan.ConflictingPublishPorts(tc.wanted, tc.occupied)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ConflictingPublishPorts = %v, want %v", got, tc.want)
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
	plan, err := sessionplan.Plan(sessionplan.PlanInput{Cfg: cfg, Workspace: workspace})
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

	plan, err := sessionplan.Plan(sessionplan.PlanInput{Cfg: testConfig(), Workspace: workspace})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if !slices.Equal(plan.SecurityOpt, []string{"seccomp=unconfined"}) {
		t.Errorf("SecurityOpt = %v, want [seccomp=unconfined]", plan.SecurityOpt)
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

	plan, err := sessionplan.Plan(sessionplan.PlanInput{Cfg: testConfig(), Workspace: workspace, Ports: []string{"13387:13387"}})
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

	plan, err := sessionplan.Plan(sessionplan.PlanInput{Cfg: testConfig(), Workspace: workspace, Ports: []string{"13387:13387"}, BridgeLoopback: true})
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

	plan, err := sessionplan.Plan(sessionplan.PlanInput{Cfg: testConfig(), Workspace: workspace, Ports: []string{"13387:13387", "8976:8976"}, BridgeLoopback: true})
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

	plan, err := sessionplan.Plan(sessionplan.PlanInput{Cfg: testConfig(), Workspace: workspace, Ports: []string{"13387:13387", "9999:13387"}, BridgeLoopback: true})
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

	plan, err := sessionplan.Plan(sessionplan.PlanInput{Cfg: testConfig(), Workspace: workspace, BridgeLoopback: true})
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

// TestPlanScopesHerdrSessionToWorkspace pins the fix for herdr reopening
// another project's directory — or, when that path is not mounted here,
// falling back to $HOME so the shell lands on /home/toolbox.
//
// ~/.config/herdr is one host-global bind (mountplan defaults, "herdr") and
// herdr persists its workspace list, cwds included, there. On restore it
// ignores the startup cwd ("restored session already has workspaces"), so
// without a per-workspace session name every container reopens whatever
// workspace another project saved last.
//
// Two invariants: distinct workspaces never share a session name (basename
// collisions included — the workspace hash is what separates them), and the
// name is derived from the workspace alone, so a --profile or --peer change,
// which forks the container name over the same workspace, keeps the session.
func TestPlanScopesHerdrSessionToWorkspace(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	herdrSession := func(t *testing.T, workspace string, profile *mountplan.Profile) string {
		t.Helper()
		if err := mkdirAll(t, workspace); err != nil {
			t.Fatalf("setup: %v", err)
		}
		plan, err := sessionplan.Plan(sessionplan.PlanInput{
			Cfg: testConfig(), Workspace: workspace, Profile: profile,
		})
		if err != nil {
			t.Fatalf("Plan: %v", err)
		}
		for _, e := range plan.Env {
			if v, ok := strings.CutPrefix(e, "HERDR_SESSION="); ok {
				return v
			}
		}
		t.Fatalf("Env = %v, want a HERDR_SESSION entry", plan.Env)
		return ""
	}

	// Same basename, different workspaces — the collision `workspaceSlug`
	// alone would not survive.
	a := herdrSession(t, filepath.Join(tmpHome, "one", "api"), nil)
	b := herdrSession(t, filepath.Join(tmpHome, "two", "api"), nil)
	if a == b {
		t.Errorf("HERDR_SESSION = %q for both workspaces, want distinct", a)
	}

	profile, err := mountplan.NewProfile("work", nil)
	if err != nil {
		t.Fatalf("NewProfile: %v", err)
	}
	if got := herdrSession(t, filepath.Join(tmpHome, "one", "api"), profile); got != a {
		t.Errorf("HERDR_SESSION under --profile = %q, want %q (workspace identity only)", got, a)
	}
}

// TestPlanCarriesTheStateMountPath pins StateDir to the bind it names. The
// host-side update prefetch writes into that directory and the in-container
// prompt hook reads it back through the mount, so a StateDir that pointed
// anywhere else would leave the banner silent with nothing to show for it.
func TestPlanCarriesTheStateMountPath(t *testing.T) {
	plan, err := sessionplan.Plan(sessionplan.PlanInput{Cfg: testConfig(), Workspace: planWorkspace(t)})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.StateDir == "" {
		t.Fatal("StateDir is empty")
	}

	const stateTarget = "/home/toolbox/.toolbox-state"
	for _, b := range plan.Binds {
		if b.Target == stateTarget {
			if b.Source != plan.StateDir {
				t.Errorf("StateDir = %q, but the %s bind sources %q", plan.StateDir, stateTarget, b.Source)
			}
			return
		}
	}
	t.Fatalf("no bind at %s to pin StateDir against", stateTarget)
}

// TestWithImageDigest covers the re-stamp the container edge performs after
// the shell-start registry refresh: the digest the host resolved before
// planning can already be superseded by the time the container is created.
func TestWithImageDigest(t *testing.T) {
	const cli = "TOOLBOX_CLI_VERSION=v1.0.0"
	const digestEnv = "TOOLBOX_IMAGE_DIGEST="

	for _, tc := range []struct {
		name   string
		env    []string
		digest string
		want   []string
	}{
		{
			name:   "replaces in place",
			env:    []string{"A=1", cli, digestEnv + "sha256:old", "B=2"},
			digest: "sha256:new",
			want:   []string{"A=1", cli, digestEnv + "sha256:new", "B=2"},
		},
		{
			// An entry the planner omitted (unresolvable at plan time) is
			// inserted where identityEnv would have put it, so the documented
			// emission order survives the re-stamp.
			name:   "inserts after the CLI version",
			env:    []string{"A=1", cli, "B=2"},
			digest: "sha256:new",
			want:   []string{"A=1", cli, digestEnv + "sha256:new", "B=2"},
		},
		{
			// An unresolvable digest removes the entry rather than emitting it
			// empty: a reader must be able to tell "no digest" from "stale".
			name:   "empty digest removes the entry",
			env:    []string{"A=1", cli, digestEnv + "sha256:old"},
			digest: "",
			want:   []string{"A=1", cli},
		},
		{
			name:   "empty digest with no entry is a no-op",
			env:    []string{"A=1", cli},
			digest: "",
			want:   []string{"A=1", cli},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := sessionplan.WithImageDigest(tc.env, tc.digest)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("WithImageDigest = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestEnvValue pins the duplicate-resolution rule: the daemon lets the last
// occurrence win, and reading the first would report a value the container is
// not running under.
func TestEnvValue(t *testing.T) {
	env := []string{"A=1", "B=2", "A=3", "MALFORMED", "C="}
	for _, tc := range []struct{ key, want string }{
		{"A", "3"},
		{"B", "2"},
		{"C", ""},
		{"MALFORMED", ""},
		{"MISSING", ""},
	} {
		if got := sessionplan.EnvValue(env, tc.key); got != tc.want {
			t.Errorf("EnvValue(%q) = %q, want %q", tc.key, got, tc.want)
		}
	}
}
