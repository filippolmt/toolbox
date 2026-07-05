package configedit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/sdd"
)

var fenceSkill = sdd.Skill{
	Key:              "fixture",
	NpmPackage:       "fixture-pkg",
	Version:          "0.0.1",
	BinName:          "fixture",
	GitignoreEntries: []string{".fixture/output/", ".fixture/cache.json"},
}

var bareSkill = sdd.Skill{
	Key:        "bare",
	NpmPackage: "bare-pkg",
	Version:    "0.0.1",
	BinName:    "bare",
}

// TestEnableSDDWritesFlagAndFence: enabling a skill with gitignore entries
// writes the yaml flag and the fenced block, reporting both changed.
func TestEnableSDDWritesFlagAndFence(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, ".toolbox.yaml")
	gitignorePath := filepath.Join(dir, ".gitignore")

	res, err := EnableSDD(yamlPath, gitignorePath, fenceSkill)
	if err != nil {
		t.Fatalf("EnableSDD: %v", err)
	}
	if !res.YAMLChanged || !res.GitignoreChanged || res.GitignoreSkipped {
		t.Errorf("want yaml+gitignore changed, not skipped; got %+v", res)
	}
	if got := readFile(t, yamlPath); !strings.Contains(got, "fixture: true") {
		t.Errorf("yaml missing flag:\n%s", got)
	}
	gi := readFile(t, gitignorePath)
	for _, want := range []string{GitignoreFenceStart("fixture"), ".fixture/output/", GitignoreFenceEnd("fixture")} {
		if !strings.Contains(gi, want) {
			t.Errorf("gitignore missing %q:\n%s", want, gi)
		}
	}
}

// TestEnableSDDIdempotent: a re-enable reports no change on either file.
func TestEnableSDDIdempotent(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, ".toolbox.yaml")
	gitignorePath := filepath.Join(dir, ".gitignore")

	if _, err := EnableSDD(yamlPath, gitignorePath, fenceSkill); err != nil {
		t.Fatalf("EnableSDD first: %v", err)
	}
	res, err := EnableSDD(yamlPath, gitignorePath, fenceSkill)
	if err != nil {
		t.Fatalf("EnableSDD second: %v", err)
	}
	if res.YAMLChanged || res.GitignoreChanged {
		t.Errorf("re-enable must be a no-op, got %+v", res)
	}
}

// TestEnableSDDEmptyEntriesSkipsFence: a skill with no gitignore entries
// writes the flag but leaves .gitignore untouched, reporting GitignoreSkipped.
func TestEnableSDDEmptyEntriesSkipsFence(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, ".toolbox.yaml")
	gitignorePath := filepath.Join(dir, ".gitignore")

	res, err := EnableSDD(yamlPath, gitignorePath, bareSkill)
	if err != nil {
		t.Fatalf("EnableSDD: %v", err)
	}
	if !res.GitignoreSkipped || res.GitignoreChanged {
		t.Errorf("want GitignoreSkipped, not changed; got %+v", res)
	}
	if _, err := os.Stat(gitignorePath); !os.IsNotExist(err) {
		t.Errorf(".gitignore must not be created (stat err=%v)", err)
	}
}

// TestSetSDDEnabledPreservesObjectForm: an object-form steps override is left
// untouched when the same key is enabled via the bool shorthand.
func TestSetSDDEnabledPreservesObjectForm(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, ".toolbox.yaml")
	if err := os.WriteFile(yamlPath, []byte("sdd:\n  fixture:\n    steps:\n      - [\"--flag\"]\n"), 0o600); err != nil {
		t.Fatalf("seed yaml: %v", err)
	}

	if _, err := EnableSDD(yamlPath, filepath.Join(dir, ".gitignore"), fenceSkill); err != nil {
		t.Fatalf("EnableSDD: %v", err)
	}
	if got := readFile(t, yamlPath); !strings.Contains(got, "steps:") {
		t.Errorf("object-form override must survive:\n%s", got)
	}
}

// TestRemoveSDDGitignore: the disable path removes the fence and no-ops when
// the fence is absent.
func TestRemoveSDDGitignore(t *testing.T) {
	dir := t.TempDir()
	gitignorePath := filepath.Join(dir, ".gitignore")

	if _, err := WriteSDDGitignore(gitignorePath, fenceSkill); err != nil {
		t.Fatalf("WriteSDDGitignore: %v", err)
	}
	changed, err := RemoveSDDGitignore(gitignorePath, fenceSkill)
	if err != nil {
		t.Fatalf("RemoveSDDGitignore: %v", err)
	}
	if !changed {
		t.Error("removing a present fence must report changed")
	}
	if got := readFile(t, gitignorePath); strings.Contains(got, GitignoreFenceStart("fixture")) {
		t.Errorf("fence must be gone:\n%s", got)
	}

	changed, err = RemoveSDDGitignore(gitignorePath, fenceSkill)
	if err != nil {
		t.Fatalf("RemoveSDDGitignore (absent): %v", err)
	}
	if changed {
		t.Error("removing an absent fence must be a no-op")
	}
}
