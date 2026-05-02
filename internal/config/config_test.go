package config

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestDefaultMounts(t *testing.T) {
	mounts := DefaultMounts()

	if len(mounts) != 21 {
		t.Fatalf("expected 21 default mounts, got %d", len(mounts))
	}

	// ~/.secrets must NOT be present (D-08).
	for _, m := range mounts {
		if m.Source == "~/.secrets" {
			t.Error("~/.secrets should NOT be in default mounts (D-08)")
		}
	}

	// Every ~-based source must live under ~/.toolbox/ so host creds are
	// not leaked into the container.
	for _, m := range mounts {
		if strings.HasPrefix(m.Source, "~/") && !strings.HasPrefix(m.Source, "~/.toolbox/") {
			t.Errorf("mount source %q must be under ~/.toolbox/", m.Source)
		}
	}

	// ~/.toolbox/.claude must be rw and auto-created.
	assertMount(t, mounts, "~/.toolbox/.claude", false, true)
	// ~/.toolbox/.codex must be rw and auto-created.
	assertMount(t, mounts, "~/.toolbox/.codex", false, true)
	// ~/.toolbox/state must be rw and auto-created.
	assertMount(t, mounts, "~/.toolbox/state", false, true)
	// Every cloud / forge CLI must have a rw, auto-created state dir.
	assertMount(t, mounts, "~/.toolbox/gh", false, true)
	assertMount(t, mounts, "~/.toolbox/glab", false, true)
	assertMount(t, mounts, "~/.toolbox/gcloud", false, true)
	assertMount(t, mounts, "~/.toolbox/gws", false, true)
	assertMount(t, mounts, "~/.toolbox/azure", false, true)
	assertMount(t, mounts, "~/.toolbox/oci", false, true)
	assertMount(t, mounts, "~/.toolbox/cf/auth", false, true)
	assertMount(t, mounts, "~/.toolbox/cf/config", false, true)
	assertMount(t, mounts, "~/.toolbox/rtk/config", false, true)
	assertMount(t, mounts, "~/.toolbox/rtk/data", false, true)
	assertMount(t, mounts, "~/.toolbox/kube", false, true)
	assertMount(t, mounts, "~/.toolbox/playwright-cache", false, true)
	// User-defined hooks dir: read-only, create-if-missing.
	assertMount(t, mounts, "~/.toolbox/startup.d", true, true)
	// Per-user npm prefix: read-write, create-if-missing.
	assertMount(t, mounts, "~/.toolbox/npm-global", false, true)
	// Per-user Go workspace (GOPATH): read-write, create-if-missing (GO-05).
	assertMount(t, mounts, "~/.toolbox/go", false, true)

	// ssh + git config follow the host via symlinks, not copies.
	assertSymlink(t, mounts, "~/.toolbox/ssh", "~/.ssh")
	assertSymlink(t, mounts, "~/.toolbox/gitconfig", "~/.gitconfig")
}

func assertMount(t *testing.T, mounts []Mount, src string, wantRO, wantCreate bool) {
	t.Helper()
	for _, m := range mounts {
		if m.Source != src {
			continue
		}
		if m.ReadOnly != wantRO {
			t.Errorf("%s: ReadOnly = %v, want %v", src, m.ReadOnly, wantRO)
		}
		if m.CreateIfMissing != wantCreate {
			t.Errorf("%s: CreateIfMissing = %v, want %v", src, m.CreateIfMissing, wantCreate)
		}
		return
	}
	t.Errorf("mount %s not found in DefaultMounts()", src)
}

func assertSymlink(t *testing.T, mounts []Mount, src, wantLink string) {
	t.Helper()
	for _, m := range mounts {
		if m.Source != src {
			continue
		}
		if m.SymlinkFrom != wantLink {
			t.Errorf("%s: SymlinkFrom = %q, want %q", src, m.SymlinkFrom, wantLink)
		}
		return
	}
	t.Errorf("mount %s not found in DefaultMounts()", src)
}

// setToolsDefaults mirrors the per-leaf defaults from cmd/root.go.
func setToolsDefaults() {
	for _, k := range KnownTools {
		viper.SetDefault("tools."+k, true)
	}
}

func TestLoadWithoutConfig(t *testing.T) {
	viper.Reset()
	setToolsDefaults()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if len(cfg.Mounts) != 21 {
		t.Errorf("expected 21 default mounts, got %d", len(cfg.Mounts))
	}

	if !IsDefaultTools(cfg.Tools) {
		t.Errorf("Load() with no user config should yield default tools, got %v", cfg.Tools)
	}
	for _, k := range KnownTools {
		if !cfg.Tools[k] {
			t.Errorf("tool %q should default to true", k)
		}
	}
}

// TestLoadUserOverridePreservesOtherTools reproduces the merge semantics:
// a .toolbox.yaml that flips a single tool must leave every other default
// untouched. This is the main contract the user asked for — "override solo
// le chiavi modificate, il resto eredita dalla globale".
func TestLoadUserOverridePreservesOtherTools(t *testing.T) {
	viper.Reset()
	setToolsDefaults()

	viper.SetConfigType("yaml")
	if err := viper.ReadConfig(bytes.NewBufferString("tools:\n  gcloud: false\n")); err != nil {
		t.Fatalf("viper.ReadConfig: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Tools["gcloud"] {
		t.Error("gcloud should be disabled after override")
	}
	for _, k := range KnownTools {
		if k == "gcloud" {
			continue
		}
		if !cfg.Tools[k] {
			t.Errorf("tool %q should remain true — one-key override must not reset others", k)
		}
	}
	if IsDefaultTools(cfg.Tools) {
		t.Error("IsDefaultTools should be false once any tool is opted out")
	}
}

func TestIsDefaultTools(t *testing.T) {
	if !IsDefaultTools(DefaultTools()) {
		t.Error("DefaultTools() should be recognised as default")
	}
	if !IsDefaultTools(nil) {
		t.Error("nil map (no user config) should be treated as default")
	}
	if !IsDefaultTools(map[string]bool{}) {
		t.Error("empty map should be treated as default (all keys default-true)")
	}
	custom := DefaultTools()
	custom["gcloud"] = false
	if IsDefaultTools(custom) {
		t.Error("one tool disabled should not be considered default")
	}
}

// TestToolBuildArgGo cross-checks that the Go toolchain key maps to the
// correct Dockerfile ARG. This is the in-code half of GO-04 cascade; the
// Dockerfile half is enforced end-to-end by the smoke test in Plan 03.
func TestToolBuildArgGo(t *testing.T) {
	if got := ToolBuildArg["go"]; got != "INSTALL_GO" {
		t.Errorf("ToolBuildArg[\"go\"] = %q, want %q", got, "INSTALL_GO")
	}
}

// TestDefaultMountsHaveNames guards the Name-based merge contract: every
// default mount must carry a non-empty, unique Name so mounts: patches and
// replacements can target it.
func TestDefaultMountsHaveNames(t *testing.T) {
	mounts := DefaultMounts()
	seen := map[string]struct{}{}
	for _, m := range mounts {
		if m.Name == "" {
			t.Errorf("default mount with target %q has empty Name", m.Target)
			continue
		}
		if _, dup := seen[m.Name]; dup {
			t.Errorf("default mount Name %q is not unique", m.Name)
		}
		seen[m.Name] = struct{}{}
	}
}

// TestLoadMountPatchRetargetsSource is the main contract for the unified
// mounts: knob: a name-only patch must change only the Source of the named
// mount and leave every other mount untouched.
func TestLoadMountPatchRetargetsSource(t *testing.T) {
	viper.Reset()
	setToolsDefaults()

	viper.SetConfigType("yaml")
	yaml := "mounts:\n  - name: gws\n    source: /custom/gws\n"
	if err := viper.ReadConfig(bytes.NewBufferString(yaml)); err != nil {
		t.Fatalf("viper.ReadConfig: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	gws := findMount(cfg.Mounts, "gws")
	if gws == nil {
		t.Fatal("gws mount missing after patch")
	}
	if gws.Source != "/custom/gws" {
		t.Errorf("gws Source = %q, want %q", gws.Source, "/custom/gws")
	}
	if gws.Target != "/home/toolbox/.config/gws" {
		t.Errorf("gws Target should be untouched, got %q", gws.Target)
	}
	if !gws.CreateIfMissing {
		t.Error("gws CreateIfMissing should remain true (default), patch must not reset it")
	}

	// Every other default Source must remain intact.
	for _, d := range DefaultMounts() {
		if d.Name == "gws" {
			continue
		}
		got := findMount(cfg.Mounts, d.Name)
		if got == nil {
			t.Errorf("mount %q missing after unrelated patch", d.Name)
			continue
		}
		if got.Source != d.Source {
			t.Errorf("mount %q Source drifted: got %q, want %q", d.Name, got.Source, d.Source)
		}
	}
}

// TestLoadMountPatchUnknownNameErrors guards the typo-detection contract:
// a name-only patch referencing a non-existent mount must fail Load().
func TestLoadMountPatchUnknownNameErrors(t *testing.T) {
	viper.Reset()
	setToolsDefaults()

	viper.SetConfigType("yaml")
	yaml := "mounts:\n  - name: nonexistent\n    source: /tmp/x\n"
	if err := viper.ReadConfig(bytes.NewBufferString(yaml)); err != nil {
		t.Fatalf("viper.ReadConfig: %v", err)
	}

	if _, err := Load(); err == nil {
		t.Fatal("Load() should fail when a patch references an unknown name")
	} else if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("error should mention the unknown name, got: %v", err)
	}
}

// TestLoadMountReplaceByName: a user entry with the same Name as a default
// AND a Target must replace the default entry wholesale.
func TestLoadMountReplaceByName(t *testing.T) {
	viper.Reset()
	setToolsDefaults()

	viper.SetConfigType("yaml")
	yaml := "" +
		"mounts:\n" +
		"  - name: gws\n" +
		"    source: /custom/gws\n" +
		"    target: /opt/gws\n" +
		"    readonly: true\n"
	if err := viper.ReadConfig(bytes.NewBufferString(yaml)); err != nil {
		t.Fatalf("viper.ReadConfig: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	gws := findMount(cfg.Mounts, "gws")
	if gws == nil {
		t.Fatal("gws mount missing after replace")
	}
	if gws.Target != "/opt/gws" {
		t.Errorf("gws Target = %q, want %q", gws.Target, "/opt/gws")
	}
	if !gws.ReadOnly {
		t.Error("gws ReadOnly should be true after replace")
	}
	if gws.CreateIfMissing {
		t.Error("gws CreateIfMissing should be false after replace (user did not set it)")
	}

	// Mount count unchanged: replace, not append.
	if len(cfg.Mounts) != len(DefaultMounts()) {
		t.Errorf("expected %d mounts after replace, got %d", len(DefaultMounts()), len(cfg.Mounts))
	}
}

// TestLoadMountAppendNewName: a user entry whose Name is not in the defaults
// (or anonymous) is appended to the resolved set.
func TestLoadMountAppendNewName(t *testing.T) {
	viper.Reset()
	setToolsDefaults()

	viper.SetConfigType("yaml")
	yaml := "" +
		"mounts:\n" +
		"  - name: project-data\n" +
		"    source: /opt/data\n" +
		"    target: /data\n"
	if err := viper.ReadConfig(bytes.NewBufferString(yaml)); err != nil {
		t.Fatalf("viper.ReadConfig: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if len(cfg.Mounts) != len(DefaultMounts())+1 {
		t.Errorf("expected %d mounts, got %d", len(DefaultMounts())+1, len(cfg.Mounts))
	}
	added := findMount(cfg.Mounts, "project-data")
	if added == nil {
		t.Fatal("project-data mount missing after append")
	}
	if added.Target != "/data" {
		t.Errorf("project-data Target = %q, want %q", added.Target, "/data")
	}
}

// TestLoadMountDisableDefault: a patch with disabled: true removes the
// default from the resolved set.
func TestLoadMountDisableDefault(t *testing.T) {
	viper.Reset()
	setToolsDefaults()

	viper.SetConfigType("yaml")
	yaml := "mounts:\n  - name: docker-sock\n    disabled: true\n"
	if err := viper.ReadConfig(bytes.NewBufferString(yaml)); err != nil {
		t.Fatalf("viper.ReadConfig: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if findMount(cfg.Mounts, "docker-sock") != nil {
		t.Error("docker-sock should be removed after disabled patch")
	}
	if len(cfg.Mounts) != len(DefaultMounts())-1 {
		t.Errorf("expected %d mounts after disable, got %d", len(DefaultMounts())-1, len(cfg.Mounts))
	}
}

// TestLoadMountsRootRetargetsAllDefaults: setting mounts_root rewrites every
// default mount whose Source lives under ~/.toolbox/ to live under the new
// root, leaving Targets, ReadOnly flags, and SymlinkFrom values untouched.
func TestLoadMountsRootRetargetsAllDefaults(t *testing.T) {
	viper.Reset()
	setToolsDefaults()

	viper.SetConfigType("yaml")
	if err := viper.ReadConfig(bytes.NewBufferString("mounts_root: /custom/root\n")); err != nil {
		t.Fatalf("viper.ReadConfig: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	for _, d := range DefaultMounts() {
		got := findMount(cfg.Mounts, d.Name)
		if got == nil {
			t.Errorf("mount %q missing after mounts_root override", d.Name)
			continue
		}
		if !strings.HasPrefix(d.Source, "~/.toolbox/") {
			// Sources outside ~/.toolbox/ (e.g. /var/run/docker.sock) must
			// stay verbatim — the global root only retargets toolbox-managed
			// state dirs, not host references.
			if got.Source != d.Source {
				t.Errorf("mount %q Source drifted: got %q, want %q", d.Name, got.Source, d.Source)
			}
			continue
		}
		want := "/custom/root/" + strings.TrimPrefix(d.Source, "~/.toolbox/")
		if got.Source != want {
			t.Errorf("mount %q Source = %q, want %q", d.Name, got.Source, want)
		}
		if got.Target != d.Target {
			t.Errorf("mount %q Target drifted: got %q, want %q", d.Name, got.Target, d.Target)
		}
		if got.ReadOnly != d.ReadOnly {
			t.Errorf("mount %q ReadOnly drifted", d.Name)
		}
		if got.SymlinkFrom != d.SymlinkFrom {
			t.Errorf("mount %q SymlinkFrom drifted: got %q, want %q", d.Name, got.SymlinkFrom, d.SymlinkFrom)
		}
	}
}

// TestLoadMountsRootHomeRelative: mounts_root accepts ~/-prefixed paths
// without trying to expand them at config-load time. Expansion happens
// later in ResolveMounts.
func TestLoadMountsRootHomeRelative(t *testing.T) {
	viper.Reset()
	setToolsDefaults()

	viper.SetConfigType("yaml")
	if err := viper.ReadConfig(bytes.NewBufferString("mounts_root: ~/work/toolbox-state\n")); err != nil {
		t.Fatalf("viper.ReadConfig: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	gws := findMount(cfg.Mounts, "gws")
	if gws == nil {
		t.Fatal("gws mount missing")
	}
	if gws.Source != "~/work/toolbox-state/gws" {
		t.Errorf("gws Source = %q, want %q", gws.Source, "~/work/toolbox-state/gws")
	}
}

// TestLoadMountsRootCombinedWithPatch: a per-name patch wins over the
// global root rewrite, so a user can move every default to /custom/root
// AND keep gws somewhere else entirely.
func TestLoadMountsRootCombinedWithPatch(t *testing.T) {
	viper.Reset()
	setToolsDefaults()

	viper.SetConfigType("yaml")
	yaml := "" +
		"mounts_root: /custom/root\n" +
		"mounts:\n" +
		"  - name: gws\n" +
		"    source: /elsewhere/gws\n"
	if err := viper.ReadConfig(bytes.NewBufferString(yaml)); err != nil {
		t.Fatalf("viper.ReadConfig: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	gws := findMount(cfg.Mounts, "gws")
	if gws == nil {
		t.Fatal("gws mount missing")
	}
	if gws.Source != "/elsewhere/gws" {
		t.Errorf("gws Source = %q, want per-mount patch %q", gws.Source, "/elsewhere/gws")
	}

	// Other defaults still follow the root rewrite.
	claude := findMount(cfg.Mounts, "claude")
	if claude == nil || claude.Source != "/custom/root/.claude" {
		t.Errorf("claude Source should follow mounts_root, got %+v", claude)
	}
}

// TestLoadMountsRootBareTildeRejected: a bare "~" would rewrite
// ~/.toolbox/<x> to ~/<x> — dropping the isolation namespace and writing
// toolbox state directly under the host home (the exact leak DefaultMounts
// guards against). Refuse it loudly with a message that points to the fix.
func TestLoadMountsRootBareTildeRejected(t *testing.T) {
	viper.Reset()
	setToolsDefaults()

	viper.SetConfigType("yaml")
	if err := viper.ReadConfig(bytes.NewBufferString("mounts_root: \"~\"\n")); err != nil {
		t.Fatalf("viper.ReadConfig: %v", err)
	}

	_, err := Load()
	if err == nil {
		t.Fatal("Load() should reject bare ~ as mounts_root")
	}
	if !strings.Contains(err.Error(), "mounts_root") || !strings.Contains(err.Error(), "isolation") {
		t.Errorf("error should explain the isolation footgun, got: %v", err)
	}
}

// TestLoadMountsRootWithAnonymousAppend: an anonymous append (e.g. a
// project-local data dir) is unaffected by mounts_root — only default
// mounts under ~/.toolbox/ are retargeted.
func TestLoadMountsRootWithAnonymousAppend(t *testing.T) {
	viper.Reset()
	setToolsDefaults()

	viper.SetConfigType("yaml")
	yaml := "" +
		"mounts_root: /custom/root\n" +
		"mounts:\n" +
		"  - name: project-data\n" +
		"    source: /opt/project/data\n" +
		"    target: /workspace/data\n"
	if err := viper.ReadConfig(bytes.NewBufferString(yaml)); err != nil {
		t.Fatalf("viper.ReadConfig: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	added := findMount(cfg.Mounts, "project-data")
	if added == nil {
		t.Fatal("project-data mount missing")
	}
	if added.Source != "/opt/project/data" {
		t.Errorf("anonymous append source must not be touched by mounts_root: got %q, want %q",
			added.Source, "/opt/project/data")
	}
}

// TestApplyMountsRootDoesNotMutateBase: callers reuse DefaultMounts()
// across Load() invocations, so ApplyMountsRoot must return a copy and
// leave the input slice untouched. Mirrors TestMergeMountsDoesNotMutateBase.
func TestApplyMountsRootDoesNotMutateBase(t *testing.T) {
	base := DefaultMounts()
	originalSource := findMount(base, "claude").Source

	out := ApplyMountsRoot(base, "/custom/root")

	if got := findMount(base, "claude").Source; got != originalSource {
		t.Errorf("base mutated: claude.Source = %q, want %q", got, originalSource)
	}
	if got := findMount(out, "claude").Source; got != "/custom/root/.claude" {
		t.Errorf("returned slice not retargeted: claude.Source = %q, want %q",
			got, "/custom/root/.claude")
	}
}

// TestLoadMountsRootRelativeRejected: a relative mounts_root is a likely
// mistake — refuse it loudly instead of silently resolving against CWD.
func TestLoadMountsRootRelativeRejected(t *testing.T) {
	viper.Reset()
	setToolsDefaults()

	viper.SetConfigType("yaml")
	if err := viper.ReadConfig(bytes.NewBufferString("mounts_root: ./relative\n")); err != nil {
		t.Fatalf("viper.ReadConfig: %v", err)
	}

	if _, err := Load(); err == nil {
		t.Fatal("Load() should reject relative mounts_root")
	} else if !strings.Contains(err.Error(), "mounts_root") {
		t.Errorf("error should mention mounts_root, got: %v", err)
	}
}

// TestLoadMountsRootEmptyIsNoop: an empty mounts_root keeps every default
// Source verbatim — guards against accidental rewrites when the field is
// declared but left blank.
func TestLoadMountsRootEmptyIsNoop(t *testing.T) {
	viper.Reset()
	setToolsDefaults()

	viper.SetConfigType("yaml")
	if err := viper.ReadConfig(bytes.NewBufferString("mounts_root: \"\"\n")); err != nil {
		t.Fatalf("viper.ReadConfig: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	for _, d := range DefaultMounts() {
		got := findMount(cfg.Mounts, d.Name)
		if got == nil || got.Source != d.Source {
			t.Errorf("mount %q Source drifted with empty mounts_root: got %+v, want %q", d.Name, got, d.Source)
		}
	}
}

// TestLoadMountsRootTrailingSlash: a trailing slash on mounts_root must
// not produce double slashes in rewritten Sources.
func TestLoadMountsRootTrailingSlash(t *testing.T) {
	viper.Reset()
	setToolsDefaults()

	viper.SetConfigType("yaml")
	if err := viper.ReadConfig(bytes.NewBufferString("mounts_root: /custom/root/\n")); err != nil {
		t.Fatalf("viper.ReadConfig: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	gws := findMount(cfg.Mounts, "gws")
	if gws == nil || gws.Source != "/custom/root/gws" {
		t.Errorf("gws Source = %v, want /custom/root/gws (no double slash)", gws)
	}
}

func findMount(mounts []Mount, name string) *Mount {
	for i := range mounts {
		if mounts[i].Name == name {
			return &mounts[i]
		}
	}
	return nil
}

// TestMergeMountsPatchFlipsReadOnly: a name-only patch can flip ReadOnly
// false→true on a default that defaults to false.
func TestMergeMountsPatchFlipsReadOnly(t *testing.T) {
	base := []Mount{
		{Name: "data", Source: "/a", Target: "/b", ReadOnly: false, CreateIfMissing: true},
	}
	user := []Mount{{Name: "data", ReadOnly: true}}

	merged, err := MergeMounts(base, user)
	if err != nil {
		t.Fatalf("MergeMounts: %v", err)
	}
	if !merged[0].ReadOnly {
		t.Error("patch should flip ReadOnly to true")
	}
	if merged[0].Source != "/a" || merged[0].Target != "/b" {
		t.Error("patch must not touch unrelated fields")
	}
	if !merged[0].CreateIfMissing {
		t.Error("CreateIfMissing should remain true (default)")
	}
}

// TestMergeMountsPatchSetsSymlinkFrom: a patch can set SymlinkFrom on a
// default that did not have one.
func TestMergeMountsPatchSetsSymlinkFrom(t *testing.T) {
	base := []Mount{{Name: "x", Source: "/a", Target: "/b"}}
	user := []Mount{{Name: "x", SymlinkFrom: "~/real"}}

	merged, err := MergeMounts(base, user)
	if err != nil {
		t.Fatalf("MergeMounts: %v", err)
	}
	if merged[0].SymlinkFrom != "~/real" {
		t.Errorf("SymlinkFrom = %q, want %q", merged[0].SymlinkFrom, "~/real")
	}
}

// TestMergeMountsAnonymousAppend: a user entry without Name is appended
// (legacy/anonymous mount), but rejected if Source is empty.
func TestMergeMountsAnonymousAppend(t *testing.T) {
	base := []Mount{{Name: "x", Source: "/a", Target: "/b"}}
	user := []Mount{{Source: "/extra", Target: "/extra-c"}}

	merged, err := MergeMounts(base, user)
	if err != nil {
		t.Fatalf("MergeMounts: %v", err)
	}
	if len(merged) != 2 || merged[1].Source != "/extra" {
		t.Errorf("anonymous append failed: %+v", merged)
	}
}

// TestMergeMountsAnonymousEmptySourceErrors: an anonymous entry with no
// source is a likely typo (would silently bind CWD), reject it.
func TestMergeMountsAnonymousEmptySourceErrors(t *testing.T) {
	base := []Mount{{Name: "x", Source: "/a", Target: "/b"}}
	user := []Mount{{Target: "/extra"}}

	if _, err := MergeMounts(base, user); err == nil {
		t.Fatal("MergeMounts should reject anonymous mount with empty source")
	}
}

// TestMergeMountsReplaceEmptySourceErrors: a replace branch (name + target)
// without a source is a likely typo — reject loud instead of clobbering
// the default's Source with "".
func TestMergeMountsReplaceEmptySourceErrors(t *testing.T) {
	base := []Mount{{Name: "gws", Source: "~/.toolbox/gws", Target: "/home/toolbox/.config/gws"}}
	user := []Mount{{Name: "gws", Target: "/home/toolbox/.config/gws"}}

	if _, err := MergeMounts(base, user); err == nil {
		t.Fatal("MergeMounts should reject replace with empty source")
	}
}

// TestMergeMountsReplaceUnknownNameAppends: a user entry with a fresh Name
// and a Target is treated as a new mount, not an error.
func TestMergeMountsReplaceUnknownNameAppends(t *testing.T) {
	base := []Mount{{Name: "x", Source: "/a", Target: "/b"}}
	user := []Mount{{Name: "fresh", Source: "/c", Target: "/d"}}

	merged, err := MergeMounts(base, user)
	if err != nil {
		t.Fatalf("MergeMounts: %v", err)
	}
	if len(merged) != 2 || merged[1].Name != "fresh" {
		t.Errorf("expected appended fresh mount, got %+v", merged)
	}
}

// TestMergeMountsMultipleUnknownPatchesSorted: when several patches refer
// to unknown names, the error must list them sorted so the message is
// stable across map-iteration order.
func TestMergeMountsMultipleUnknownPatchesSorted(t *testing.T) {
	base := []Mount{{Name: "x", Source: "/a", Target: "/b"}}
	user := []Mount{
		{Name: "zzz", Source: "/x"},
		{Name: "aaa", Source: "/y"},
		{Name: "mmm", Source: "/z"},
	}

	_, err := MergeMounts(base, user)
	if err == nil {
		t.Fatal("expected error for unknown patches")
	}
	if !strings.Contains(err.Error(), "aaa, mmm, zzz") {
		t.Errorf("unknown names should be sorted in message, got: %v", err)
	}
}

// TestMergeMountsDisableAndPatchSameName: a user list that combines a
// disable patch and a regular patch on the same Name results in the mount
// being removed (Disabled wins at the final filter, regardless of order).
// Locks the contract for "I want to opt out of a default but also document
// what its source would have been if re-enabled".
func TestMergeMountsDisableAndPatchSameName(t *testing.T) {
	base := []Mount{{Name: "x", Source: "/a", Target: "/b"}}

	disableFirst := []Mount{
		{Name: "x", Disabled: true},
		{Name: "x", Source: "/changed"},
	}
	merged, err := MergeMounts(base, disableFirst)
	if err != nil {
		t.Fatalf("MergeMounts(disable-first): %v", err)
	}
	if len(merged) != 0 {
		t.Errorf("disable-first: expected mount to be removed, got %+v", merged)
	}

	patchFirst := []Mount{
		{Name: "x", Source: "/changed"},
		{Name: "x", Disabled: true},
	}
	merged, err = MergeMounts(base, patchFirst)
	if err != nil {
		t.Fatalf("MergeMounts(patch-first): %v", err)
	}
	if len(merged) != 0 {
		t.Errorf("patch-first: expected mount to be removed, got %+v", merged)
	}
}

// TestMergeMountsUnknownPatchPlusLaterAppend: a typo'd patch on an unknown
// Name fails Load() even when a later user entry would have made the same
// Name valid via the append branch. The patch typo is the loud failure
// signal — silently shadowing it with a later append would mask config
// mistakes.
func TestMergeMountsUnknownPatchPlusLaterAppend(t *testing.T) {
	base := []Mount{{Name: "x", Source: "/a", Target: "/b"}}
	user := []Mount{
		{Name: "fresh", Source: "/c"},               // patch on unknown
		{Name: "fresh", Source: "/c", Target: "/d"}, // would be a valid append on its own
	}

	if _, err := MergeMounts(base, user); err == nil {
		t.Fatal("expected error: patch on unknown name must fail even when followed by a valid append")
	} else if !strings.Contains(err.Error(), "fresh") {
		t.Errorf("error should mention the unknown name 'fresh', got: %v", err)
	}
}

// TestMergeMountsNoOpPatch: a patch that only carries Name (no Source,
// SymlinkFrom, ReadOnly, CreateIfMissing, Disabled) is a documented no-op —
// every field stays at the base value. Locks the contract so a refactor
// cannot accidentally start clobbering defaults to zero values.
func TestMergeMountsNoOpPatch(t *testing.T) {
	base := []Mount{{
		Name: "x", Source: "/a", Target: "/b",
		ReadOnly: true, CreateIfMissing: true, SymlinkFrom: "/host",
	}}
	user := []Mount{{Name: "x"}}

	merged, err := MergeMounts(base, user)
	if err != nil {
		t.Fatalf("MergeMounts: %v", err)
	}
	if len(merged) != 1 {
		t.Fatalf("expected 1 mount, got %d", len(merged))
	}
	got := merged[0]
	if got.Source != "/a" || got.Target != "/b" || !got.ReadOnly || !got.CreateIfMissing || got.SymlinkFrom != "/host" {
		t.Errorf("no-op patch must preserve every field, got %+v", got)
	}
}

// TestMergeMountsDoesNotMutateBase: MergeMounts must not mutate the slice
// passed as base, since callers reuse DefaultMounts() across calls.
func TestMergeMountsDoesNotMutateBase(t *testing.T) {
	base := DefaultMounts()
	originalSource := findMount(base, "gws").Source

	if _, err := MergeMounts(base, []Mount{{Name: "gws", Source: "/changed"}}); err != nil {
		t.Fatalf("MergeMounts: %v", err)
	}

	if got := findMount(base, "gws").Source; got != originalSource {
		t.Errorf("base mutated: gws.Source = %q, want %q", got, originalSource)
	}
}
