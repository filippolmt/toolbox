package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/mountplan"
)

// resetMountsFlags restores the mounts writer flag vars after a test that
// sets them directly.
func resetMountsFlags(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		mountsListDefaultsOnly = false
		mountsAddSource, mountsAddTarget, mountsAddReadonly, mountsAddWhere = "", "", false, "global"
		mountsDisableWhere = "global"
		mountsRemoveWhere = "global"
		mountsRootWhere = "global"
		mountsAddDryRun, mountsDisableDryRun, mountsRemoveDryRun, mountsRootDryRun = false, false, false, false
	})
}

func TestMountsAddNodeShape(t *testing.T) {
	resetMountsFlags(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	mountsAddSource = "~/scratch"
	mountsAddTarget = "/scratch"
	mountsAddReadonly = true
	out := &bytes.Buffer{}
	mountsAddCmd.SetOut(out)

	if err := runMountsAdd(mountsAddCmd, []string{"scratch"}); err != nil {
		t.Fatalf("runMountsAdd: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(home, ".toolbox.yaml"))
	if err != nil {
		t.Fatalf("read global: %v", err)
	}
	got := string(body)
	if !strings.Contains(got, "mounts:\n  - name: scratch\n    source: ~/scratch\n    target: /scratch\n    readonly: true") {
		t.Errorf("unexpected mount node shape:\n%s", got)
	}
	if !strings.Contains(out.String(), ": created") {
		t.Errorf("report must say created, got: %s", out.String())
	}
}

func TestMountsAddRejectsEmptySource(t *testing.T) {
	resetMountsFlags(t)
	t.Setenv("HOME", t.TempDir())

	mountsAddSource = "  "
	mountsAddTarget = "/scratch"

	err := runMountsAdd(mountsAddCmd, []string{"scratch"})
	if _, ok := errors.AsType[*usageError](err); err == nil || !ok {
		t.Fatalf("empty --source must be a usage error, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(os.Getenv("HOME"), ".toolbox.yaml")); !os.IsNotExist(statErr) {
		t.Error("no file must be written on validation failure")
	}
}

func TestMountsDisableDefaultName(t *testing.T) {
	resetMountsFlags(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	withCfg(t, &config.Config{})

	name := mountplan.Defaults()[0].Name
	out := &bytes.Buffer{}
	mountsDisableCmd.SetOut(out)

	if err := runMountsDisable(mountsDisableCmd, []string{name}); err != nil {
		t.Fatalf("runMountsDisable: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(home, ".toolbox.yaml"))
	if err != nil {
		t.Fatalf("read global: %v", err)
	}
	if !strings.Contains(string(body), "- name: "+name+"\n    disabled: true") {
		t.Errorf("expected disable patch for %s:\n%s", name, body)
	}
}

func TestMountsDisableUnknownNameSuggests(t *testing.T) {
	resetMountsFlags(t)
	t.Setenv("HOME", t.TempDir())
	withCfg(t, &config.Config{})

	name := mountplan.Defaults()[0].Name
	typo := name[:len(name)-1]

	err := runMountsDisable(mountsDisableCmd, []string{typo})
	if _, ok := errors.AsType[*usageError](err); err == nil || !ok {
		t.Fatalf("unknown disable name must be a usage error, got %v", err)
	}
	if !strings.Contains(err.Error(), `did you mean "`+name+`"?`) {
		t.Errorf("expected suggestion for %q, got: %v", name, err)
	}
}

func TestMountsRemoveUserEntry(t *testing.T) {
	resetMountsFlags(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, ".toolbox.yaml"),
		[]byte("mounts:\n  - name: scratch\n    source: ~/s\n    target: /s\n"), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	out := &bytes.Buffer{}
	mountsRemoveCmd.SetOut(out)
	if err := runMountsRemove(mountsRemoveCmd, []string{"scratch"}); err != nil {
		t.Fatalf("runMountsRemove: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(home, ".toolbox.yaml"))
	if err != nil {
		t.Fatalf("read global: %v", err)
	}
	if strings.Contains(string(body), "scratch") {
		t.Errorf("entry must be removed:\n%s", body)
	}
}

func TestMountsRemoveDefaultOnlyNameFails(t *testing.T) {
	resetMountsFlags(t)
	t.Setenv("HOME", t.TempDir())

	name := mountplan.Defaults()[0].Name
	err := runMountsRemove(mountsRemoveCmd, []string{name})
	if err == nil {
		t.Fatal("removing a default-only name must fail")
	}
	if !strings.Contains(err.Error(), "disable") {
		t.Errorf("error must point at disable, got: %v", err)
	}
	if _, ok := errors.AsType[*usageError](err); ok {
		t.Errorf("default-only removal is a runtime error (exit 1), not usage, got %T", err)
	}
}

func TestMountsRemoveUnknownNameSuggests(t *testing.T) {
	resetMountsFlags(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, ".toolbox.yaml"),
		[]byte("mounts:\n  - name: scratch\n    source: ~/s\n    target: /s\n"), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	err := runMountsRemove(mountsRemoveCmd, []string{"scrach"})
	if _, ok := errors.AsType[*usageError](err); err == nil || !ok {
		t.Fatalf("unknown remove name must be a usage error, got %v", err)
	}
	if !strings.Contains(err.Error(), `did you mean "scratch"?`) {
		t.Errorf("expected suggestion, got: %v", err)
	}
}

func TestMountsRootValidatesBeforeWrite(t *testing.T) {
	resetMountsFlags(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	err := runMountsRoot(mountsRootCmd, []string{"relative/path"})
	if _, ok := errors.AsType[*usageError](err); err == nil || !ok {
		t.Fatalf("invalid mounts_root must be a usage error, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(home, ".toolbox.yaml")); !os.IsNotExist(statErr) {
		t.Error("no file must be written when validation fails")
	}
}

func TestMountsRootWritesValidRoot(t *testing.T) {
	resetMountsFlags(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	out := &bytes.Buffer{}
	mountsRootCmd.SetOut(out)
	if err := runMountsRoot(mountsRootCmd, []string{"~/encrypted/toolbox"}); err != nil {
		t.Fatalf("runMountsRoot: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(home, ".toolbox.yaml"))
	if err != nil {
		t.Fatalf("read global: %v", err)
	}
	if !strings.Contains(string(body), "mounts_root: ~/encrypted/toolbox") {
		t.Errorf("expected mounts_root key:\n%s", body)
	}
}

// TestMountsRootEmptyValueRemovesTheKey pins the reset: an empty path drops
// mounts_root from the file instead of writing `mounts_root: ""`. The two are
// not the same configuration — an explicit empty value is an override that
// means "no override", which still shadows whatever the layer below sets, and
// leaves a key behind for the next reader to wonder about. Removal is the
// clean reset, and config.ValidateMountsRoot accepts "" precisely so this
// surface can offer it.
//
// The behaviour comes from configedit.Scalar's empty-value-removes rule, one
// mutation shared by every top-level scalar; this case is what holds the
// command to it. Sibling keys are untouched, so a reset is a reset and not a
// rewrite.
func TestMountsRootEmptyValueRemovesTheKey(t *testing.T) {
	resetMountsFlags(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgPath := filepath.Join(home, ".toolbox.yaml")
	if err := os.WriteFile(cfgPath,
		[]byte("mounts_root: /vault/toolbox\npull: never\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out := &bytes.Buffer{}
	mountsRootCmd.SetOut(out)
	t.Cleanup(func() { mountsRootCmd.SetOut(nil) })

	if err := runMountsRoot(mountsRootCmd, []string{""}); err != nil {
		t.Fatalf("runMountsRoot with an empty path: %v", err)
	}
	body, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read global: %v", err)
	}
	got := string(body)
	if strings.Contains(got, "mounts_root") {
		t.Errorf("an empty path must remove the key, not write it empty:\n%s", got)
	}
	if !strings.Contains(got, "pull: never") {
		t.Errorf("the reset must leave sibling keys alone:\n%s", got)
	}
	if !strings.Contains(out.String(), ": updated") {
		t.Errorf("the reset must be reported as a write, got: %s", out.String())
	}
}

func TestMountsListClassification(t *testing.T) {
	resetMountsFlags(t)
	t.Setenv("HOME", t.TempDir())

	defaults := mountplan.Defaults()
	patchedName := defaults[0].Name
	disabledName := defaults[1].Name
	withCfg(t, &config.Config{Mounts: []config.Mount{
		{Name: patchedName, ReadOnly: true},            // patch form
		{Name: disabledName, Disabled: true},           // disable patch
		{Name: "scratch", Source: "~/s", Target: "/s"}, // user append
	}})

	out := &bytes.Buffer{}
	mountsListCmd.SetOut(out)
	if err := runMountsList(mountsListCmd, nil); err != nil {
		t.Fatalf("runMountsList: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		patchedName + " ", "(patched)",
		"(disabled)",
		"scratch", "(user)",
		"(default)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("list output missing %q:\n%s", want, got)
		}
	}
	// The disabled default appears exactly once, flagged disabled.
	for line := range strings.SplitSeq(got, "\n") {
		if strings.HasPrefix(line, disabledName+" ") && !strings.Contains(line, "(disabled)") {
			t.Errorf("disabled default misclassified: %s", line)
		}
	}
}

func TestMountsListDefaultsOnly(t *testing.T) {
	resetMountsFlags(t)
	withCfg(t, &config.Config{Mounts: []config.Mount{{Name: "scratch", Source: "~/s", Target: "/s"}}})

	mountsListDefaultsOnly = true
	out := &bytes.Buffer{}
	mountsListCmd.SetOut(out)
	if err := runMountsList(mountsListCmd, nil); err != nil {
		t.Fatalf("runMountsList: %v", err)
	}
	got := out.String()
	if strings.Contains(got, "scratch") {
		t.Errorf("--defaults-only must omit user mounts:\n%s", got)
	}
	if !strings.Contains(got, mountplan.Defaults()[0].Name) {
		t.Errorf("--defaults-only must list the canonical defaults:\n%s", got)
	}
}

// TestMountsListDegradesOnAnUnresolvableHome pins hostBestEffort's contract at
// the surface it exists for: the listing is read-only, so a $HOME the process
// cannot resolve must still print the mount set the config declares. Refusing
// would hide exactly what a reader with a broken home came to look at, and the
// pre-hostBestEffort code degraded here too (mountplan.Merge discarded the same
// os.UserHomeDir error).
func TestMountsListDegradesOnAnUnresolvableHome(t *testing.T) {
	resetMountsFlags(t)
	t.Setenv("HOME", "")
	withCfg(t, &config.Config{Mounts: []config.Mount{
		{Name: "scratch", Source: "/abs/s", Target: "/s"},
	}})

	out := &bytes.Buffer{}
	mountsListCmd.SetOut(out)
	if err := runMountsList(mountsListCmd, nil); err != nil {
		t.Fatalf("runMountsList must not refuse an unresolvable home: %v", err)
	}
	if !strings.Contains(out.String(), "scratch") {
		t.Errorf("listing lost the user mount: %q", out.String())
	}
}
