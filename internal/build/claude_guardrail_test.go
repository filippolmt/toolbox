package build

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const claudeGuardrailAsset = "bin/claude-guardrail"

// guardrailCase is one Bash tool call: the command Claude Code would run, the
// directory it runs in, and whether the guard must block it.
type guardrailCase struct {
	command string
	cwd     string
	block   bool
}

// runGuardrail feeds one PreToolUse payload to the guard the way Claude Code
// does, on stdin, and returns its exit status and stderr.
func runGuardrail(t *testing.T, guard string, c guardrailCase, env ...string) (int, string) {
	t.Helper()
	requireTools(t, "jq")
	dir := t.TempDir()
	script := filepath.Join(dir, "claude-guardrail")
	writeTestExecutable(t, script, readAsset(t, claudeGuardrailAsset))
	payload, err := json.Marshal(map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Bash",
		"cwd":             c.cwd,
		"tool_input":      map[string]string{"command": c.command},
	})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/bash", script, guard)
	cmd.Stdin = strings.NewReader(string(payload))
	cmd.Env = append([]string{"PATH=/usr/bin:/bin"}, env...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	err = cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), stderr.String()
	}
	if err != nil {
		t.Fatalf("run guard %s: %v", guard, err)
	}
	return 0, stderr.String()
}

// checkGuardrail runs every case: a blocked call exits 2, Claude Code's
// blocking status, with a one-line reason; an allowed one exits 0, silent.
func checkGuardrail(t *testing.T, guard string, cases []guardrailCase, env ...string) {
	t.Helper()
	for _, c := range cases {
		code, stderr := runGuardrail(t, guard, c, env...)
		switch {
		case c.block && code != 2:
			t.Errorf("%s: %q exited %d, want 2 (blocked)\n%s", guard, c.command, code, stderr)
		case c.block && strings.Count(strings.TrimSpace(stderr), "\n") != 0:
			t.Errorf("%s: %q reason is not one line:\n%s", guard, c.command, stderr)
		case c.block && !strings.HasPrefix(stderr, "toolbox: "):
			t.Errorf("%s: %q reason does not name toolbox: %q", guard, c.command, stderr)
		case !c.block && (code != 0 || stderr != ""):
			t.Errorf("%s: %q exited %d, want 0 (allowed)\n%s", guard, c.command, code, stderr)
		}
	}
}

// TestClaudeGuardrailBlocksCommitAll: `git commit -a` commits every tracked
// change in the worktree, the developer's included; the agent commits only
// the paths it staged.
func TestClaudeGuardrailBlocksCommitAll(t *testing.T) {
	checkGuardrail(t, "git-commit-all", []guardrailCase{
		{command: `git commit -a -m "fix"`, block: true},
		{command: `git commit --all -m fix`, block: true},
		{command: `git commit -am "fix"`, block: true},
		{command: `git commit -sam fix`, block: true},
		{command: `git -C /src commit -a`, block: true},
		{command: `git -c user.name=x commit --all`, block: true},
		{command: `GIT_AUTHOR_NAME=x git commit -a`, block: true},
		{command: "git add a.go && git commit -am wip", block: true},
		{command: "make fmt; /usr/bin/git commit -a", block: true},
		{command: `git commit -m "fix"`, block: false},
		{command: `git commit -m "-a is a flag"`, block: false},
		{command: `git commit -ma`, block: false},
		{command: `git commit -m fix -- a.go`, block: false},
		{command: `git commit --amend --no-edit`, block: false},
		{command: `git commit --allow-empty -m x`, block: false},
		{command: `echo "git commit -a"`, block: false},
		{command: `git log -a`, block: false},
		{command: `ls -a`, block: false},
	})
}

// TestClaudeGuardrailBlocksImplicitStash: a stash with no pathspec takes every
// change in the worktree, the developer's included, off the disk.
func TestClaudeGuardrailBlocksImplicitStash(t *testing.T) {
	checkGuardrail(t, "git-stash", []guardrailCase{
		{command: `git stash`, block: true},
		{command: `git stash push`, block: true},
		{command: `git stash push -m "wip"`, block: true},
		{command: `git stash -u`, block: true},
		{command: `git stash push -um wip`, block: true},
		{command: `git stash save "wip"`, block: true},
		{command: `git stash push --`, block: true},
		{command: `git stash 2>&1`, block: true},
		{command: `git stash >/dev/null && git pull`, block: true},
		{command: `git -C /src stash`, block: true},
		{command: `git stash push -- a.go`, block: false},
		{command: `git stash push -m wip -- a.go b.go`, block: false},
		{command: `git stash push -m wip a.go`, block: false},
		{command: `git stash -- a.go`, block: false},
		{command: `git stash push --pathspec-from-file=paths.txt`, block: false},
		{command: `git stash list`, block: false},
		{command: `git stash pop`, block: false},
		{command: `git stash show -p stash@{0}`, block: false},
		{command: `git status # then git stash`, block: false},
		{command: "if true; then git stash; fi", block: true},
		{command: "{ git stash; }", block: true},
		{command: "timeout 30 git stash", block: true},
		{command: "env -u FOO git stash", block: true},
		{command: "sudo -u dev git stash", block: true},
		{command: "cat > notes.txt <<EOF\ngit stash\nEOF\ngit status", block: false},
		{command: "cat <<-'EOF' > x\n\tgit stash\n\tEOF\ngit stash", block: true},
	})
}

// TestClaudeGuardrailBlocksContainerLocalDockerMounts: under
// Docker-outside-of-Docker the host daemon resolves a bind source, so a path
// that exists only in the container (its home, /tmp, the /workspace mount
// point) mounts an empty or missing directory on the host. The workspace's
// host-path mirror, and paths the container does not own, pass.
func TestClaudeGuardrailBlocksContainerLocalDockerMounts(t *testing.T) {
	const ws = "/Users/dev/projects"
	app := ws + "/app"
	checkGuardrail(t, "docker-mount", []guardrailCase{
		{command: `docker run -v /tmp/x:/x alpine`, block: true},
		{command: `docker run --rm -v "$HOME/.cache:/c" alpine`, block: true},
		{command: `docker run -v ~/data:/d alpine`, block: true},
		{command: `docker run --volume=/workspace/app:/src alpine`, block: true},
		{command: `docker run --volume /var/tmp/a:/a alpine`, block: true},
		{command: `docker run -v/tmp/x:/x alpine`, block: true},
		{command: `docker create --mount type=bind,source=/tmp/x,target=/x alpine`, block: true},
		{command: `docker container run --mount=type=bind,src=/home/toolbox/a,dst=/a alpine`, block: true},
		{command: `docker -H unix:///var/run/docker.sock run -v /tmp/x:/x alpine`, block: true},
		{command: `docker compose -f c.yml run -v /tmp/x:/x app`, block: true},
		{command: `docker-compose run --volume /tmp/x:/x app`, block: true},
		{command: `docker run -v .:/src alpine`, cwd: "/workspace/app", block: true},
		{command: `docker run -v "$PWD":/src alpine`, cwd: "/tmp/scratch", block: true},
		{command: `docker run -v "$(pwd)/out:/out" alpine`, cwd: "/tmp/scratch", block: true},
		{command: `docker run -v "$TOOLBOX_HOST_WORKSPACE/x:/x" alpine`, block: false},
		{command: `docker run -v "${TOOLBOX_HOST_WORKSPACE}/x:/x:ro" alpine`, block: false},
		{command: `docker run -v ` + app + `:/src alpine`, block: false},
		{command: `docker run -v .:/src alpine`, cwd: app, block: false},
		{command: `docker run -v "$PWD/sub:/src" alpine`, cwd: app, block: false},
		{command: `docker run -v /var/run/docker.sock:/var/run/docker.sock alpine`, block: false},
		{command: `docker run -v named-volume:/data alpine`, block: false},
		{command: `docker run -v /data alpine`, block: false},
		{command: `docker run --mount type=volume,source=cache,target=/c alpine`, block: false},
		{command: `docker run -v "$SOME_DIR:/x" alpine`, block: false},
		{command: `docker volume ls`, block: false},
		{command: `docker exec -it box ls /tmp`, block: false},
		{command: `echo docker run -v /tmp/x:/x`, block: false},
		{command: "for d in a b; do docker run -v /tmp/$d:/x alpine; done", block: false},
		{command: "for d in a b; do docker run -v /tmp/x:/x alpine; done", block: true},
	}, "HOME=/home/toolbox", "TOOLBOX_HOST_WORKSPACE="+ws)
	// No HOME in the hook's environment: still a clean verdict, never a crash.
	checkGuardrail(t, "docker-mount", []guardrailCase{
		{command: `docker run -v "$HOME/x:/x" alpine`, block: false},
		{command: `docker run -v /tmp/x:/x alpine`, block: true},
	})
}

// TestClaudeGuardrailDockerReasonNamesTheHostWorkspace: the block says where
// a mount belongs, and for a /workspace path, the host path it maps to.
func TestClaudeGuardrailDockerReasonNamesTheHostWorkspace(t *testing.T) {
	_, reason := runGuardrail(t, "docker-mount", guardrailCase{command: `docker run -v /workspace/app:/src alpine`},
		"HOME=/home/toolbox", "TOOLBOX_HOST_WORKSPACE=/Users/dev/projects")
	if !strings.Contains(reason, "$TOOLBOX_HOST_WORKSPACE/app") {
		t.Errorf("reason does not point at $TOOLBOX_HOST_WORKSPACE/app: %q", reason)
	}
}

const claudeGuardrailsInit = "init.d/45-claude-guardrails.sh"

// runGuardrailsInit runs the init.d member once against home, with a fake
// claude on PATH, and returns its output.
func runGuardrailsInit(t *testing.T, dir, home string) string {
	t.Helper()
	requireTools(t, "jq", "flock")
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestExecutable(t, filepath.Join(binDir, "claude"), "#!/bin/sh\nexit 0\n")
	script := filepath.Join(dir, "45-claude-guardrails.sh")
	if err := os.WriteFile(script, wireAgentTopologyForTest(t, dir, []byte(readAsset(t, claudeGuardrailsInit))), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/bash", script)
	cmd.Env = []string{"HOME=" + home, "PATH=" + binDir + ":/usr/bin:/bin"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run %s: %v\n%s", claudeGuardrailsInit, err, out)
	}
	return string(out)
}

// TestClaudeGuardrailsInitWiresHooksIdempotently: every start merges the
// guards as one PreToolUse group on Bash. A second start changes no
// byte, an earlier image's guard is replaced rather than stacked, and the
// developer's own hooks keep their place.
func TestClaudeGuardrailsInitWiresHooksIdempotently(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	settings := filepath.Join(home, ".claude", "settings.json")
	writeFile(t, settings, `{
  "model": "opus",
  "hooks": {
    "PreToolUse": [
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "my-own-guard"}]},
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "/usr/local/lib/toolbox/claude-guardrail retired-guard"}]}
    ]
  }
}`)

	runGuardrailsInit(t, dir, home)
	first, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	runGuardrailsInit(t, dir, home)
	second, _ := os.ReadFile(settings)
	if string(first) != string(second) {
		t.Errorf("second start rewrote settings.json:\n--- first\n%s\n--- second\n%s", first, second)
	}

	want := []string{
		"my-own-guard",
		"/usr/local/lib/toolbox/claude-guardrail docker-mount",
		"/usr/local/lib/toolbox/claude-guardrail git-commit-all",
		"/usr/local/lib/toolbox/claude-guardrail git-stash",
	}
	hooks := readHooks(t, settings)
	if got := commands(hooks, "PreToolUse"); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("PreToolUse commands = %q, want %q", got, want)
	}
	for _, g := range hooks["PreToolUse"] {
		if g.Matcher == nil || *g.Matcher != "Bash" {
			t.Errorf("PreToolUse group %q is not scoped to Bash", commandsOf(g))
		}
	}
	if !strings.Contains(string(second), `"model": "opus"`) {
		t.Errorf("settings.json lost unrelated keys:\n%s", second)
	}
}
