package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// guardrailCase is one Bash command handed to the hook the way Claude Code
// hands it: the PreToolUse JSON on stdin, the session's cwd in it.
type guardrailCase struct {
	name    string
	command string
	cwd     string
	blocked bool
}

// The container identity is fixed: HOME is the image's /home/toolbox and the
// host workspace is a Linux-style /home path, the layout where a bare "/home/"
// prefix would be the wrong test.
var testEnv = map[string]string{"HOME": "/home/toolbox", "TOOLBOX_HOST_WORKSPACE": "/home/dev/projects"}

// withDockerSocket points the docker check at a socket path that exists, as
// it does in a Shell with the docker-sock mount, or (present false) at one
// that does not.
func withDockerSocket(t *testing.T, present bool) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "docker.sock")
	if present {
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := dockerSocket
	dockerSocket = p
	t.Cleanup(func() { dockerSocket = old })
}

func runCase(t *testing.T, c guardrailCase) (int, string) {
	t.Helper()
	cwd := c.cwd
	if cwd == "" {
		cwd = "/home/dev/projects/app"
	}
	in, err := json.Marshal(map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Bash",
		"cwd":             cwd,
		"tool_input":      map[string]string{"command": c.command},
	})
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	code := run(testEnv, strings.NewReader(string(in)), &stderr)
	return code, stderr.String()
}

func checkCases(t *testing.T, cases []guardrailCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, stderr := runCase(t, c)
			want := 0
			if c.blocked {
				want = 2
			}
			if code != want {
				t.Fatalf("%q: exit %d, want %d (stderr %q)", c.command, code, want, stderr)
			}
			if c.blocked && strings.TrimSpace(stderr) == "" {
				t.Errorf("%q: blocked with no reason on stderr", c.command)
			}
		})
	}
}

// TestBlocksContainerLocalDockerMounts: the Docker daemon is the host's, so it
// resolves a bind source on the host. A container-local source arrives empty
// with no error; a host-workspace source is the correct mount.
func TestBlocksContainerLocalDockerMounts(t *testing.T) {
	withDockerSocket(t, true)
	checkCases(t, []guardrailCase{
		{name: "tmp source", command: "docker run --rm -v /tmp/fixture:/data alpine ls /data", blocked: true},
		{name: "container home source", command: "docker run -v /home/toolbox/.cache/x:/x alpine", blocked: true},
		{name: "host workspace under /home on a Linux host", command: "docker run -v /home/dev/projects/app/fixture:/data alpine"},
		{name: "tilde source", command: "docker run -v ~/fixture:/data alpine", blocked: true},
		{name: "HOME variable source", command: `docker run -v "$HOME/fixture:/data" alpine`, blocked: true},
		{name: "single-quoted variable is not expanded", command: `docker run -v '$HOME/fixture:/data' alpine`},
		{name: "escaped variable is not expanded", command: `docker run -v \$HOME/fixture:/data alpine`},
		{name: "escaped variable inside double quotes", command: `docker run -v "\$HOME/fixture:/data" alpine`},
		{name: "variable assigned earlier in the command", command: `D=/tmp/fx; docker run -v "${D}:/data" alpine`, blocked: true},
		{name: "host workspace variable", command: `docker run -v "$TOOLBOX_HOST_WORKSPACE/app/fx:/data" alpine`},
		{name: "workspace alias mount", command: "docker run -v /workspace/app/fx:/data alpine", blocked: true},
		{name: "relative source from the workspace alias", command: "docker run -v ./fx:/data alpine", cwd: "/workspace/app", blocked: true},
		{name: "pwd source from the workspace alias", command: "docker run -v $(pwd)/fx:/data alpine", cwd: "/workspace/app", blocked: true},
		{name: "backtick pwd source from the workspace alias", command: "docker run -v `pwd`/fx:/data alpine", cwd: "/workspace/app", blocked: true},
		{name: "relative source from the host workspace path", command: "docker run -v ./fx:/data alpine"},
		{name: "PWD source from the host workspace path", command: `docker run -v "$PWD/fx:/data" alpine`},
		{name: "named volume", command: "docker run -v cache:/data alpine"},
		{name: "volume flag with equals", command: "docker run --volume=/tmp/fx:/data alpine", blocked: true},
		{name: "attached short volume flag", command: "docker run -v/tmp/fx:/data alpine", blocked: true},
		{name: "mount flag source", command: "docker run --mount type=bind,source=/tmp/fx,target=/data alpine", blocked: true},
		{name: "mount flag with equals and src", command: "docker run --mount=type=bind,src=/tmp/fx,dst=/data alpine", blocked: true},
		{name: "mount flag host workspace", command: "docker run --mount type=bind,source=/home/dev/projects/app,target=/data alpine"},
		{name: "container run", command: "docker container run -v /tmp/fx:/data alpine", blocked: true},
		{name: "compose run after another command", command: "cd /tmp && docker compose run -v /tmp/fx:/data app", blocked: true},
		{name: "compose down removing volumes", command: "docker compose down -v --remove-orphans"},
		{name: "unparsable command", command: `docker run -v "/tmp/unterminated`},
		{name: "behind sudo", command: "sudo -E docker run -v /tmp/fx:/data alpine", blocked: true},
		{name: "line continuation", command: "docker run --rm \\\n  -v /tmp/fx:/data \\\n  alpine", blocked: true},
		{name: "anonymous volume", command: "docker run -v /tmp alpine"},
		{name: "mount of a named volume", command: "docker run --mount source=cache,target=/tmp alpine"},
		{name: "standalone docker-compose", command: "docker-compose run -v /tmp/fx:/data app", blocked: true},
		{name: "relative source after cd into a container path", command: "cd /tmp && docker run -v ./fx:/data alpine", blocked: true},
		{name: "relative source after cd within the host workspace", command: "cd /home/dev/projects/app/sub && docker run -v ./fx:/data alpine"},
		{name: "exported variable", command: `export D=/tmp/fx; docker run -v "$D:/data" alpine`, blocked: true},
		{name: "declared variable", command: `declare -x D=/tmp/fx && docker run -v "$D:/data" alpine`, blocked: true},
		{name: "behind timeout", command: "timeout -s KILL 30s docker run -v /tmp/fx:/data alpine", blocked: true},
		{name: "behind nice and nohup", command: "nice -n 5 nohup docker run -v /tmp/fx:/data alpine", blocked: true},
		{name: "absolute docker path", command: "/usr/local/bin/docker run -v /tmp/fx:/data alpine", blocked: true},
		{name: "-v of the container command after the image", command: "docker run --rm alpine grep -v /tmp/x f"},
		{name: "value options before the mount", command: "docker run -e A=1 --name n -it -v /tmp/fx:/data alpine", blocked: true},
		{name: "docker global options", command: "docker -H unix:///var/run/docker.sock --context default run -v /tmp/fx:/data alpine", blocked: true},
		{name: "compose file option", command: "docker compose -f c.yml run --rm -v /tmp/fx:/data app", blocked: true},
		{name: "-v of the service command after the service", command: "docker compose run app grep -v /tmp/x f"},
		{name: "image named after a flag value", command: "docker run --network host alpine ls -v /tmp"},
		{name: "global option missing its value", command: "docker -H"},
		{name: "run option missing its value", command: "docker run -v"},
		{name: "behind env", command: "env -u DOCKER_CONTEXT FOO=1 docker run -v /tmp/fx:/data alpine", blocked: true},
		{name: "behind command and exec", command: "exec command docker run -v /tmp/fx:/data alpine", blocked: true},
	})
}

// TestDockerCheckFollowsDockerAccess: Docker access crosses the Containment
// Boundary and is off unless the Shell mounts the socket or names a daemon, so
// the check runs only while the agent can reach one.
func TestDockerCheckFollowsDockerAccess(t *testing.T) {
	mount := guardrailCase{command: "docker run -v /tmp/fx:/data alpine"}
	withDockerSocket(t, false)
	if code, _ := runCase(t, mount); code != 0 {
		t.Errorf("no socket, no DOCKER_HOST: exit %d, want 0", code)
	}
	testEnv["DOCKER_HOST"] = "tcp://127.0.0.1:2375"
	t.Cleanup(func() { delete(testEnv, "DOCKER_HOST") })
	if code, _ := runCase(t, mount); code != 2 {
		t.Errorf("DOCKER_HOST set: exit %d, want 2", code)
	}
}

// TestBlocksCommitAll: -a stages every tracked change, the agent's and
// whatever else is dirty in the tree, so the agent commits only what it staged
// by path.
func TestBlocksCommitAll(t *testing.T) {
	checkCases(t, []guardrailCase{
		{name: "short flag", command: `git commit -a -m "fix"`, blocked: true},
		{name: "long flag", command: "git commit --all", blocked: true},
		{name: "combined with message", command: `git commit -am "fix"`, blocked: true},
		{name: "after global options", command: "git -C sub -c core.quotePath=false commit -a", blocked: true},
		{name: "chained after add", command: "git add x.go && git commit -a", blocked: true},
		{name: "a inside the message value", command: `git commit -m "add -a support"`},
		{name: "a attached as the message", command: "git commit -ma"},
		{name: "message value that looks like the flag", command: "git commit -m -a"},
		{name: "amend", command: "git commit --amend --no-edit"},
		{name: "explicit staging", command: "git add -A && git commit -m fix"},
		{name: "behind sudo with a user", command: "sudo -u dev git commit -a", blocked: true},
		{name: "after --git-dir", command: "git --git-dir .git commit -a", blocked: true},
		{name: "after --work-tree", command: "git --work-tree w --namespace n commit --all", blocked: true},
		{name: "absolute git path", command: "/usr/bin/git commit -a", blocked: true},
		{name: "inside bash -c", command: `bash -c "git add . && git commit -a -m wip"`, blocked: true},
		{name: "inside sh -lc", command: `sh -lc 'git commit --all'`, blocked: true},
		{name: "inside eval", command: `eval "git commit -a"`, blocked: true},
		{name: "inside a command substitution", command: "out=$(git commit -a -m x)", blocked: true},
		{name: "inside backticks", command: "echo `git commit --all`", blocked: true},
		{name: "message from a heredoc with an unbalanced parenthesis", command: "git commit -a -m \"$(cat <<'EOF'\n(wip\nEOF\n)\"", blocked: true},
		{name: "here-string is not a heredoc", command: "grep -q x <<<word\ngit commit -a", blocked: true},
		{name: "heredoc body is text, not commands", command: "git commit -F - <<'EOF'\nfix: drop git stash usage\ngit commit -a was wrong\nEOF"},
		{name: "bash -c with a clean script", command: `bash -c "git commit -m fix"`},
		{name: "bash running a script file", command: "bash ./commit-all.sh"},
		{name: "bash with valued options before -c", command: `bash -o pipefail -c 'git commit -a'`, blocked: true},
		{name: "bash with -euo pipefail -c", command: `bash -euo pipefail -c "git commit -a"`, blocked: true},
		{name: "inside an if", command: "if true; then git commit -am x; fi", blocked: true},
		{name: "inside a loop", command: "for f in a b; do git commit --all; done", blocked: true},
		{name: "inside a group", command: "{ git commit -a; }", blocked: true},
		{name: "negated", command: "! git commit -a", blocked: true},
		{name: "inside a function defined with the keyword", command: "function f { git stash; }; f", blocked: true},
		{name: "env split string", command: `env -S 'git commit -a'`, blocked: true},
		{name: "behind env assignment", command: "env GIT_EDITOR=true git commit --all", blocked: true},
		{name: "ambiguous value abbreviation takes one word", command: "git commit --re x -a", blocked: true},
	})
}

// TestBlocksImplicitStash: a stash with no pathspec sweeps up every change in
// the tree, including work that is not the agent's.
func TestBlocksImplicitStash(t *testing.T) {
	checkCases(t, []guardrailCase{
		{name: "bare", command: "git stash", blocked: true},
		{name: "bare with message", command: `git stash -m "wip"`, blocked: true},
		{name: "push without pathspec", command: `git stash push -m "wip"`, blocked: true},
		{name: "save", command: `git stash save "wip"`, blocked: true},
		{name: "chained", command: "git stash && git pull", blocked: true},
		{name: "push with separated pathspec", command: `git stash push -m "wip" -- a.go`},
		{name: "push with pathspec", command: "git stash push a.go b.go"},
		{name: "list", command: "git stash list"},
		{name: "pop", command: "git stash pop stash@{1}"},
		{name: "only what is staged", command: "git stash push --staged"},
		{name: "only what is staged, short", command: "git stash -S"},
		{name: "only what is staged, abbreviated", command: "git stash push --sta"},
		{name: "separator with nothing after", command: "git stash push --", blocked: true},
		{name: "whole tree after separator", command: "git stash push -- .", blocked: true},
		{name: "whole tree as pathspec", command: "git stash push :/", blocked: true},
		{name: "pathspec from a file", command: "git stash push --pathspec-from-file=list.txt"},
		{name: "message in a short cluster", command: "git stash push -um wip", blocked: true},
		{name: "keep index cluster with pathspec", command: "git stash push -ku -- a.go"},
		{name: "output redirected", command: "git stash >/dev/null", blocked: true},
		{name: "stderr merged into stdout", command: "git stash 2>&1", blocked: true},
		{name: "redirection target as its own word", command: "git stash > out.txt", blocked: true},
		{name: "both streams redirected", command: "git stash &>/dev/null", blocked: true},
		{name: "list redirected", command: "git stash list >/dev/null"},
		{name: "pathspec variable set empty in the line", command: "P=; git stash push $P", blocked: true},
	})
}

// TestBlocksStashApplyWithoutReference: with no reference, pop and apply take
// stash@{0}, which can be another session's; after an agent's stash that saved
// nothing, that is someone else's work landing on the tree.
func TestBlocksStashApplyWithoutReference(t *testing.T) {
	checkCases(t, []guardrailCase{
		{name: "bare pop", command: "git stash pop", blocked: true},
		{name: "bare apply", command: "git stash apply", blocked: true},
		{name: "options are not a reference", command: "git stash pop --index -q", blocked: true},
		{name: "apply with quiet only", command: "git stash apply --quiet", blocked: true},
		{name: "pop with a reference", command: "git stash pop stash@{1}"},
		{name: "apply with a bare index", command: "git stash apply 1"},
		{name: "reference after an option", command: "git stash pop --index stash@{2}"},
		{name: "after git -C", command: "git -C /home/dev/projects/app stash pop", blocked: true},
		{name: "behind sudo", command: "sudo git stash pop", blocked: true},
		{name: "inside bash -c", command: `bash -c "git stash apply"`, blocked: true},
		{name: "inside command substitution", command: "echo $(git stash pop)", blocked: true},
		{name: "chained after a pathspec stash", command: "git stash push -- a.go && git pull && git stash pop", blocked: true},
		{name: "output redirected", command: "git stash pop > out.txt", blocked: true},
		{name: "reference variable set empty in the line", command: "R=; git stash pop $R", blocked: true},
		{name: "quoted empty reference reaches git as a word", command: `git stash pop ""`},
		{name: "drop is not apply", command: "git stash drop"},
	})
}

// TestBlocksResetHard: a hard reset throws away every uncommitted change in the
// tree, other sessions' included, and git keeps no copy of what it dropped.
func TestBlocksResetHard(t *testing.T) {
	checkCases(t, []guardrailCase{
		{name: "bare", command: "git reset --hard", blocked: true},
		{name: "to a target", command: "git reset --hard origin/main", blocked: true},
		{name: "keep refuses to lose changes", command: "git reset --keep origin/main"},
		{name: "soft", command: "git reset --soft HEAD~1"},
		{name: "mixed", command: "git reset"},
		{name: "keep without a target", command: "git reset --keep"},
		{name: "abbreviated hard", command: "git reset --har", blocked: true},
		{name: "hard after the target", command: "git reset HEAD~1 --hard", blocked: true},
		{name: "unstage a path", command: "git reset -- a.go"},
	})
}

// TestBlocksTreeWideCheckout: checking out the whole tree, or forcing a branch
// switch, overwrites every uncommitted change; naming the files is the agent
// choosing what to throw away.
func TestBlocksTreeWideCheckout(t *testing.T) {
	checkCases(t, []guardrailCase{
		{name: "dot", command: "git checkout .", blocked: true},
		{name: "dot after separator", command: "git checkout -- .", blocked: true},
		{name: "dot from a tree-ish", command: "git checkout HEAD -- .", blocked: true},
		{name: "top of the tree", command: "git checkout -- :/", blocked: true},
		{name: "force", command: "git checkout -f main", blocked: true},
		{name: "long force", command: "git checkout --force main", blocked: true},
		{name: "named file", command: "git checkout -- a.go"},
		{name: "named directory", command: "git checkout -- src/"},
		{name: "branch switch", command: "git checkout main"},
		{name: "new branch", command: "git checkout -b feat/x"},
		{name: "file named like an option", command: "git checkout -- -f"},
		{name: "forced checkout of named files", command: "git checkout -f -- a.go"},
		{name: "forced checkout of named files from a tree-ish", command: "git checkout -f HEAD -- a.go"},
		{name: "forced checkout of the whole tree", command: "git checkout -f -- .", blocked: true},
		{name: "abbreviated force", command: "git checkout --forc main", blocked: true},
		{name: "every file by glob", command: "git checkout -- '*'", blocked: true},
		{name: "top magic", command: "git checkout -- ':(top)'", blocked: true},
		{name: "parent directory", command: "git checkout -- ..", blocked: true},
		{name: "dot with trailing slash", command: "git checkout ./", blocked: true},
		{name: "branch name taken by -b", command: "git checkout -b f main"},
		{name: "top without separator", command: "git checkout :/", blocked: true},
		{name: "working directory by absolute path", command: `git checkout -- "$PWD"`, blocked: true},
		{name: "parent of the working directory by absolute path", command: "git checkout -- /home/dev/projects", blocked: true},
		{name: "subdirectory by absolute path", command: "git checkout -- /home/dev/projects/app/src"},
		{name: "path that climbs back", command: "git checkout -- src/..", blocked: true},
		{name: "doubled slash", command: "git checkout -- .//", blocked: true},
		{name: "top among combined magic", command: "git checkout -- ':(top,icase).'", blocked: true},
		{name: "magic on a named directory", command: "git checkout -- ':(icase)src'"},
		{name: "directory given to git -C", command: "git -C /home/dev/projects/app/sub checkout -- /home/dev/projects/app/sub", blocked: true},
		{name: "relative directory given to git -C", command: "git -C sub checkout -- /home/dev/projects/app/sub", blocked: true},
	})
}

// TestBlocksTreeWideRestore: restoring the whole worktree is the same discard
// as checking it out; restoring only the index keeps the changes.
func TestBlocksTreeWideRestore(t *testing.T) {
	checkCases(t, []guardrailCase{
		{name: "dot", command: "git restore .", blocked: true},
		{name: "top of the tree", command: "git restore :/", blocked: true},
		{name: "from a source", command: "git restore -s HEAD~1 .", blocked: true},
		{name: "index and worktree", command: "git restore --staged --worktree .", blocked: true},
		{name: "index and worktree, short", command: "git restore -SW .", blocked: true},
		{name: "worktree, short", command: "git restore -W .", blocked: true},
		{name: "worktree, long", command: "git restore --worktree .", blocked: true},
		{name: "abbreviated worktree", command: "git restore --staged --work .", blocked: true},
		{name: "top glob", command: "git restore ':/*'", blocked: true},
		{name: "index only", command: "git restore --staged ."},
		{name: "abbreviated staged", command: "git restore --sta ."},
		{name: "index only, short", command: "git restore -S ."},
		{name: "named file", command: "git restore a.go"},
	})
}

// TestBlocksTreeWideClean: a forced clean with no pathspec deletes every
// untracked file, other sessions' new files included, and git never had them.
func TestBlocksTreeWideClean(t *testing.T) {
	checkCases(t, []guardrailCase{
		{name: "force", command: "git clean -f", blocked: true},
		{name: "force with directories", command: "git clean -fd", blocked: true},
		{name: "force with ignored files", command: "git clean -xdf", blocked: true},
		{name: "long force", command: "git clean --force -d", blocked: true},
		{name: "force on the whole tree", command: "git clean -f -- .", blocked: true},
		{name: "force with an exclude", command: "git clean -f -e keep.txt", blocked: true},
		{name: "named path beside the whole tree", command: "git clean -fd build/ .", blocked: true},
		{name: "named path beside the top", command: "git clean -f -- build/ :/", blocked: true},
		{name: "abbreviated force", command: "git clean --forc -d", blocked: true},
		{name: "dry run", command: "git clean -n"},
		{name: "forced dry run", command: "git clean -fn"},
		{name: "long dry run", command: "git clean -f --dry-run"},
		{name: "named directory", command: "git clean -f -- build/"},
		{name: "named path without separator", command: "git clean -fd build/"},
	})
}

// TestBlocksForcedSwitch: a forced switch overwrites the uncommitted changes a
// plain switch would refuse to touch.
func TestBlocksForcedSwitch(t *testing.T) {
	checkCases(t, []guardrailCase{
		{name: "force", command: "git switch -f main", blocked: true},
		{name: "long force", command: "git switch --force main", blocked: true},
		{name: "discard changes", command: "git switch --discard-changes main", blocked: true},
		{name: "abbreviated discard changes", command: "git switch --discard main", blocked: true},
		{name: "plain", command: "git switch main"},
		{name: "create", command: "git switch -c feat/x"},
		{name: "create a branch named f", command: "git switch -cf main"},
		{name: "force after the branch", command: "git switch -c fix -f", blocked: true},
	})
}

// TestBlocksTreeWideDiscardsThroughWrappers: the discard rules see the command
// the shell runs, as the other git rules do.
func TestBlocksTreeWideDiscardsThroughWrappers(t *testing.T) {
	checkCases(t, []guardrailCase{
		{name: "git -C", command: "git -C /home/dev/projects/app reset --hard", blocked: true},
		{name: "behind sudo", command: "sudo git clean -fdx", blocked: true},
		{name: "inside bash -c", command: `bash -c "git checkout ."`, blocked: true},
		{name: "inside command substitution", command: "echo $(git restore .)", blocked: true},
		{name: "chained", command: "git fetch && git reset --hard origin/main", blocked: true},
		{name: "output redirected", command: "git switch -f main >/dev/null 2>&1", blocked: true},
	})
}

// TestGitOptionTablesNameKnownOptions: a value or optional-value entry that
// names no option of its table is a typo, and the option it meant would be read
// as taking no value, shifting every word after it.
func TestGitOptionTablesNameKnownOptions(t *testing.T) {
	tables := map[string]gitOptions{
		"commit": commitOptions, "stash push": stashPushOptions, "stash apply": stashApplyOptions,
		"reset": resetOptions, "checkout": checkoutOptions, "restore": restoreOptions,
		"clean": cleanOptions, "switch": switchOptions,
	}
	for sub, o := range tables {
		known := set(o.long...)
		for _, n := range o.short {
			known[n] = true
		}
		for n := range o.value {
			if !known[n] {
				t.Errorf("%s: value option %q is neither a long name nor a short option's", sub, n)
			}
		}
		for n := range o.optional {
			if !known[n] {
				t.Errorf("%s: optional-value option %q is neither a long name nor a short option's", sub, n)
			}
			if o.value[n] {
				t.Errorf("%s: %q is both a value and an optional-value option", sub, n)
			}
		}
	}
}

// TestReasonsSayWhatToDoInstead: the reason is all the agent sees, so it names
// the way out, not only the rule.
func TestReasonsSayWhatToDoInstead(t *testing.T) {
	withDockerSocket(t, true)
	for _, c := range []struct{ command, want string }{
		{"docker run -v /tmp/fx:/data alpine", "$TOOLBOX_HOST_WORKSPACE"},
		{"git commit -am fix", "git add <path>"},
		{"git stash", "git stash push -- <path>"},
		{"git stash pop", "git stash list"},
		{"git stash apply", "git stash pop stash@{n}"},
		{"git reset --hard", "git reset --keep <target>"},
		{"git checkout .", "git checkout -- <path>"},
		{"git restore .", "git restore -- <path>"},
		{"git clean -fd", "git clean -n"},
		{"git switch -f main", "git stash push -- <path>"},
	} {
		_, stderr := runCase(t, guardrailCase{command: c.command})
		if !strings.Contains(stderr, c.want) {
			t.Errorf("%q: reason %q does not name %q", c.command, stderr, c.want)
		}
	}
}

// TestLetsThroughWhatItCannotRead: the hook catches mistakes, it is not a
// sandbox, so input it cannot read never blocks a tool call.
func TestLetsThroughWhatItCannotRead(t *testing.T) {
	var stderr strings.Builder
	if code := run(testEnv, strings.NewReader("not json"), &stderr); code != 0 {
		t.Fatalf("exit %d on unreadable input, want 0 (stderr %q)", code, stderr.String())
	}
}
