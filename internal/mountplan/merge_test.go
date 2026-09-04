package mountplan

import (
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/config"
)

// TestMergeRetargetsSource is the main contract for the unified mounts:
// knob: a name-only patch must change only the Source of the named mount
// and leave every other mount untouched.
func TestMergeRetargetsSource(t *testing.T) {
	cfg := config.Config{
		Mounts: []config.Mount{
			{Name: "gws", Source: "/custom/gws"},
		},
	}

	merged, err := Merge(testHost(t), &cfg, nil)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	gws := findMount(merged, "gws")
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

	for _, d := range Defaults() {
		if d.Name == "gws" {
			continue
		}
		got := findMount(merged, d.Name)
		if got == nil {
			t.Errorf("mount %q missing after unrelated patch", d.Name)
			continue
		}
		if got.Source != d.Source {
			t.Errorf("mount %q Source drifted: got %q, want %q", d.Name, got.Source, d.Source)
		}
	}
}

// TestMergeUnknownNameErrors guards the typo-detection contract:
// a name-only patch referencing a non-existent mount must fail Merge().
func TestMergeUnknownNameErrors(t *testing.T) {
	cfg := config.Config{
		Mounts: []config.Mount{{Name: "nonexistent", Source: "/tmp/x"}},
	}
	_, err := Merge(testHost(t), &cfg, nil)
	if err == nil {
		t.Fatal("Merge should fail when a patch references an unknown name")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("error should mention the unknown name, got: %v", err)
	}
}

// TestMergeReplaceByName: a user entry with the same Name as a default
// AND a Target must replace the default entry wholesale.
func TestMergeReplaceByName(t *testing.T) {
	cfg := config.Config{
		Mounts: []config.Mount{
			{Name: "gws", Source: "/custom/gws", Target: "/opt/gws", ReadOnly: true},
		},
	}

	merged, err := Merge(testHost(t), &cfg, nil)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	gws := findMount(merged, "gws")
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

	if len(merged) != len(Defaults()) {
		t.Errorf("expected %d mounts after replace, got %d", len(Defaults()), len(merged))
	}
}

// TestMergeAppendNewName: a user entry whose Name is not in the defaults
// (or anonymous) is appended to the resolved set.
func TestMergeAppendNewName(t *testing.T) {
	cfg := config.Config{
		Mounts: []config.Mount{
			{Name: "project-data", Source: "/opt/data", Target: "/data"},
		},
	}

	merged, err := Merge(testHost(t), &cfg, nil)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	if len(merged) != len(Defaults())+1 {
		t.Errorf("expected %d mounts, got %d", len(Defaults())+1, len(merged))
	}
	added := findMount(merged, "project-data")
	if added == nil {
		t.Fatal("project-data mount missing after append")
	}
	if added.Target != "/data" {
		t.Errorf("project-data Target = %q, want %q", added.Target, "/data")
	}
}

// TestMergeDisableDefault: a patch with disabled: true removes the
// default from the resolved set.
func TestMergeDisableDefault(t *testing.T) {
	cfg := config.Config{
		Mounts: []config.Mount{{Name: "docker-sock", Disabled: true}},
	}

	merged, err := Merge(testHost(t), &cfg, nil)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	if findMount(merged, "docker-sock") != nil {
		t.Error("docker-sock should be removed after disabled patch")
	}
	if len(merged) != len(Defaults())-1 {
		t.Errorf("expected %d mounts after disable, got %d", len(Defaults())-1, len(merged))
	}
}

// TestMergeMountsRootRetargetsAllDefaults: setting MountsRoot rewrites every
// default mount whose Source lives under ~/.toolbox/ to live under the new
// root, leaving Targets, ReadOnly flags, and SymlinkFrom values untouched.
func TestMergeMountsRootRetargetsAllDefaults(t *testing.T) {
	cfg := config.Config{MountsRoot: "/custom/root"}

	merged, err := Merge(testHost(t), &cfg, nil)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	for _, d := range Defaults() {
		got := findMount(merged, d.Name)
		if got == nil {
			t.Errorf("mount %q missing after mounts_root override", d.Name)
			continue
		}
		if !strings.HasPrefix(d.Source, "~/.toolbox/") {
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

// TestMergeMountsRootHomeRelative: MountsRoot accepts ~/-prefixed paths
// without trying to expand them at merge time. Expansion happens later in
// resolveAll.
func TestMergeMountsRootHomeRelative(t *testing.T) {
	cfg := config.Config{MountsRoot: "~/work/toolbox-state"}

	merged, err := Merge(testHost(t), &cfg, nil)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	gws := findMount(merged, "gws")
	if gws == nil {
		t.Fatal("gws mount missing")
	}
	if gws.Source != "~/work/toolbox-state/gws" {
		t.Errorf("gws Source = %q, want %q", gws.Source, "~/work/toolbox-state/gws")
	}
}

// TestMergeMountsRootCombinedWithPatch: a per-name patch wins over the
// global root rewrite.
func TestMergeMountsRootCombinedWithPatch(t *testing.T) {
	cfg := config.Config{
		MountsRoot: "/custom/root",
		Mounts:     []config.Mount{{Name: "gws", Source: "/elsewhere/gws"}},
	}

	merged, err := Merge(testHost(t), &cfg, nil)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	gws := findMount(merged, "gws")
	if gws == nil {
		t.Fatal("gws mount missing")
	}
	if gws.Source != "/elsewhere/gws" {
		t.Errorf("gws Source = %q, want per-mount patch %q", gws.Source, "/elsewhere/gws")
	}

	claude := findMount(merged, "claude")
	if claude == nil || claude.Source != "/custom/root/.claude" {
		t.Errorf("claude Source should follow mounts_root, got %+v", claude)
	}
}

// TestMergeMountsRootWithAnonymousAppend: an anonymous append (e.g. a
// project-local data dir) is unaffected by mounts_root — only default
// mounts under ~/.toolbox/ are retargeted.
func TestMergeMountsRootWithAnonymousAppend(t *testing.T) {
	cfg := config.Config{
		MountsRoot: "/custom/root",
		Mounts: []config.Mount{
			{Name: "project-data", Source: "/opt/project/data", Target: "/workspace/data"},
		},
	}

	merged, err := Merge(testHost(t), &cfg, nil)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	added := findMount(merged, "project-data")
	if added == nil {
		t.Fatal("project-data mount missing")
	}
	if added.Source != "/opt/project/data" {
		t.Errorf("anonymous append source must not be touched by mounts_root: got %q, want %q",
			added.Source, "/opt/project/data")
	}
}

// TestMergeMountsRootEmptyIsNoop: an empty MountsRoot keeps every default
// Source verbatim — guards against accidental rewrites when the field is
// declared but left blank.
func TestMergeMountsRootEmptyIsNoop(t *testing.T) {
	cfg := config.Config{MountsRoot: ""}

	merged, err := Merge(testHost(t), &cfg, nil)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	for _, d := range Defaults() {
		got := findMount(merged, d.Name)
		if got == nil || got.Source != d.Source {
			t.Errorf("mount %q Source drifted with empty mounts_root: got %+v, want %q", d.Name, got, d.Source)
		}
	}
}

// TestMergeMountsRootTrailingSlash: a trailing slash on MountsRoot must
// not produce double slashes in rewritten Sources.
func TestMergeMountsRootTrailingSlash(t *testing.T) {
	cfg := config.Config{MountsRoot: "/custom/root/"}

	merged, err := Merge(testHost(t), &cfg, nil)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	gws := findMount(merged, "gws")
	if gws == nil || gws.Source != "/custom/root/gws" {
		t.Errorf("gws Source = %v, want /custom/root/gws (no double slash)", gws)
	}
}

// TestApplyMountsRootDoesNotMutateBase: callers reuse defaults() across
// invocations, so applyMountsRoot must return a copy and leave the input
// slice untouched.
func TestApplyMountsRootDoesNotMutateBase(t *testing.T) {
	base := defaults()
	originalSource := findMount(base, "claude").Source

	out := applyMountsRoot(base, "/custom/root", nil)

	if got := findMount(base, "claude").Source; got != originalSource {
		t.Errorf("base mutated: claude.Source = %q, want %q", got, originalSource)
	}
	if got := findMount(out, "claude").Source; got != "/custom/root/.claude" {
		t.Errorf("returned slice not retargeted: claude.Source = %q, want %q",
			got, "/custom/root/.claude")
	}
}

// TestMergePatchFlipsReadOnly: a name-only patch can flip ReadOnly
// false→true on a default that defaults to false.
func TestMergePatchFlipsReadOnly(t *testing.T) {
	base := []config.Mount{
		{Name: "data", Source: "/a", Target: "/b", ReadOnly: false, CreateIfMissing: true},
	}
	user := []config.Mount{{Name: "data", ReadOnly: true}}

	merged, err := mergeMounts(base, user)
	if err != nil {
		t.Fatalf("mergeMounts: %v", err)
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

// TestMergePatchSetsSymlinkFrom: a patch can set SymlinkFrom on a default
// that did not have one.
func TestMergePatchSetsSymlinkFrom(t *testing.T) {
	base := []config.Mount{{Name: "x", Source: "/a", Target: "/b"}}
	user := []config.Mount{{Name: "x", SymlinkFrom: "~/real"}}

	merged, err := mergeMounts(base, user)
	if err != nil {
		t.Fatalf("mergeMounts: %v", err)
	}
	if merged[0].SymlinkFrom != "~/real" {
		t.Errorf("SymlinkFrom = %q, want %q", merged[0].SymlinkFrom, "~/real")
	}
}

// TestMergeAnonymousAppend: a user entry without Name is appended
// (legacy/anonymous mount), but rejected if Source is empty.
func TestMergeAnonymousAppend(t *testing.T) {
	base := []config.Mount{{Name: "x", Source: "/a", Target: "/b"}}
	user := []config.Mount{{Source: "/extra", Target: "/extra-c"}}

	merged, err := mergeMounts(base, user)
	if err != nil {
		t.Fatalf("mergeMounts: %v", err)
	}
	if len(merged) != 2 || merged[1].Source != "/extra" {
		t.Errorf("anonymous append failed: %+v", merged)
	}
}

// TestMergeAnonymousEmptySourceErrors: an anonymous entry with no source
// is a likely typo (would silently bind CWD), reject it.
func TestMergeAnonymousEmptySourceErrors(t *testing.T) {
	base := []config.Mount{{Name: "x", Source: "/a", Target: "/b"}}
	user := []config.Mount{{Target: "/extra"}}

	if _, err := mergeMounts(base, user); err == nil {
		t.Fatal("mergeMounts should reject anonymous mount with empty source")
	}
}

// TestMergeReplaceEmptySourceErrors: a replace branch (name + target)
// without a source is a likely typo — reject loud instead of clobbering
// the default's Source with "".
func TestMergeReplaceEmptySourceErrors(t *testing.T) {
	base := []config.Mount{{Name: "gws", Source: "~/.toolbox/gws", Target: "/home/toolbox/.config/gws"}}
	user := []config.Mount{{Name: "gws", Target: "/home/toolbox/.config/gws"}}

	if _, err := mergeMounts(base, user); err == nil {
		t.Fatal("mergeMounts should reject replace with empty source")
	}
}

// TestMergeReplaceUnknownNameAppends: a user entry with a fresh Name
// and a Target is treated as a new mount, not an error.
func TestMergeReplaceUnknownNameAppends(t *testing.T) {
	base := []config.Mount{{Name: "x", Source: "/a", Target: "/b"}}
	user := []config.Mount{{Name: "fresh", Source: "/c", Target: "/d"}}

	merged, err := mergeMounts(base, user)
	if err != nil {
		t.Fatalf("mergeMounts: %v", err)
	}
	if len(merged) != 2 || merged[1].Name != "fresh" {
		t.Errorf("expected appended fresh mount, got %+v", merged)
	}
}

// TestMergeMultipleUnknownPatchesSorted: when several patches refer to
// unknown names, the error must list them sorted so the message is stable
// across map-iteration order.
func TestMergeMultipleUnknownPatchesSorted(t *testing.T) {
	base := []config.Mount{{Name: "x", Source: "/a", Target: "/b"}}
	user := []config.Mount{
		{Name: "zzz", Source: "/x"},
		{Name: "aaa", Source: "/y"},
		{Name: "mmm", Source: "/z"},
	}

	_, err := mergeMounts(base, user)
	if err == nil {
		t.Fatal("expected error for unknown patches")
	}
	if !strings.Contains(err.Error(), "aaa, mmm, zzz") {
		t.Errorf("unknown names should be sorted in message, got: %v", err)
	}
}

// TestMergeDisableAndPatchSameName: a user list that combines a disable
// patch and a regular patch on the same Name results in the mount being
// removed (Disabled wins at the final filter, regardless of order).
func TestMergeDisableAndPatchSameName(t *testing.T) {
	base := []config.Mount{{Name: "x", Source: "/a", Target: "/b"}}

	disableFirst := []config.Mount{
		{Name: "x", Disabled: true},
		{Name: "x", Source: "/changed"},
	}
	merged, err := mergeMounts(base, disableFirst)
	if err != nil {
		t.Fatalf("mergeMounts(disable-first): %v", err)
	}
	if len(merged) != 0 {
		t.Errorf("disable-first: expected mount to be removed, got %+v", merged)
	}

	patchFirst := []config.Mount{
		{Name: "x", Source: "/changed"},
		{Name: "x", Disabled: true},
	}
	merged, err = mergeMounts(base, patchFirst)
	if err != nil {
		t.Fatalf("mergeMounts(patch-first): %v", err)
	}
	if len(merged) != 0 {
		t.Errorf("patch-first: expected mount to be removed, got %+v", merged)
	}
}

// TestMergeUnknownPatchPlusLaterAppend: a typo'd patch on an unknown Name
// fails Merge() even when a later user entry would have made the same Name
// valid via the append branch. The patch typo is the loud failure signal —
// silently shadowing it with a later append would mask config mistakes.
func TestMergeUnknownPatchPlusLaterAppend(t *testing.T) {
	base := []config.Mount{{Name: "x", Source: "/a", Target: "/b"}}
	user := []config.Mount{
		{Name: "fresh", Source: "/c"},               // patch on unknown
		{Name: "fresh", Source: "/c", Target: "/d"}, // would be a valid append on its own
	}

	if _, err := mergeMounts(base, user); err == nil {
		t.Fatal("expected error: patch on unknown name must fail even when followed by a valid append")
	} else if !strings.Contains(err.Error(), "fresh") {
		t.Errorf("error should mention the unknown name 'fresh', got: %v", err)
	}
}

// TestMergeNoOpPatch: a patch that only carries Name (no Source,
// SymlinkFrom, ReadOnly, CreateIfMissing, Disabled) is a documented no-op —
// every field stays at the base value. Locks the contract so a refactor
// cannot accidentally start clobbering defaults to zero values.
func TestMergeNoOpPatch(t *testing.T) {
	base := []config.Mount{{
		Name: "x", Source: "/a", Target: "/b",
		ReadOnly: true, CreateIfMissing: true, SymlinkFrom: "/host",
	}}
	user := []config.Mount{{Name: "x"}}

	merged, err := mergeMounts(base, user)
	if err != nil {
		t.Fatalf("mergeMounts: %v", err)
	}
	if len(merged) != 1 {
		t.Fatalf("expected 1 mount, got %d", len(merged))
	}
	got := merged[0]
	if got.Source != "/a" || got.Target != "/b" || !got.ReadOnly || !got.CreateIfMissing || got.SymlinkFrom != "/host" {
		t.Errorf("no-op patch must preserve every field, got %+v", got)
	}
}

// TestMergeDoesNotMutateBase: mergeMounts must not mutate the slice passed
// as base, since callers reuse defaults() across calls.
func TestMergeDoesNotMutateBase(t *testing.T) {
	base := defaults()
	originalSource := findMount(base, "gws").Source

	if _, err := mergeMounts(base, []config.Mount{{Name: "gws", Source: "/changed"}}); err != nil {
		t.Fatalf("mergeMounts: %v", err)
	}

	if got := findMount(base, "gws").Source; got != originalSource {
		t.Errorf("base mutated: gws.Source = %q, want %q", got, originalSource)
	}
}
