package build

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// refreshFixture runs bin/toolbox-refresh against a home with Claude and pi
// state mounted, fake agents, fake per-repo installers that log "<cwd>|<argv>",
// and one baked global skill (gh).
type refreshFixture struct {
	dir, home, script, log string
	env                    []string
}

func newRefreshFixture(t *testing.T) refreshFixture {
	t.Helper()
	for _, tool := range []string{"git", "jq", "flock"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH", tool)
		}
	}
	dir := t.TempDir()
	f := refreshFixture{dir: dir, home: filepath.Join(dir, "home"), log: filepath.Join(dir, "calls")}
	for _, d := range []string{".claude", ".pi"} {
		if err := os.MkdirAll(filepath.Join(f.home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{"claude", "pi"} {
		writeTestExecutable(t, filepath.Join(binDir, a), "#!/bin/sh\nexit 0\n")
	}
	for _, tool := range []string{"graphify", "codegraph", "playwright-cli"} {
		writeTestExecutable(t, filepath.Join(binDir, tool),
			"#!/bin/sh\nprintf '%s|%s\\n' \"$PWD\" \""+tool+" $*\" >> \""+f.log+"\"\n")
	}
	baked := filepath.Join(dir, "baked")
	writeFile(t, filepath.Join(baked, "gh", "SKILL.md"), "# gh baked\n")

	body := wireAgentTopologyForTest(t, dir, []byte(readAsset(t, "bin/toolbox-refresh")))
	wireBakedSkillsForTest(t, dir, baked)
	f.script = filepath.Join(dir, "toolbox-refresh")
	if err := os.WriteFile(f.script, body, 0o755); err != nil {
		t.Fatal(err)
	}
	f.env = []string{
		"HOME=" + f.home,
		"PATH=" + binDir + ":/usr/bin:/bin",
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
	}
	return f
}

// repo creates a git repo under the fixture with the given opt-in markers.
func (f refreshFixture) repo(t *testing.T, markers ...string) string {
	t.Helper()
	root := filepath.Join(f.dir, "repo")
	for _, m := range append([]string{"sub/deeper"}, markers...) {
		if err := os.MkdirAll(filepath.Join(root, m), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("git", "init", "-q", root)
	cmd.Env = f.env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	return root
}

func (f refreshFixture) run(t *testing.T, cwd string, args ...string) (calls []string, out string, err error) {
	t.Helper()
	cmd := exec.Command("/bin/bash", append([]string{f.script}, args...)...)
	cmd.Dir = cwd
	cmd.Env = f.env
	b, err := cmd.CombinedOutput()
	if logged, rerr := os.ReadFile(f.log); rerr == nil {
		calls = strings.Split(strings.TrimSpace(string(logged)), "\n")
	}
	return calls, string(b), err
}

var allOptIns = []string{"graphify-out", ".codegraph", ".claude/skills/playwright-cli"}

// TestToolboxRefreshRunsTheRepoInstallersFromTheRepoRoot: a toolbox opened on a
// parent folder starts with $PWD outside every repo, so the repo is resolved
// from git, never from where the developer happens to stand.
func TestToolboxRefreshRunsTheRepoInstallersFromTheRepoRoot(t *testing.T) {
	f := newRefreshFixture(t)
	root := f.repo(t, allOptIns...)
	calls, out, err := f.run(t, filepath.Join(root, "sub", "deeper"))
	if err != nil {
		t.Fatalf("toolbox-refresh: %v\n%s", err, out)
	}
	for _, want := range []string{
		"graphify install --project --platform claude",
		"graphify install --project --platform pi",
		"graphify hook install",
		"codegraph install --refresh",
		"playwright-cli install --skills claude",
		"playwright-cli install --skills agents",
	} {
		if !slices.Contains(calls, root+"|"+want) {
			t.Errorf("missing %q run from %s; calls:\n%s", want, root, strings.Join(calls, "\n"))
		}
	}
	if got := readSkill(t, filepath.Join(f.home, ".claude", "skills", "gh", "SKILL.md")); got != "# gh baked\n" {
		t.Errorf("global skill not refreshed: %q", got)
	}
}

// TestToolboxRefreshOutsideARepoTouchesOnlyGlobalSkills.
func TestToolboxRefreshOutsideARepoTouchesOnlyGlobalSkills(t *testing.T) {
	f := newRefreshFixture(t)
	outside := filepath.Join(f.dir, "outside")
	if err := os.MkdirAll(filepath.Join(outside, "graphify-out"), 0o755); err != nil {
		t.Fatal(err)
	}
	calls, out, err := f.run(t, outside)
	if err != nil {
		t.Fatalf("toolbox-refresh: %v\n%s", err, out)
	}
	if len(calls) != 0 {
		t.Errorf("per-repo installers ran outside a repo:\n%s", strings.Join(calls, "\n"))
	}
	readSkill(t, filepath.Join(f.home, ".agents", "skills", "gh", "SKILL.md"))
}

// TestToolboxRefreshLeavesARepoThatNeverOptedIn: opt-in stays with the *-init
// aliases; a refresh never adds a tool to a repo.
func TestToolboxRefreshLeavesARepoThatNeverOptedIn(t *testing.T) {
	f := newRefreshFixture(t)
	root := f.repo(t)
	calls, out, err := f.run(t, root)
	if err != nil {
		t.Fatalf("toolbox-refresh: %v\n%s", err, out)
	}
	if len(calls) != 0 {
		t.Errorf("installers ran in a repo that never opted in:\n%s", strings.Join(calls, "\n"))
	}
}

// TestToolboxRefreshNamedToolsOnly: arguments narrow the refresh to the tools
// they name — no global skill, no other installer.
func TestToolboxRefreshNamedToolsOnly(t *testing.T) {
	f := newRefreshFixture(t)
	root := f.repo(t, allOptIns...)
	calls, out, err := f.run(t, root, "codegraph")
	if err != nil {
		t.Fatalf("toolbox-refresh codegraph: %v\n%s", err, out)
	}
	if want := []string{root + "|codegraph install --refresh"}; !slices.Equal(calls, want) {
		t.Errorf("calls = %q, want %q", calls, want)
	}
	if _, err := os.Stat(filepath.Join(f.home, ".claude", "skills", "gh")); !os.IsNotExist(err) {
		t.Error("a global skill was refreshed although only codegraph was named")
	}
}

func TestToolboxRefreshRejectsAnUnknownTool(t *testing.T) {
	f := newRefreshFixture(t)
	_, out, err := f.run(t, f.dir, "nope")
	var exit *exec.ExitError
	if err == nil || !errors.As(err, &exit) || exit.ExitCode() != 2 {
		t.Fatalf("toolbox-refresh nope: err = %v, want exit 2\n%s", err, out)
	}
}

// TestNoInitScriptReadsTheWorkspace: a toolbox is usually opened on a parent
// folder, so the start-time $PWD names no repo, and a start that writes into a
// repo hands the developer a dirty tree. Per-repo installs belong to
// toolbox-refresh alone.
func TestNoInitScriptReadsTheWorkspace(t *testing.T) {
	entries, err := Assets.ReadDir("assets/init.d")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		for i, line := range strings.Split(readAsset(t, "init.d/"+e.Name()), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			if strings.Contains(line, "$PWD") || strings.Contains(line, "${PWD") {
				t.Errorf("init.d/%s:%d reads the workspace: %s", e.Name(), i+1, strings.TrimSpace(line))
			}
		}
	}
}

// TestEveryGraphifySkillDirIsGitignored: toolbox-refresh installs one graphify
// skill per agent platform, under .<platform>/skills/graphify/ in the repo;
// each must be gitignored here, or an opted-in checkout of this repo carries
// an untracked copy.
func TestEveryGraphifySkillDirIsGitignored(t *testing.T) {
	ignore, err := os.ReadFile("../../.gitignore")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(ignore), "\n")
	platforms := regexp.MustCompile(`graphify install --project --platform (\w+)`).FindAllStringSubmatch(readAsset(t, "bin/toolbox-refresh"), -1)
	if len(platforms) == 0 {
		t.Fatal("bin/toolbox-refresh: no graphify install found — the pattern no longer matches the script")
	}
	for _, m := range platforms {
		if dir := "." + m[1] + "/skills/graphify/"; !slices.Contains(lines, dir) {
			t.Errorf(".gitignore has no %q entry for the %s graphify skill", dir, m[1])
		}
	}
}
