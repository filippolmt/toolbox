package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/sdd"
)

// withFixtureSkill swaps the package-level sdd.Skills slice for the
// duration of a test, appending a synthetic skill so a static-fence
// scenario can be exercised without depending on which production skills
// currently sit in the registry.
func withFixtureSkill(t *testing.T, s sdd.Skill) {
	t.Helper()
	orig := sdd.Skills
	sdd.Skills = append([]sdd.Skill{}, orig...)
	sdd.Skills = append(sdd.Skills, s)
	t.Cleanup(func() { sdd.Skills = orig })
}

// TestSDDInitGSDWritesGitignoreFence covers gsd's static-fence path:
// `.toolbox.yaml` gets the opt-in flag, `.gitignore` receives the fenced
// glob block, and a re-run is a byte-identical no-op.
func TestSDDInitGSDWritesGitignoreFence(t *testing.T) {
	dir := chdirTemp(t)

	cmd := sddInitCmd
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	if err := runSDDInit(cmd, []string{"gsd"}); err != nil {
		t.Fatalf("runSDDInit gsd: %v", err)
	}

	yamlBody, err := os.ReadFile(filepath.Join(dir, ".toolbox.yaml"))
	if err != nil {
		t.Fatalf("read .toolbox.yaml: %v", err)
	}
	got := string(yamlBody)
	if !strings.Contains(got, "sdd:") || !strings.Contains(got, "gsd: true") {
		t.Errorf(".toolbox.yaml missing sdd.gsd:\n%s", got)
	}

	giBody, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	for _, want := range []string{
		gitignoreFenceStart("gsd"),
		gitignoreFenceEnd("gsd"),
		".claude/agents/gsd-*",
		".claude/get-shit-done/",
		".claude/skills/gsd-*/",
		".codex/agents/gsd-*",
		".codex/get-shit-done/",
		".codex/skills/gsd-*/",
	} {
		if !strings.Contains(string(giBody), want) {
			t.Errorf(".gitignore missing %q:\n%s", want, giBody)
		}
	}

	out.Reset()
	if err := runSDDInit(cmd, []string{"gsd"}); err != nil {
		t.Fatalf("runSDDInit gsd (second): %v", err)
	}
	yamlBody2, _ := os.ReadFile(filepath.Join(dir, ".toolbox.yaml"))
	giBody2, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if !bytes.Equal(yamlBody, yamlBody2) {
		t.Errorf(".toolbox.yaml drifted on re-run\nfirst:\n%s\nsecond:\n%s", yamlBody, yamlBody2)
	}
	if !bytes.Equal(giBody, giBody2) {
		t.Errorf(".gitignore drifted on re-run\nfirst:\n%s\nsecond:\n%s", giBody, giBody2)
	}
}

// TestSDDInitFixtureSkillCreatesYAMLAndGitignore exercises the same
// static-fence path with a synthetic skill so the contract is covered
// even if every production row changes.
func TestSDDInitFixtureSkillCreatesYAMLAndGitignore(t *testing.T) {
	withFixtureSkill(t, sdd.Skill{
		Key:        "fixture",
		NpmPackage: "fixture-pkg",
		Version:    "0.0.1",
		BinName:    "fixture",
		InstallSteps: [][]string{
			{"init"},
		},
		GitignoreEntries: []string{
			".fixture/output/",
			".fixture/cache.json",
		},
	})

	dir := chdirTemp(t)

	cmd := sddInitCmd
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := runSDDInit(cmd, []string{"fixture"}); err != nil {
		t.Fatalf("runSDDInit fixture: %v", err)
	}

	yamlBody, err := os.ReadFile(filepath.Join(dir, ".toolbox.yaml"))
	if err != nil {
		t.Fatalf("read .toolbox.yaml: %v", err)
	}
	if !strings.Contains(string(yamlBody), "fixture: true") {
		t.Errorf(".toolbox.yaml missing fixture: true:\n%s", yamlBody)
	}

	giBody, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	for _, want := range []string{
		gitignoreFenceStart("fixture"),
		gitignoreFenceEnd("fixture"),
		".fixture/output/",
		".fixture/cache.json",
	} {
		if !strings.Contains(string(giBody), want) {
			t.Errorf(".gitignore missing %q:\n%s", want, giBody)
		}
	}

	if err := runSDDInit(cmd, []string{"fixture"}); err != nil {
		t.Fatalf("runSDDInit fixture (second): %v", err)
	}
	yamlBody2, _ := os.ReadFile(filepath.Join(dir, ".toolbox.yaml"))
	giBody2, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if !bytes.Equal(yamlBody, yamlBody2) {
		t.Errorf(".toolbox.yaml drifted on re-run\nfirst:\n%s\nsecond:\n%s", yamlBody, yamlBody2)
	}
	if !bytes.Equal(giBody, giBody2) {
		t.Errorf(".gitignore drifted on re-run\nfirst:\n%s\nsecond:\n%s", giBody, giBody2)
	}
}

// TestSDDInitSkillWithoutGitignoreEntries covers integrations whose
// upstream installer emits purely user-authored content (bmad):
// .toolbox.yaml gets the opt-in flag, .gitignore stays untouched.
func TestSDDInitSkillWithoutGitignoreEntries(t *testing.T) {
	dir := chdirTemp(t)

	cmd := sddInitCmd
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := runSDDInit(cmd, []string{"bmad"}); err != nil {
		t.Fatalf("runSDDInit bmad: %v", err)
	}

	yamlBody, _ := os.ReadFile(filepath.Join(dir, ".toolbox.yaml"))
	if !strings.Contains(string(yamlBody), "bmad: true") {
		t.Errorf(".toolbox.yaml missing bmad: true:\n%s", yamlBody)
	}

	if _, err := os.Stat(filepath.Join(dir, ".gitignore")); !os.IsNotExist(err) {
		t.Errorf(".gitignore should not be created for skills with no GitignoreEntries (err=%v)", err)
	}
}

// TestSDDInitOpenSpecWritesGitignoreFence locks in the openspec footprint:
// `openspec init --tools=claude,codex` regenerates adapter files under
// .claude/skills/openspec-*, .claude/commands/opsx/, .codex/skills/openspec-*
// on every shell, so those paths must land in the fenced gitignore block.
// Source-of-truth content under openspec/ (specs, changes, config.yaml)
// stays committed and never appears in the fence.
func TestSDDInitOpenSpecWritesGitignoreFence(t *testing.T) {
	dir := chdirTemp(t)

	cmd := sddInitCmd
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := runSDDInit(cmd, []string{"openspec"}); err != nil {
		t.Fatalf("runSDDInit openspec: %v", err)
	}

	giBody, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	for _, want := range []string{
		gitignoreFenceStart("openspec"),
		gitignoreFenceEnd("openspec"),
		".claude/skills/openspec-*/",
		".claude/commands/opsx/",
		".codex/skills/openspec-*/",
		"openspec/config.yaml",
	} {
		if !strings.Contains(string(giBody), want) {
			t.Errorf(".gitignore missing %q:\n%s", want, giBody)
		}
	}
	for _, forbidden := range []string{"openspec/specs", "openspec/changes"} {
		if strings.Contains(string(giBody), forbidden) {
			t.Errorf(".gitignore must not list user-authored %q:\n%s", forbidden, giBody)
		}
	}
}

// TestSDDInitRejectsUnknownSkill locks in the contract that a bogus
// integration name fails fast with a usageError listing supported keys.
func TestSDDInitRejectsUnknownSkill(t *testing.T) {
	chdirTemp(t)

	cmd := sddInitCmd
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	err := runSDDInit(cmd, []string{"nonsense"})
	if err == nil {
		t.Fatal("expected error for unknown skill name")
	}
	if !strings.Contains(err.Error(), "unknown sdd integration") {
		t.Errorf("error %q should mention 'unknown sdd integration'", err.Error())
	}
	if !strings.Contains(err.Error(), "gsd") {
		t.Errorf("error %q should list 'gsd' among supported keys", err.Error())
	}
}

// TestSDDInitAddsKeyToExistingBlock asserts that a pre-existing
// `sdd:` block in .toolbox.yaml gets the new key injected without
// duplicating the header or losing sibling keys.
func TestSDDInitAddsKeyToExistingBlock(t *testing.T) {
	dir := chdirTemp(t)

	original := "shell: bash\nsdd:\n  gsd: true\n"
	yamlPath := filepath.Join(dir, ".toolbox.yaml")
	if err := os.WriteFile(yamlPath, []byte(original), 0o600); err != nil {
		t.Fatalf("seed yaml: %v", err)
	}

	cmd := sddInitCmd
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := runSDDInit(cmd, []string{"bmad"}); err != nil {
		t.Fatalf("runSDDInit bmad: %v", err)
	}

	body, _ := os.ReadFile(yamlPath)
	got := string(body)
	if strings.Count(got, "sdd:") != 1 {
		t.Errorf("expected single sdd: block, got:\n%s", got)
	}
	for _, want := range []string{"shell: bash", "gsd: true", "bmad: true"} {
		if !strings.Contains(got, want) {
			t.Errorf("yaml missing %q:\n%s", want, got)
		}
	}
}

// TestSDDInitFlipsFalseToTrue asserts that an existing sdd.<key>: false
// is rewritten to true instead of duplicated.
func TestSDDInitFlipsFalseToTrue(t *testing.T) {
	dir := chdirTemp(t)

	original := "sdd:\n  gsd: false\n"
	yamlPath := filepath.Join(dir, ".toolbox.yaml")
	if err := os.WriteFile(yamlPath, []byte(original), 0o600); err != nil {
		t.Fatalf("seed yaml: %v", err)
	}

	cmd := sddInitCmd
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := runSDDInit(cmd, []string{"gsd"}); err != nil {
		t.Fatalf("runSDDInit gsd: %v", err)
	}

	body, _ := os.ReadFile(yamlPath)
	got := string(body)
	if strings.Count(got, "gsd:") != 1 {
		t.Errorf("expected single gsd: directive, got:\n%s", got)
	}
	if !strings.Contains(got, "gsd: true") {
		t.Errorf("yaml should now contain 'gsd: true':\n%s", got)
	}
}

// TestSDDInitPreservesExistingGitignoreEntries asserts that unrelated
// .gitignore lines stay intact when the per-skill fenced block is
// appended.
func TestSDDInitPreservesExistingGitignoreEntries(t *testing.T) {
	withFixtureSkill(t, sdd.Skill{
		Key:        "fixture",
		NpmPackage: "fixture-pkg",
		Version:    "0.0.1",
		BinName:    "fixture",
		InstallSteps: [][]string{
			{"init"},
		},
		GitignoreEntries: []string{
			".fixture/output/",
		},
	})

	dir := chdirTemp(t)

	giPath := filepath.Join(dir, ".gitignore")
	original := "dist/\nnode_modules/\n"
	if err := os.WriteFile(giPath, []byte(original), 0o600); err != nil {
		t.Fatalf("seed gitignore: %v", err)
	}

	cmd := sddInitCmd
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := runSDDInit(cmd, []string{"fixture"}); err != nil {
		t.Fatalf("runSDDInit fixture: %v", err)
	}

	body, _ := os.ReadFile(giPath)
	got := string(body)
	for _, want := range []string{
		"dist/", "node_modules/",
		gitignoreFenceStart("fixture"),
		".fixture/output/",
		gitignoreFenceEnd("fixture"),
	} {
		if !strings.Contains(got, want) {
			t.Errorf(".gitignore missing %q:\n%s", want, got)
		}
	}
}

// TestSDDListIncludesAllSkills smoke-checks the list subcommand surfaces
// every registry entry with its pinned version.
func TestSDDListIncludesAllSkills(t *testing.T) {
	cmd := sddListCmd
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(&bytes.Buffer{})
	if err := runSDDList(cmd, nil); err != nil {
		t.Fatalf("runSDDList: %v", err)
	}
	got := buf.String()
	for _, want := range []string{"gsd", "bmad", "openspec", "@"} {
		if !strings.Contains(got, want) {
			t.Errorf("list output missing %q:\n%s", want, got)
		}
	}
}
