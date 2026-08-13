package configedit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	yaml "gopkg.in/yaml.v3"

	"github.com/filippolmt/toolbox/internal/configio"
)

// The write-pipeline cases below moved here from internal/configio when
// UpsertFile was deleted: ApplyChecked is now the only writer in the tree, so
// this is the seam that owns comment preservation, document bootstrap, the
// byte-equal short-circuit and loud parse failures.

// TestApplyCheckedPreservesCommentsAndOrder exercises the full pipeline against
// an existing file: user comments survive, pre-existing keys keep their order,
// and the mutation lands as an appended sibling.
func TestApplyCheckedPreservesCommentsAndOrder(t *testing.T) {
	path := tmpConfigPath(t)
	src := "# user comment\ntools:\n  gh: false # inline note\nimage: ghcr.io/x/y:1\n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	changed, err := ApplyChecked(path, cwdOf(path), func(doc *yaml.Node) {
		infra := configio.EnsureChildMap(configio.EnsureChildMap(doc, "shells"), "infra")
		configio.SetMapValue(infra, "path", "/tmp/infra")
	})
	if err != nil {
		t.Fatalf("ApplyChecked: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true")
	}

	got := readFile(t, path)
	for _, want := range []string{
		"# user comment", "# inline note", "gh: false",
		"image: ghcr.io/x/y:1", "shells:", "infra:", "path: /tmp/infra",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "tools:") > strings.Index(got, "image:") {
		t.Errorf("key order not preserved:\n%s", got)
	}
}

// TestApplyCheckedBootstrapsDocument covers the two empty-input branches: a
// missing file and a file holding only whitespace (yaml.Unmarshal leaves a zero
// node; EnsureDocumentMap must still materialise a mapping). Both must apply the
// mutation to a fresh document and leave the file with mode 0o600.
func TestApplyCheckedBootstrapsDocument(t *testing.T) {
	cases := []struct {
		name string
		seed []byte // nil → file absent
	}{
		{name: "missing file"},
		{name: "whitespace-only file", seed: []byte("\n\n   \n")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := tmpConfigPath(t)
			if tc.seed != nil {
				if err := os.WriteFile(path, tc.seed, 0o600); err != nil {
					t.Fatalf("seed: %v", err)
				}
			}
			changed, err := ApplyChecked(path, cwdOf(path), func(doc *yaml.Node) {
				configio.SetMapBool(configio.EnsureChildMap(doc, "sdd"), "openspec", true)
			})
			if err != nil {
				t.Fatalf("ApplyChecked: %v", err)
			}
			if !changed {
				t.Fatal("changed = false, want true")
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("stat %s: %v", path, err)
			}
			if info.Mode().Perm() != 0o600 {
				t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
			}
			if got := readFile(t, path); !strings.Contains(got, "openspec: true") {
				t.Fatalf("output missing mutation:\n%s", got)
			}
		})
	}
}

// TestApplyCheckedIdempotentRunDoesNotRewrite proves the byte-equal
// short-circuit skips the disk write entirely: a rewrite would reset the file
// mode to 0o600 (AtomicWriteFile), so a surviving 0o644 witness mode means no
// write happened.
func TestApplyCheckedIdempotentRunDoesNotRewrite(t *testing.T) {
	path := tmpConfigPath(t)
	mutate := func(doc *yaml.Node) {
		configio.SetMapBool(configio.EnsureChildMap(doc, "sdd"), "openspec", true)
	}
	if _, err := ApplyChecked(path, cwdOf(path), mutate); err != nil {
		t.Fatalf("first ApplyChecked: %v", err)
	}
	before := readFile(t, path)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod witness: %v", err)
	}

	changed, err := ApplyChecked(path, cwdOf(path), mutate)
	if err != nil {
		t.Fatalf("second ApplyChecked: %v", err)
	}
	if changed {
		t.Fatal("changed = true on idempotent re-run, want false")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("mode = %v, want witness 0644 (file was rewritten)", info.Mode().Perm())
	}
	if after := readFile(t, path); after != before {
		t.Fatalf("content changed on idempotent re-run:\n%s", after)
	}
}

// TestApplyCheckedUnparseableYAMLFailsLoudly asserts a corrupt file produces an
// error naming the path, the mutation never runs, and the file is left untouched.
func TestApplyCheckedUnparseableYAMLFailsLoudly(t *testing.T) {
	path := tmpConfigPath(t)
	bad := "mounts: [unclosed\n"
	if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	changed, err := ApplyChecked(path, cwdOf(path), func(_ *yaml.Node) {
		t.Fatal("mutate must not run on unparseable input")
	})
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("error %q does not mention path %q", err.Error(), path)
	}
	if changed {
		t.Fatal("changed = true on parse error, want false")
	}
	if got := readFile(t, path); got != bad {
		t.Fatalf("file must be left untouched, got:\n%s", got)
	}
}

// TestApplyCheckedRejectsWithoutWriting is the seam-level statement of the card-4
// guarantee: a candidate the doctor rejects never reaches disk — not even
// transiently — so there is nothing to roll back. The mutation writes an empty
// shells.<name>.path, which lintShellPaths reports as an error.
func TestApplyCheckedRejectsWithoutWriting(t *testing.T) {
	path := tmpConfigPath(t)
	src := "image: ghcr.io/x/y:1\n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	changed, err := ApplyChecked(path, cwdOf(path), func(doc *yaml.Node) {
		entry := configio.EnsureChildMap(configio.EnsureChildMap(doc, "shells"), "infra")
		configio.SetMapValue(entry, "path", "")
	})
	if err == nil {
		t.Fatal("a candidate with an empty shell path must be rejected")
	}
	if !strings.Contains(err.Error(), "shells.infra.path is empty") {
		t.Errorf("error must name the finding, got: %v", err)
	}
	if changed {
		t.Error("a rejected candidate must report changed=false")
	}
	if got := readFile(t, path); got != src {
		t.Errorf("a rejected candidate must leave the file byte-identical:\n%s", got)
	}
}

// TestApplyCheckedCreatesNothingWhenRejected: a rejected candidate for a file
// that does not exist yet must not leave a file behind — the render-then-write
// order makes that structural, where write-then-rollback had to delete it again.
func TestApplyCheckedCreatesNothingWhenRejected(t *testing.T) {
	path := tmpConfigPath(t)

	if _, err := ApplyChecked(path, cwdOf(path), func(doc *yaml.Node) {
		entry := configio.EnsureChildMap(configio.EnsureChildMap(doc, "shells"), "infra")
		configio.SetMapValue(entry, "path", "")
	}); err == nil {
		t.Fatal("a candidate with an empty shell path must be rejected")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("rejected creation must leave no file at %s (err=%v)", path, err)
	}
}

// TestApplyCheckedValidatesTheCandidateInItsOwnLayer: an edit to the project
// file must be judged against the layers a real load would see, not against the
// project file alone. Here the shell's path lives in the global file and only its
// env overlay is written locally — the exact shape `toolbox shells set --where
// local` produces, and a project-only view of that file would reject it for a
// missing path.
func TestApplyCheckedValidatesTheCandidateInItsOwnLayer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	shellDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".toolbox.yaml"),
		[]byte("shells:\n  infra:\n    path: "+shellDir+"\n"), 0o600); err != nil {
		t.Fatalf("seed global: %v", err)
	}
	project := filepath.Join(t.TempDir(), ".toolbox.yaml")

	changed, err := SetShellEnv(project, cwdOf(project), "infra", map[string]string{"FOO": "bar"})
	if err != nil {
		t.Fatalf("an env overlay for a globally-defined shell must be accepted: %v", err)
	}
	if !changed {
		t.Error("changed = false, want true")
	}
	if got := readFile(t, project); !strings.Contains(got, "FOO: bar") {
		t.Errorf("env overlay not written:\n%s", got)
	}
}

// TestApplyCheckedIgnoresAnotherLayersFinding: a candidate is answerable for
// what it says, not for what another layer says. Removing the global shell entry
// leaves the project file's env overlay dangling — a finding about the *merged*
// result, produced by a file this write does not touch, so it must not block the
// removal. Without this the CLI can write an overlay it can never undo.
func TestApplyCheckedIgnoresAnotherLayersFinding(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	globalPath := filepath.Join(home, ".toolbox.yaml")
	shellDir := t.TempDir()
	if err := os.WriteFile(globalPath,
		[]byte("shells:\n  infra:\n    path: "+shellDir+"\n"), 0o600); err != nil {
		t.Fatalf("seed global: %v", err)
	}
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, ".toolbox.yaml"),
		[]byte("shells:\n  infra:\n    env:\n      A: \"1\"\n"), 0o600); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	changed, err := RemoveShell(globalPath, repo, "infra")
	if err != nil {
		t.Fatalf("removing the global entry must not be blocked by the project overlay: %v", err)
	}
	if !changed {
		t.Error("changed = false, want true")
	}
	if got := readFile(t, globalPath); strings.Contains(got, "infra") {
		t.Errorf("entry must be removed:\n%s", got)
	}
}
