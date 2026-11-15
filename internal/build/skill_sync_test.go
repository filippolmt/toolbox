package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// bakedSkillsDir is where the image bakes every global skill at build time,
// extracted from the release that ships the tool (Dockerfile fetch stages).
const bakedSkillsDir = "/usr/local/share/toolbox/skills"

// wireBakedSkillsForTest repoints the Agent Topology copy wireAgentTopologyForTest
// wrote under dir at a fixture baked-skills tree.
func wireBakedSkillsForTest(t *testing.T, dir, baked string) {
	t.Helper()
	lib := filepath.Join(dir, "agent-topology-lib.sh")
	b, err := os.ReadFile(lib)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), bakedSkillsDir) {
		t.Fatalf("%s does not name %s", agentTopologyLib, bakedSkillsDir)
	}
	if err := os.WriteFile(lib, []byte(strings.ReplaceAll(string(b), bakedSkillsDir, baked)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// skillSyncFixture is a home with Claude's state mounted, fake agents on PATH
// and one baked skill, "demo", with a nested reference file.
type skillSyncFixture struct {
	dir, home, baked, script string
	env                      []string
}

func newSkillSyncFixture(t *testing.T, agents ...string) skillSyncFixture {
	t.Helper()
	if _, err := exec.LookPath("flock"); err != nil {
		t.Skip("flock not on PATH")
	}
	dir := t.TempDir()
	f := skillSyncFixture{dir: dir, home: filepath.Join(dir, "home"), baked: filepath.Join(dir, "baked")}
	if err := os.MkdirAll(filepath.Join(f.home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(f.baked, "demo", "SKILL.md"), "# demo\n")
	writeFile(t, filepath.Join(f.baked, "demo", "reference", "more.md"), "more\n")

	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, a := range agents {
		writeTestExecutable(t, filepath.Join(binDir, a), "#!/bin/sh\nexit 0\n")
	}
	body := wireAgentTopologyForTest(t, dir, []byte("#!/usr/bin/env bash\nset -euo pipefail\n"+agentTopologySource+"\ntoolbox_sync_skill \"$@\"\n"))
	wireBakedSkillsForTest(t, dir, f.baked)
	f.script = filepath.Join(dir, "sync.sh")
	if err := os.WriteFile(f.script, body, 0o755); err != nil {
		t.Fatal(err)
	}
	f.env = []string{"HOME=" + f.home, "PATH=" + binDir + ":/usr/bin:/bin"}
	return f
}

func (f skillSyncFixture) run(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("/bin/bash", append([]string{f.script}, args...)...)
	cmd.Env = f.env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("toolbox_sync_skill %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func readSkill(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("skill not installed: %v", err)
	}
	return string(b)
}

// TestSyncSkillInstallsIntoEveryActiveRoot: Claude reads its own config root,
// Codex and pi the cross-agent ~/.agents/skills, which is container-local and
// starts empty on every recreate.
func TestSyncSkillInstallsIntoEveryActiveRoot(t *testing.T) {
	f := newSkillSyncFixture(t, "claude", "pi")
	f.run(t, "demo")
	for _, root := range []string{filepath.Join(f.home, ".claude", "skills"), filepath.Join(f.home, ".agents", "skills")} {
		if got := readSkill(t, filepath.Join(root, "demo", "SKILL.md")); got != "# demo\n" {
			t.Errorf("%s: SKILL.md = %q", root, got)
		}
		if got := readSkill(t, filepath.Join(root, "demo", "reference", "more.md")); got != "more\n" {
			t.Errorf("%s: reference file = %q", root, got)
		}
	}
}

// TestSyncSkillLeavesACurrentCopyUntouched is the "two starts, no write"
// criterion: a Claude skill root is one host mount every container shares, so
// a rewrite on each start is churn every other container observes.
func TestSyncSkillLeavesACurrentCopyUntouched(t *testing.T) {
	f := newSkillSyncFixture(t, "claude")
	f.run(t, "demo")
	skill := filepath.Join(f.home, ".claude", "skills", "demo", "SKILL.md")
	past := time.Unix(1_000_000, 0)
	if err := os.Chtimes(skill, past, past); err != nil {
		t.Fatal(err)
	}
	f.run(t, "demo")
	st, err := os.Stat(skill)
	if err != nil {
		t.Fatal(err)
	}
	if !st.ModTime().Equal(past) {
		t.Errorf("a current SKILL.md was rewritten on the second start (mtime %v)", st.ModTime())
	}
}

// TestSyncSkillRestoresADeletedOrEditedCopy: the baked copy is the authority;
// a missing skill self-heals and a hand edit is replaced, stray files included.
func TestSyncSkillRestoresADeletedOrEditedCopy(t *testing.T) {
	f := newSkillSyncFixture(t, "claude", "codex")
	f.run(t, "demo")
	claude := filepath.Join(f.home, ".claude", "skills", "demo")
	agents := filepath.Join(f.home, ".agents", "skills", "demo")
	if err := os.RemoveAll(agents); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(claude, "SKILL.md"), "hand edit\n")
	writeFile(t, filepath.Join(claude, "stale.md"), "dropped upstream\n")
	f.run(t, "demo")
	for _, d := range []string{claude, agents} {
		if got := readSkill(t, filepath.Join(d, "SKILL.md")); got != "# demo\n" {
			t.Errorf("%s: SKILL.md = %q, want the baked copy", d, got)
		}
	}
	if _, err := os.Stat(filepath.Join(claude, "stale.md")); !os.IsNotExist(err) {
		t.Error("a file the baked skill does not carry survived the sync")
	}
}

// TestSyncSkillSkipsWhatTheImageDidNotBake: no baked skill, nothing written,
// no failure — an image without the tool carries no skill for it.
func TestSyncSkillSkipsWhatTheImageDidNotBake(t *testing.T) {
	f := newSkillSyncFixture(t, "claude")
	if out := f.run(t, "absent"); out != "" {
		t.Errorf("unexpected output: %q", out)
	}
	if _, err := os.Stat(filepath.Join(f.home, ".claude", "skills", "absent")); !os.IsNotExist(err) {
		t.Error("a skill the image did not bake was created")
	}
}

// TestSyncSkillReportsAFailedRootAndSyncsTheRest: a root that cannot take the
// skill fails the call — toolbox-refresh exits non-zero on it — while the
// other roots are still synced.
func TestSyncSkillReportsAFailedRootAndSyncsTheRest(t *testing.T) {
	f := newSkillSyncFixture(t, "claude", "pi")
	// A file where the Claude skill root should be: mkdir -p cannot get past it.
	writeFile(t, filepath.Join(f.home, ".claude", "skills"), "not a directory\n")
	cmd := exec.Command("/bin/bash", f.script, "demo")
	cmd.Env = f.env
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("toolbox_sync_skill succeeded with an unwritable root\n%s", out)
	}
	if !strings.Contains(string(out), "demo skill install into") {
		t.Errorf("failed root not reported: %q", out)
	}
	readSkill(t, filepath.Join(f.home, ".agents", "skills", "demo", "SKILL.md"))
}
