package configio

import (
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestGlobalConfigPath asserts the global config resolves to .toolbox.yaml
// joined onto the home directory — the location internal/config/plan.go's
// read must agree with.
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
