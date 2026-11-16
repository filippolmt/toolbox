// Command agent-guardrails is a Claude Code PreToolUse hook for the Bash tool,
// baked into the image at /usr/local/lib/toolbox/agent-guardrails and wired by
// init.d/37-agent-guardrails.sh.
//
// It reads the hook JSON on stdin. Exit 2 blocks the tool call and hands
// stderr back to the agent; exit 0 lets it run. Input it cannot read is let
// through: the hook catches mistakes, it is not a sandbox.
//
// --docker adds the bind-mount check, wired only when the Docker socket is
// mounted.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path"
	"regexp"
	"strings"
)

const (
	commitAll = "Blocked: git commit -a/--all stages every tracked change, including work that is not yours. " +
		"Run `git add <path>` for the files of this commit, then `git commit` without -a."
	implicitStash = "Blocked: a git stash without a pathspec sweeps up every change in the tree, including work " +
		"that is not yours. Name what to stash: `git stash push -- <path>`."
)

func main() {
	env := map[string]string{}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	os.Exit(run(os.Args[1:], env, os.Stdin, os.Stderr))
}

func run(args []string, env map[string]string, stdin io.Reader, stderr io.Writer) int {
	var event struct {
		Cwd       string `json:"cwd"`
		ToolInput struct {
			Command string `json:"command"`
		} `json:"tool_input"`
	}
	if err := json.NewDecoder(stdin).Decode(&event); err != nil {
		return 0
	}
	commands, err := tokenize(event.ToolInput.Command)
	if err != nil {
		return 0
	}
	docker := len(args) > 0 && args[0] == "--docker"

	vars := maps.Clone(env)
	if vars == nil {
		vars = map[string]string{}
	}
	if event.Cwd != "" {
		vars["PWD"] = event.Cwd
	}
	for _, words := range commands {
		argv := expandCommand(words, vars)
		reason := checkCommit(argv)
		if reason == "" {
			reason = checkStash(argv)
		}
		if reason == "" && docker {
			reason = checkDocker(argv, vars)
		}
		if reason != "" {
			fmt.Fprintln(stderr, reason)
			return 2
		}
	}
	return 0
}

// expandCommand expands a simple command's words, dropping its leading
// NAME=value words after recording them: they reach every later word.
func expandCommand(words []word, vars map[string]string) []string {
	var argv []string
	for _, w := range words {
		s := w.expand(vars)
		if len(argv) == 0 && w.assignment() {
			name, value, _ := strings.Cut(s, "=")
			vars[name] = value
			continue
		}
		argv = append(argv, s)
	}
	return argv
}

// gitSubcommand returns a git invocation's subcommand and its arguments, past
// git's global options.
func gitSubcommand(argv []string) (string, []string) {
	if len(argv) == 0 || argv[0] != "git" {
		return "", nil
	}
	k := 1
	for k < len(argv) && strings.HasPrefix(argv[k], "-") {
		if argv[k] == "-C" || argv[k] == "-c" {
			k++
		}
		k++
	}
	if k >= len(argv) {
		return "", nil
	}
	return argv[k], argv[k+1:]
}

// git commit options whose value may be the next word.
const commitShortValue = "mFCct"

var commitLongValue = map[string]bool{
	"--message": true, "--file": true, "--reuse-message": true, "--reedit-message": true, "--fixup": true,
	"--squash": true, "--author": true, "--date": true, "--cleanup": true, "--template": true,
	"--trailer": true, "--pathspec-from-file": true,
}

func checkCommit(argv []string) string {
	sub, args := gitSubcommand(argv)
	if sub != "commit" {
		return ""
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			return ""
		case a == "--all":
			return commitAll
		case commitLongValue[a]:
			i++
		case strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--"):
			for j, ch := range a[1:] {
				if ch == 'a' {
					return commitAll
				}
				if strings.ContainsRune(commitShortValue+"uS", ch) {
					// The rest of the cluster is the value; a value flag ending
					// the cluster takes the next word.
					if strings.ContainsRune(commitShortValue, ch) && j == len(a)-2 {
						i++
					}
					break
				}
			}
		}
	}
	return ""
}

func checkStash(argv []string) string {
	sub, args := gitSubcommand(argv)
	if sub != "stash" {
		return ""
	}
	if len(args) > 0 {
		switch {
		case args[0] == "save":
			return implicitStash // its words are a message, never a pathspec
		case args[0] == "push":
			args = args[1:]
		case !strings.HasPrefix(args[0], "-"):
			return "" // list, show, pop, apply, drop, branch, …
		}
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-m" || a == "--message" || a == "--pathspec-from-file":
			i++
		case a == "--" || !strings.HasPrefix(a, "-") || strings.HasPrefix(a, "--pathspec-from-file="):
			return "" // a pathspec follows or is here
		}
	}
	return implicitStash
}

var mountSource = regexp.MustCompile(`(?:^|,)(?:src|source)=([^,]+)`)

// mountSources returns the source half of every -v/--volume and --mount, in
// any spelling.
func mountSources(args []string) []string {
	var out []string
	volume := func(spec string) { out = append(out, strings.SplitN(spec, ":", 2)[0]) }
	mount := func(spec string) {
		if m := mountSource.FindStringSubmatch(spec); m != nil {
			out = append(out, m[1])
		}
	}
	for k, a := range args {
		next := ""
		if k+1 < len(args) {
			next = args[k+1]
		}
		switch {
		case a == "-v" || a == "--volume":
			volume(next)
		case strings.HasPrefix(a, "--volume="):
			volume(strings.TrimPrefix(a, "--volume="))
		case strings.HasPrefix(a, "-v") && len(a) > 2 && !strings.HasPrefix(a, "--"):
			volume(strings.TrimPrefix(a[2:], "="))
		case a == "--mount":
			mount(next)
		case strings.HasPrefix(a, "--mount="):
			mount(strings.TrimPrefix(a, "--mount="))
		}
	}
	return out
}

func under(p, root string) bool {
	root = strings.TrimSuffix(root, "/")
	return root != "" && (p == root || strings.HasPrefix(p, root+"/"))
}

func checkDocker(argv []string, vars map[string]string) string {
	if len(argv) == 0 || argv[0] != "docker" {
		return ""
	}
	for _, src := range mountSources(argv[1:]) {
		if !strings.HasPrefix(src, "/") && !strings.HasPrefix(src, ".") {
			continue // a named volume, or a variable left unexpanded
		}
		if !strings.HasPrefix(src, "/") {
			src = path.Join(vars["PWD"], src)
		}
		src = path.Clean(src)
		if under(src, vars["TOOLBOX_HOST_WORKSPACE"]) {
			continue
		}
		// /workspace is the container's second mount of the workspace; the
		// host has none.
		for _, root := range []string{"/tmp", "/workspace", vars["HOME"]} {
			if under(src, root) {
				return fmt.Sprintf("Blocked: bind mount source %s is container-local. The Docker daemon is the host's "+
					"and resolves the source on the host, so the mount would come up empty with no error. Put the "+
					"files under the workspace and mount $TOOLBOX_HOST_WORKSPACE followed by their path below it.", src)
			}
		}
	}
	return ""
}
