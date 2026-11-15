package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// globalSkillInit runs one init.d member that installs a baked global skill,
// with a fake tool binary that logs every call, fake agents on PATH and the
// baked skill as a fixture. It returns the home and the logged tool calls.
func globalSkillInit(t *testing.T, script, tool, skill string, agents ...string) (home string, calls []string) {
	t.Helper()
	if _, err := exec.LookPath("flock"); err != nil {
		t.Skip("flock not on PATH")
	}
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.Mkdir(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "calls")
	writeTestExecutable(t, filepath.Join(binDir, tool), "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \""+log+"\"\nexit 1\n")
	for _, a := range agents {
		writeTestExecutable(t, filepath.Join(binDir, a), "#!/bin/sh\nexit 0\n")
	}
	baked := filepath.Join(dir, "baked")
	writeFile(t, filepath.Join(baked, skill, "SKILL.md"), "# "+skill+" baked\n")

	body := wireAgentTopologyForTest(t, dir, []byte(readAsset(t, script)))
	wireBakedSkillsForTest(t, dir, baked)
	path := filepath.Join(dir, filepath.Base(script))
	if err := os.WriteFile(path, body, 0o755); err != nil {
		t.Fatal(err)
	}
	home = filepath.Join(dir, "home")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/bash", path)
	cmd.Env = []string{"HOME=" + home, "PATH=" + binDir + ":/usr/bin:/bin"}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run %s: %v\n%s", script, err, out)
	}
	if b, err := os.ReadFile(log); err == nil {
		calls = strings.Split(strings.TrimSpace(string(b)), "\n")
	}
	return home, calls
}

// TestGlobalSkillInitsInstallTheBakedCopy: gh, glab and herdr no longer fetch
// or regenerate their skill at start. Each copies the one baked from its own
// release into every active root — no network, no `--force` rewrite, so a
// second start writes nothing (TestSyncSkillLeavesACurrentCopyUntouched).
func TestGlobalSkillInitsInstallTheBakedCopy(t *testing.T) {
	for _, tc := range []struct{ script, tool, skill, forbidden string }{
		{"init.d/02-gh.sh", "gh", "gh", "skill"},
		{"init.d/60-glab.sh", "glab", "glab", "skills"},
		{"init.d/61-herdr.sh", "herdr", "herdr", "--skill"},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			home, calls := globalSkillInit(t, tc.script, tc.tool, tc.skill, "claude", "pi")
			for _, root := range []string{filepath.Join(home, ".claude", "skills"), filepath.Join(home, ".agents", "skills")} {
				if got := readSkill(t, filepath.Join(root, tc.skill, "SKILL.md")); got != "# "+tc.skill+" baked\n" {
					t.Errorf("%s: SKILL.md = %q, want the baked copy", root, got)
				}
			}
			for _, c := range calls {
				if slices.Contains(strings.Fields(c), tc.forbidden) {
					t.Errorf("%s still regenerates its skill at start: %s %s", tc.script, tc.tool, c)
				}
			}
		})
	}
}

func writeTestExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
