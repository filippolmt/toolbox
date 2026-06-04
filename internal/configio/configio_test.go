package configio

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestAtomicWriteFileLeavesNoTemp asserts the rename pattern cleans up the
// sibling temp file on success and leaves the destination with the
// rewritten bytes.
func TestAtomicWriteFileLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, ".toolbox.yaml")
	if err := AtomicWriteFile(dest, []byte("hello"), 0o600); err != nil {
		t.Fatalf("AtomicWriteFile: %v", err)
	}
	b, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if string(b) != "hello" {
		t.Fatalf("dest = %q, want hello", string(b))
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("leftover temp file: %s", e.Name())
		}
	}
}

// TestAtomicWriteFileOverwrites asserts a second write replaces the prior
// content rather than leaving a partial / appended file.
func TestAtomicWriteFileOverwrites(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "f.yaml")
	if err := AtomicWriteFile(dest, []byte("v1"), 0o600); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := AtomicWriteFile(dest, []byte("v2"), 0o600); err != nil {
		t.Fatalf("second write: %v", err)
	}
	b, _ := os.ReadFile(dest)
	if string(b) != "v2" {
		t.Fatalf("dest = %q, want v2", string(b))
	}
}

// TestGlobalConfigPath returns the joined home path; the directory helper
// returns the same parent so callers can write a sibling temp file
// without resolving HOME a second time.
func TestGlobalConfigPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	p, err := GlobalConfigPath()
	if err != nil {
		t.Fatalf("GlobalConfigPath: %v", err)
	}
	if p != filepath.Join(home, ".toolbox.yaml") {
		t.Fatalf("path = %q, want %q", p, filepath.Join(home, ".toolbox.yaml"))
	}
	d, err := GlobalConfigDir()
	if err != nil {
		t.Fatalf("GlobalConfigDir: %v", err)
	}
	if d != home {
		t.Fatalf("dir = %q, want %q", d, home)
	}
}

// TestEnsureChildMapPreservesSiblings exercises the comment-preservation
// path: an existing key/value pair stays put and a new sibling is appended
// after it.
func TestEnsureChildMapPreservesSiblings(t *testing.T) {
	src := "tools:\n  gh: false\n"
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(src), &root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	doc := EnsureDocumentMap(&root)
	shells := EnsureChildMap(doc, "shells")
	infra := EnsureChildMap(shells, "infra")
	SetMapValue(infra, "path", "/tmp/infra")

	out, err := yaml.Marshal(&root)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(out)
	for _, want := range []string{"tools:", "gh: false", "shells:", "infra:", "path: /tmp/infra"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

// TestUpsertFilePreservesCommentsAndOrder exercises the full pipeline
// against an existing file: user comments survive, pre-existing keys keep
// their order, and the mutation lands as an appended sibling.
func TestUpsertFilePreservesCommentsAndOrder(t *testing.T) {
	dest := filepath.Join(t.TempDir(), ".toolbox.yaml")
	src := "# user comment\ntools:\n  gh: false # inline note\nimage: custom\n"
	if err := os.WriteFile(dest, []byte(src), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	changed, err := UpsertFile(dest, func(doc *yaml.Node) {
		infra := EnsureChildMap(EnsureChildMap(doc, "shells"), "infra")
		SetMapValue(infra, "path", "/tmp/infra")
	})
	if err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true")
	}

	b, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	got := string(b)
	for _, want := range []string{"# user comment", "# inline note", "gh: false", "image: custom", "shells:", "infra:", "path: /tmp/infra"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "tools:") > strings.Index(got, "image:") {
		t.Errorf("key order not preserved:\n%s", got)
	}
}

// TestUpsertFileBootstrapsDocument covers the two empty-input branches of
// the read switch: a missing file (os.ErrNotExist) and a file holding only
// whitespace (yaml.Unmarshal leaves a zero node; EnsureDocumentMap must
// still materialise a mapping). Both must apply the mutation to a fresh
// document and leave the file with mode 0o600.
func TestUpsertFileBootstrapsDocument(t *testing.T) {
	cases := []struct {
		name string
		seed []byte // nil → file absent
	}{
		{name: "missing file"},
		{name: "whitespace-only file", seed: []byte("\n\n   \n")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), ".toolbox.yaml")
			if tc.seed != nil {
				if err := os.WriteFile(dest, tc.seed, 0o600); err != nil {
					t.Fatalf("seed: %v", err)
				}
			}
			changed, err := UpsertFile(dest, func(doc *yaml.Node) {
				SetMapBool(EnsureChildMap(doc, "sdd"), "openspec", true)
			})
			if err != nil {
				t.Fatalf("UpsertFile: %v", err)
			}
			if !changed {
				t.Fatal("changed = false, want true")
			}
			info, err := os.Stat(dest)
			if err != nil {
				t.Fatalf("stat dest: %v", err)
			}
			if info.Mode().Perm() != 0o600 {
				t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
			}
			b, _ := os.ReadFile(dest)
			if !strings.Contains(string(b), "openspec: true") {
				t.Fatalf("output missing mutation:\n%s", string(b))
			}
		})
	}
}

// TestUpsertFileIdempotentRunDoesNotRewrite proves the byte-equal
// short-circuit skips the disk write entirely: a rewrite would reset the
// file mode to 0o600 (AtomicWriteFile), so a surviving 0o644 witness mode
// means no write happened.
func TestUpsertFileIdempotentRunDoesNotRewrite(t *testing.T) {
	dest := filepath.Join(t.TempDir(), ".toolbox.yaml")
	mutate := func(doc *yaml.Node) {
		SetMapBool(EnsureChildMap(doc, "sdd"), "openspec", true)
	}
	if _, err := UpsertFile(dest, mutate); err != nil {
		t.Fatalf("first UpsertFile: %v", err)
	}
	before, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if err := os.Chmod(dest, 0o644); err != nil {
		t.Fatalf("chmod witness: %v", err)
	}

	changed, err := UpsertFile(dest, mutate)
	if err != nil {
		t.Fatalf("second UpsertFile: %v", err)
	}
	if changed {
		t.Fatal("changed = true on idempotent re-run, want false")
	}
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("stat dest: %v", err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("mode = %v, want witness 0644 (file was rewritten)", info.Mode().Perm())
	}
	after, _ := os.ReadFile(dest)
	if string(after) != string(before) {
		t.Fatalf("content changed on idempotent re-run:\n%s", string(after))
	}
}

// TestUpsertFileUnparseableYAMLFailsLoudly asserts a corrupt file produces
// an error naming the path, the mutation never runs, and the file is left
// untouched.
func TestUpsertFileUnparseableYAMLFailsLoudly(t *testing.T) {
	dest := filepath.Join(t.TempDir(), ".toolbox.yaml")
	bad := "tools: [unclosed\n"
	if err := os.WriteFile(dest, []byte(bad), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	changed, err := UpsertFile(dest, func(doc *yaml.Node) {
		t.Fatal("mutate must not run on unparseable input")
	})
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
	if !strings.Contains(err.Error(), dest) {
		t.Fatalf("error %q does not mention path %q", err.Error(), dest)
	}
	if changed {
		t.Fatal("changed = true on parse error, want false")
	}
	b, _ := os.ReadFile(dest)
	if string(b) != bad {
		t.Fatalf("file modified on parse error:\n%s", string(b))
	}
}

// TestSetMapValueReplacesExistingScalar locks in the in-place replacement
// behaviour — the prior scalar is overwritten, the key/value pair count
// stays the same.
func TestSetMapValueReplacesExistingScalar(t *testing.T) {
	src := "shells:\n  infra:\n    path: /old\n"
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(src), &root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	doc := EnsureDocumentMap(&root)
	shells := EnsureChildMap(doc, "shells")
	infra := EnsureChildMap(shells, "infra")
	SetMapValue(infra, "path", "/new")

	out, _ := yaml.Marshal(&root)
	got := string(out)
	if !strings.Contains(got, "path: /new") {
		t.Fatalf("expected updated path, got:\n%s", got)
	}
	if strings.Contains(got, "/old") {
		t.Fatalf("old value not replaced:\n%s", got)
	}
}
