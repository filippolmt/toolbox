package main

import (
	"encoding/json"
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

func runCase(t *testing.T, c guardrailCase, args ...string) (int, string) {
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
	code := run(args, testEnv, strings.NewReader(string(in)), &stderr)
	return code, stderr.String()
}

func checkCases(t *testing.T, cases []guardrailCase, args ...string) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, stderr := runCase(t, c, args...)
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
	checkCases(t, []guardrailCase{
		{name: "tmp source", command: "docker run --rm -v /tmp/fixture:/data alpine ls /data", blocked: true},
		{name: "container home source", command: "docker run -v /home/toolbox/.cache/x:/x alpine", blocked: true},
		{name: "host workspace under /home on a Linux host", command: "docker run -v /home/dev/projects/app/fixture:/data alpine"},
		{name: "tilde source", command: "docker run -v ~/fixture:/data alpine", blocked: true},
		{name: "HOME variable source", command: `docker run -v "$HOME/fixture:/data" alpine`, blocked: true},
		{name: "single-quoted variable is not expanded", command: `docker run -v '$HOME/fixture:/data' alpine`},
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
	}, "--docker")
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
		{name: "docker check off without --docker", command: "docker run -v /tmp/fx:/data alpine"},
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
	})
}

// TestReasonsSayWhatToDoInstead: the reason is all the agent sees, so it names
// the way out, not only the rule.
func TestReasonsSayWhatToDoInstead(t *testing.T) {
	for _, c := range []struct{ command, want string }{
		{"docker run -v /tmp/fx:/data alpine", "$TOOLBOX_HOST_WORKSPACE"},
		{"git commit -am fix", "git add <path>"},
		{"git stash", "git stash push -- <path>"},
	} {
		_, stderr := runCase(t, guardrailCase{command: c.command}, "--docker")
		if !strings.Contains(stderr, c.want) {
			t.Errorf("%q: reason %q does not name %q", c.command, stderr, c.want)
		}
	}
}

// TestLetsThroughWhatItCannotRead: the hook catches mistakes, it is not a
// sandbox, so input it cannot read never blocks a tool call.
func TestLetsThroughWhatItCannotRead(t *testing.T) {
	var stderr strings.Builder
	if code := run(nil, testEnv, strings.NewReader("not json"), &stderr); code != 0 {
		t.Fatalf("exit %d on unreadable input, want 0 (stderr %q)", code, stderr.String())
	}
}
