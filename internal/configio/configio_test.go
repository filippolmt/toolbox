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
