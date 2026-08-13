package configedit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/config"
)

// tmpConfigPath returns an isolated project config path for a writer test.
// HOME is redirected to a separate temp dir so ApplyChecked's validation sees no
// global layer under the test's feet, and the returned path's directory is the
// cwd every writer call must be given (see cwdOf).
func tmpConfigPath(t *testing.T) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	return filepath.Join(t.TempDir(), ".toolbox.yaml")
}

// cwdOf is the layer-resolution directory for a temp config file: every writer
// test keeps its file at <tmpdir>/.toolbox.yaml, so <tmpdir> is the cwd whose
// walk-up finds exactly that file as the project layer.
func cwdOf(path string) string { return filepath.Dir(path) }

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func TestApplyCheckedHeaderOnCreate(t *testing.T) {
	path := tmpConfigPath(t)

	changed, err := SetShell(path, cwdOf(path), "infra", "/tmp/infra", nil)
	if err != nil {
		t.Fatalf("SetShell: %v", err)
	}
	if !changed {
		t.Error("first write must report changed")
	}
	got := readFile(t, path)
	if !strings.HasPrefix(got, "# .toolbox.yaml — toolbox configuration.") {
		t.Errorf("created file must start with the docs header, got:\n%s", got)
	}
	if !strings.Contains(got, "toolbox config example") {
		t.Errorf("header must point at the annotated template, got:\n%s", got)
	}
}

func TestSetScalarsWritesAllKeysAndIsIdempotent(t *testing.T) {
	path := tmpConfigPath(t)
	edits := []ScalarEdit{
		{"image", "ghcr.io/x/y:1"},
		{"registry_mirror", "harbor.corp.io/ghcr-proxy"},
		{"pull", "never"},
	}

	changed, err := SetScalars(path, cwdOf(path), edits)
	if err != nil {
		t.Fatalf("SetScalars: %v", err)
	}
	if !changed {
		t.Error("first write must report changed")
	}
	got := readFile(t, path)
	for _, want := range []string{"image: ghcr.io/x/y:1", "registry_mirror: harbor.corp.io/ghcr-proxy", "pull: never"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}

	// Idempotent: an identical re-write reports unchanged.
	changed, err = SetScalars(path, cwdOf(path), edits)
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if changed {
		t.Error("identical rewrite must report unchanged")
	}
}

func TestSetScalarsEmptyValueRemovesKey(t *testing.T) {
	path := tmpConfigPath(t)
	if _, err := SetScalars(path, cwdOf(path), []ScalarEdit{{"image", "ghcr.io/x/y:1"}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	changed, err := SetScalars(path, cwdOf(path), []ScalarEdit{{"image", ""}})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if !changed {
		t.Error("removing an existing key must report changed")
	}
	if got := readFile(t, path); strings.Contains(got, "image:") {
		t.Errorf("empty value must remove the key, got:\n%s", got)
	}
}

func TestApplyCheckedNoHeaderOnExistingFile(t *testing.T) {
	path := tmpConfigPath(t)
	if err := os.WriteFile(path, []byte("shell: zsh\n"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	if _, err := SetShell(path, cwdOf(path), "infra", "/tmp/infra", nil); err != nil {
		t.Fatalf("SetShell: %v", err)
	}
	if got := readFile(t, path); strings.Contains(got, "# .toolbox.yaml") {
		t.Errorf("existing file must not gain the create header, got:\n%s", got)
	}
}

func TestApplyCheckedIdempotent(t *testing.T) {
	path := tmpConfigPath(t)
	if _, err := SetShell(path, cwdOf(path), "infra", "/tmp/infra", nil); err != nil {
		t.Fatalf("first SetShell: %v", err)
	}

	changed, err := SetShell(path, cwdOf(path), "infra", "/tmp/infra", nil)
	if err != nil {
		t.Fatalf("second SetShell: %v", err)
	}
	if changed {
		t.Error("identical re-run must report changed=false")
	}
}

func TestSetShellNodeShape(t *testing.T) {
	path := tmpConfigPath(t)
	if _, err := SetShell(path, cwdOf(path), "infra", "/tmp/infra", nil); err != nil {
		t.Fatalf("SetShell: %v", err)
	}

	got := readFile(t, path)
	if !strings.Contains(got, "shells:\n  infra:\n    path: /tmp/infra") {
		t.Errorf("unexpected shells node shape:\n%s", got)
	}
}

func TestSetShellEnvSortedAndShaped(t *testing.T) {
	path := tmpConfigPath(t)
	if _, err := SetShell(path, cwdOf(path), "infra", "/tmp/infra", nil); err != nil {
		t.Fatalf("SetShell: %v", err)
	}

	if _, err := SetShellEnv(path, cwdOf(path), "infra", map[string]string{"ZED": "z", "ALPHA": "a"}); err != nil {
		t.Fatalf("SetShellEnv: %v", err)
	}
	got := readFile(t, path)
	if !strings.Contains(got, "env:\n      ALPHA: a\n      ZED: z") {
		t.Errorf("env keys must render sorted under shells.infra.env, got:\n%s", got)
	}
	if !strings.Contains(got, "path: /tmp/infra") {
		t.Errorf("path sibling must survive env write, got:\n%s", got)
	}
}

func TestRemoveShell(t *testing.T) {
	path := tmpConfigPath(t)
	if _, err := SetShell(path, cwdOf(path), "infra", "/tmp/infra", nil); err != nil {
		t.Fatalf("SetShell: %v", err)
	}
	if _, err := SetShell(path, cwdOf(path), "qa", "/tmp/qa", nil); err != nil {
		t.Fatalf("SetShell qa: %v", err)
	}

	changed, err := RemoveShell(path, cwdOf(path), "infra")
	if err != nil {
		t.Fatalf("RemoveShell: %v", err)
	}
	if !changed {
		t.Error("removal of existing entry must report changed")
	}
	got := readFile(t, path)
	if strings.Contains(got, "infra") {
		t.Errorf("infra entry must be gone, got:\n%s", got)
	}
	if !strings.Contains(got, "qa") {
		t.Errorf("sibling qa entry must survive, got:\n%s", got)
	}

	// Removing the last entry drops the shells: key entirely.
	if _, err := RemoveShell(path, cwdOf(path), "qa"); err != nil {
		t.Fatalf("RemoveShell qa: %v", err)
	}
	if got := readFile(t, path); strings.Contains(got, "shells") {
		t.Errorf("empty shells map must be dropped, got:\n%s", got)
	}

	// Unknown name is a no-op.
	changed, err = RemoveShell(path, cwdOf(path), "nope")
	if err != nil {
		t.Fatalf("RemoveShell nope: %v", err)
	}
	if changed {
		t.Error("removal of unknown entry must report changed=false")
	}
}

func TestAddMountAppendAndReplace(t *testing.T) {
	path := tmpConfigPath(t)

	if _, err := AddMount(path, cwdOf(path), config.Mount{Name: "scratch", Source: "~/scratch", Target: "/scratch", ReadOnly: true}); err != nil {
		t.Fatalf("AddMount: %v", err)
	}
	got := readFile(t, path)
	if !strings.Contains(got, "mounts:\n  - name: scratch\n    source: ~/scratch\n    target: /scratch\n    readonly: true") {
		t.Errorf("unexpected mount node shape:\n%s", got)
	}

	// Same name replaces in place (no duplicate entry).
	if _, err := AddMount(path, cwdOf(path), config.Mount{Name: "scratch", Source: "~/other", Target: "/scratch"}); err != nil {
		t.Fatalf("AddMount replace: %v", err)
	}
	got = readFile(t, path)
	if strings.Count(got, "name: scratch") != 1 {
		t.Errorf("replace must not duplicate the entry, got:\n%s", got)
	}
	if !strings.Contains(got, "source: ~/other") || strings.Contains(got, "readonly") {
		t.Errorf("replace must swap the whole entry, got:\n%s", got)
	}
}

func TestAddMountIntoFlowEmptyList(t *testing.T) {
	path := tmpConfigPath(t)
	if err := os.WriteFile(path, []byte("mounts: []\n"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	if _, err := AddMount(path, cwdOf(path), config.Mount{Name: "scratch", Source: "~/s", Target: "/s"}); err != nil {
		t.Fatalf("AddMount: %v", err)
	}
	if got := readFile(t, path); !strings.Contains(got, "mounts:\n  - name: scratch") {
		t.Errorf("flow [] placeholder must convert to block style, got:\n%s", got)
	}
}

func TestDisableMount(t *testing.T) {
	path := tmpConfigPath(t)

	// Absent entry → `{name, disabled: true}` patch is appended.
	if _, err := DisableMount(path, cwdOf(path), "gh"); err != nil {
		t.Fatalf("DisableMount: %v", err)
	}
	got := readFile(t, path)
	if !strings.Contains(got, "- name: gh\n    disabled: true") {
		t.Errorf("expected disable patch shape, got:\n%s", got)
	}

	// Existing entry gains disabled: true in place.
	if _, err := AddMount(path, cwdOf(path), config.Mount{Name: "scratch", Source: "~/s", Target: "/s"}); err != nil {
		t.Fatalf("AddMount: %v", err)
	}
	if _, err := DisableMount(path, cwdOf(path), "scratch"); err != nil {
		t.Fatalf("DisableMount scratch: %v", err)
	}
	got = readFile(t, path)
	if strings.Count(got, "name: scratch") != 1 {
		t.Errorf("disable of existing entry must not append a second one, got:\n%s", got)
	}
	if !strings.Contains(got, "source: ~/s") {
		t.Errorf("existing fields must survive in-place disable, got:\n%s", got)
	}
}

func TestRemoveMount(t *testing.T) {
	path := tmpConfigPath(t)
	if _, err := AddMount(path, cwdOf(path), config.Mount{Name: "scratch", Source: "~/s", Target: "/s"}); err != nil {
		t.Fatalf("AddMount: %v", err)
	}

	changed, err := RemoveMount(path, cwdOf(path), "scratch")
	if err != nil {
		t.Fatalf("RemoveMount: %v", err)
	}
	if !changed {
		t.Error("removal of existing entry must report changed")
	}
	if got := readFile(t, path); strings.Contains(got, "mounts") {
		t.Errorf("empty mounts list must be dropped, got:\n%s", got)
	}

	changed, err = RemoveMount(path, cwdOf(path), "scratch")
	if err != nil {
		t.Fatalf("RemoveMount again: %v", err)
	}
	if changed {
		t.Error("removal of absent entry must report changed=false")
	}
}

func TestSetMountsRoot(t *testing.T) {
	path := tmpConfigPath(t)
	if _, err := SetMountsRoot(path, cwdOf(path), "~/encrypted/toolbox"); err != nil {
		t.Fatalf("SetMountsRoot: %v", err)
	}
	if got := readFile(t, path); !strings.Contains(got, "mounts_root: ~/encrypted/toolbox") {
		t.Errorf("expected mounts_root key, got:\n%s", got)
	}
}

func TestUserShells(t *testing.T) {
	path := tmpConfigPath(t)

	shells, err := UserShells(path)
	if err != nil {
		t.Fatalf("UserShells on missing file: %v", err)
	}
	if len(shells) != 0 {
		t.Errorf("missing file must yield no shells, got %v", shells)
	}

	if _, err := SetShell(path, cwdOf(path), "infra", "/tmp/infra", nil); err != nil {
		t.Fatalf("SetShell: %v", err)
	}
	shells, err = UserShells(path)
	if err != nil {
		t.Fatalf("UserShells: %v", err)
	}
	if len(shells) != 1 || shells["infra"] != "/tmp/infra" {
		t.Errorf("UserShells = %v, want map[infra:/tmp/infra]", shells)
	}
}

func TestUserMountNames(t *testing.T) {
	path := tmpConfigPath(t)

	names, err := UserMountNames(path)
	if err != nil {
		t.Fatalf("UserMountNames on missing file: %v", err)
	}
	if len(names) != 0 {
		t.Errorf("missing file must yield no names, got %v", names)
	}

	if _, err := AddMount(path, cwdOf(path), config.Mount{Name: "scratch", Source: "~/s", Target: "/s"}); err != nil {
		t.Fatalf("AddMount: %v", err)
	}
	if _, err := DisableMount(path, cwdOf(path), "gh"); err != nil {
		t.Fatalf("DisableMount: %v", err)
	}

	names, err = UserMountNames(path)
	if err != nil {
		t.Fatalf("UserMountNames: %v", err)
	}
	want := []string{"scratch", "gh"}
	if len(names) != 2 || names[0] != want[0] || names[1] != want[1] {
		t.Errorf("UserMountNames = %v, want %v", names, want)
	}
}
