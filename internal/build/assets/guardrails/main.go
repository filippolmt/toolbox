// Command agent-guardrails is the image's PreToolUse hook for the agents'
// shell tool. What it blocks, and how each agent runs it:
// docs/internals/image-build.md#agent-guardrails.
//
// It reads the hook JSON on stdin. Exit 2 blocks the tool call and hands
// stderr back to the agent; exit 0 lets it run. Input it cannot read is let
// through: the hook catches mistakes, it is not a sandbox.
//
// The bind-mount check runs only while the agent can reach a Docker daemon:
// the docker-sock mount is present or DOCKER_HOST names one.
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
	containerLocalMount = "Blocked: bind mount source %s is container-local. The Docker daemon is the host's " +
		"and resolves the source on the host, so the mount would come up empty with no error. Put the files " +
		"under the workspace and mount $TOOLBOX_HOST_WORKSPACE followed by their path below it."
	implicitStash = "Blocked: a git stash without a pathspec sweeps up every change in the tree, including work " +
		"that is not yours. Name what to stash: `git stash push -- <path>`."
	implicitStashApply = "Blocked: git stash pop/apply without a reference takes stash@{0}, which can be another " +
		"session's. Check `git stash list` and name yours: `git stash pop stash@{n}`."
	resetHard = "Blocked: git reset --hard throws away every uncommitted change in the tree, including work that " +
		"is not yours, and git keeps no copy. Use `git reset --keep <target>`, which refuses when it would lose " +
		"changes, or set yours aside first with `git stash push -- <path>`."
	treeWideDiscard = "Blocked: git %s on the whole tree throws away every uncommitted change, including work " +
		"that is not yours. Name the files to discard: `git %s -- <path>`."
	forcedSwitch = "Blocked: a forced git %s throws away every uncommitted change in the tree, including work " +
		"that is not yours. Commit yours, or set it aside with `git stash push -- <path>`, then switch without force."
	treeWideClean = "Blocked: a forced git clean with no pathspec deletes every untracked file in the tree, " +
		"including new files that are not yours. Review with `git clean -n`, then name the paths: " +
		"`git clean -f -- <path>`."
)

func main() {
	env := map[string]string{}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	os.Exit(run(env, os.Stdin, os.Stderr))
}

// dockerSocket is the docker-sock mount's target in the Shell.
var dockerSocket = "/var/run/docker.sock"

func dockerReachable(env map[string]string) bool {
	if env["DOCKER_HOST"] != "" {
		return true
	}
	_, err := os.Stat(dockerSocket)
	return err == nil
}

func run(env map[string]string, stdin io.Reader, stderr io.Writer) int {
	var event struct {
		Cwd       string `json:"cwd"`
		ToolInput struct {
			Command string `json:"command"`
		} `json:"tool_input"`
	}
	if err := json.NewDecoder(stdin).Decode(&event); err != nil {
		return 0
	}
	vars := maps.Clone(env)
	if vars == nil {
		vars = map[string]string{}
	}
	if event.Cwd != "" {
		vars["PWD"] = event.Cwd
	}
	c := checker{vars: vars, docker: dockerReachable(env)}
	if reason := c.line(event.ToolInput.Command, 0); reason != "" {
		_, _ = fmt.Fprintln(stderr, reason)
		return 2
	}
	return 0
}

// checker carries what the checks share across one command line: the shell
// variables it has set so far and whether Docker is reachable.
type checker struct {
	vars   map[string]string
	docker bool
}

// maxNesting bounds how deep bash -c / eval scripts are followed.
const maxNesting = 3

// line checks every simple command of a command line, following the scripts
// of bash -c, sh -c and eval, and returns the first block reason. A line it
// cannot split blocks nothing.
func (c checker) line(cmdline string, depth int) string {
	commands, subs, err := tokenize(cmdline)
	if err != nil {
		return ""
	}
	if depth < maxNesting {
		for _, sub := range subs {
			if reason := c.line(sub, depth+1); reason != "" {
				return reason
			}
		}
	}
	for _, words := range commands {
		if reason := c.simple(words, depth); reason != "" {
			return reason
		}
	}
	return ""
}

// simple checks one simple command, or the script it hands to a shell.
func (c checker) simple(words []word, depth int) string {
	argv := stripWrappers(expandCommand(words, c.vars))
	if recordAssignments(argv, c.vars) || c.changeDirectory(argv) {
		return ""
	}
	if script, ok := nestedScript(argv); ok {
		if depth >= maxNesting {
			return ""
		}
		return c.line(script, depth+1)
	}
	return c.command(argv)
}

// changeDirectory follows cd and pushd into vars["PWD"], which relative bind
// sources resolve against, and reports whether argv was one of them.
func (c checker) changeDirectory(argv []string) bool {
	if len(argv) == 0 || argv[0] != "cd" && argv[0] != "pushd" {
		return false
	}
	args := argv[1+skipOptions(argv[1:], func(string) bool { return false }):]
	switch {
	case len(args) == 0:
		c.vars["PWD"] = c.vars["HOME"]
	case args[0] == "-":
		// The previous directory is not tracked; keep the current one.
	case path.IsAbs(args[0]):
		c.vars["PWD"] = path.Clean(args[0])
	default:
		c.vars["PWD"] = path.Join(c.vars["PWD"], args[0])
	}
	return true
}

func (c checker) command(argv []string) string {
	if reason := checkCommit(argv); reason != "" {
		return reason
	}
	if reason := checkStash(argv); reason != "" {
		return reason
	}
	if reason := checkReset(argv); reason != "" {
		return reason
	}
	if reason := checkCheckout(argv); reason != "" {
		return reason
	}
	if reason := checkRestore(argv); reason != "" {
		return reason
	}
	if reason := checkClean(argv); reason != "" {
		return reason
	}
	if reason := checkSwitch(argv); reason != "" {
		return reason
	}
	if c.docker {
		return checkDocker(argv, c.vars)
	}
	return ""
}

var shells = set("bash", "sh", "zsh", "dash", "ksh")

// nestedScript returns the script a command hands to a shell: the word after
// bash/sh -c (in any option cluster, such as -lc), or eval's words joined.
func nestedScript(argv []string) (string, bool) {
	if len(argv) == 0 {
		return "", false
	}
	if argv[0] == "eval" {
		return strings.Join(argv[1:], " "), true
	}
	if !shells[argv[0]] {
		return "", false
	}
	return shellCommandString(argv[1:])
}

// shellCommandString returns the script a shell's arguments carry: with -c in
// any option cluster (-c, -lc, -ec), the first operand after the options.
// A cluster ending in o or O (-o pipefail, -euo pipefail, -O extglob) takes
// the next word.
func shellCommandString(args []string) (string, bool) {
	k, sawC := shellOptions(args)
	if !sawC || k >= len(args) {
		return "", false
	}
	return args[k], true
}

// shellOptions scans a shell's leading options and returns the index of its
// first operand and whether any option cluster held -c.
func shellOptions(args []string) (int, bool) {
	sawC := false
	for k := 0; k < len(args); k++ {
		a := args[k]
		switch {
		case a == "--":
			return k + 1, sawC
		case !strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "+"):
			return k, sawC
		case strings.HasPrefix(a, "--"):
			// a long option, such as --login
		default:
			sawC = sawC || strings.ContainsRune(a[1:], 'c')
			if strings.HasSuffix(a, "o") || strings.HasSuffix(a, "O") {
				k++
			}
		}
	}
	return len(args), sawC
}

// redirection matches a redirection word, capturing what follows the operator:
// empty when the target is the next word.
var redirection = regexp.MustCompile(`^(?:[0-9]*|&)(?:>>?|<<?<?|>&|<&|>\|)(.*)$`)

// expandCommand expands a simple command's words, dropping redirections and
// their targets, and, as the shell does, an unquoted word that expands to
// nothing. Its leading NAME=value words
// are dropped from the result and written into vars, on purpose: the shell
// expands every later command's words with them.
func expandCommand(words []word, vars map[string]string) []string {
	var argv []string
	for k := 0; k < len(words); k++ {
		w := words[k]
		s := w.expand(vars)
		if m := redirection.FindStringSubmatch(s); m != nil && w.unquotedStart() {
			if m[1] == "" {
				k++ // the target is the next word
			}
			continue
		}
		if len(argv) == 0 && w.assignment() {
			name, value, _ := strings.Cut(s, "=")
			vars[name] = value
			continue
		}
		if s == "" && !w.quoted() {
			continue
		}
		argv = append(argv, s)
	}
	return argv
}

// Options of the command wrappers that take the next word as their value.
// wrapper describes a command that runs another: the options that take the
// next word, and the operands it reads before the command (timeout's duration).
type wrapper struct {
	valueOptions map[string]bool
	operands     int
}

var wrappers = map[string]wrapper{
	"sudo":    {valueOptions: set("-u", "-g", "-p", "-C", "-h", "-r", "-t", "-U", "-D", "-R", "-T")},
	"env":     {valueOptions: set("-u", "-C", "-S")},
	"command": {},
	"exec":    {valueOptions: set("-a")},
	"timeout": {valueOptions: set("-s", "--signal", "-k", "--kill-after"), operands: 1},
	"nice":    {valueOptions: set("-n", "--adjustment")},
	"nohup":   {},
	"time":    {},
	"ionice":  {valueOptions: set("-c", "--class", "-n", "--classdata")},
	"stdbuf":  {valueOptions: set("-i", "-o", "-e")},
}

// stripWrappers drops leading command wrappers (sudo, env, timeout, …) with
// their options and operands, so the checks see the command they run, named
// without its directory. env's NAME=value words go with it: they reach the
// child's environment, not the words the shell already expanded.
func stripWrappers(argv []string) []string {
	for len(argv) > 0 {
		argv[0] = path.Base(argv[0]) // /usr/bin/git runs git
		if controlKeywords[argv[0]] {
			argv = argv[1:]
			continue
		}
		if argv[0] == "function" {
			argv = argv[min(2, len(argv)):] // function NAME { … : the body runs
			continue
		}
		if split, ok := envSplitString(argv); ok {
			argv = split
			continue
		}
		w, ok := wrappers[argv[0]]
		if !ok {
			return argv
		}
		k := 1 + skipOptions(argv[1:], func(a string) bool { return w.valueOptions[a] })
		for argv[0] == "env" && k < len(argv) && assignmentPrefix.MatchString(argv[k]) {
			k++
		}
		if k < len(argv) && argv[k] == "--" {
			k++
		}
		argv = argv[min(k+w.operands, len(argv)):]
	}
	return argv
}

// controlKeywords open a compound command or a branch of one; the command they
// lead with is the one that runs.
var controlKeywords = set("if", "then", "else", "elif", "while", "until", "do", "!", "{")

// envSplitString rewrites `env [opts] -S 'cmd args' rest…` as the command
// env runs: the split string's words, then the rest.
func envSplitString(argv []string) ([]string, bool) {
	if argv[0] != "env" {
		return nil, false
	}
	for k := 1; k < len(argv) && strings.HasPrefix(argv[k], "-"); k++ {
		a := argv[k]
		switch {
		case (a == "-S" || a == "--split-string") && k+1 < len(argv):
			return append(strings.Fields(argv[k+1]), argv[k+2:]...), true
		case strings.HasPrefix(a, "--split-string="):
			return append(strings.Fields(strings.TrimPrefix(a, "--split-string=")), argv[k+1:]...), true
		case strings.HasPrefix(a, "-S") && len(a) > 2:
			return append(strings.Fields(a[2:]), argv[k+1:]...), true
		case wrappers["env"].valueOptions[a]:
			k++
		}
	}
	return nil, false
}

// assignmentBuiltins set shell variables from their NAME=value operands.
var assignmentBuiltins = set("export", "readonly", "declare", "typeset", "local")

// recordAssignments writes the NAME=value operands of export, declare and
// their kin into vars, as the shell does for every later command, and reports
// whether argv was one of them.
func recordAssignments(argv []string, vars map[string]string) bool {
	if len(argv) == 0 || !assignmentBuiltins[argv[0]] {
		return false
	}
	for _, a := range argv[1:] {
		if assignmentPrefix.MatchString(a) {
			name, value, _ := strings.Cut(a, "=")
			vars[name] = value
		}
	}
	return true
}

// gitSubcommand returns a git invocation's subcommand and its arguments, past
// git's global options.
func gitSubcommand(argv []string) (string, []string) {
	if len(argv) == 0 || argv[0] != "git" {
		return "", nil
	}
	k := 1 + skipOptions(argv[1:], func(a string) bool { return gitGlobalValue[a] })
	if k >= len(argv) {
		return "", nil
	}
	return argv[k], argv[k+1:]
}

// git options, before the subcommand, that take the next word.
var gitGlobalValue = set("-C", "-c", "--git-dir", "--work-tree", "--namespace", "--config-env", "--super-prefix", "--attr-source")

// git commit short options whose value is the rest of the cluster or, ending
// it, the next word.
const commitShortValue = "mFCct"

// git commit short options with an optional value: it can only be attached,
// so the rest of the cluster is theirs and the next word never is.
const commitShortOptionalValue = "uS"

var commitLongValue = set("--message", "--file", "--reuse-message", "--reedit-message", "--fixup",
	"--squash", "--author", "--date", "--cleanup", "--template", "--trailer", "--pathspec-from-file")

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
			all, takesNext := readCommitCluster(a[1:])
			if all {
				return commitAll
			}
			if takesNext {
				i++
			}
		}
	}
	return ""
}

// readCommitCluster reads a cluster of git commit short options: whether it
// holds -a, and whether its last option takes the next word as its value.
// A value option ends the cluster: the rest of it is the value.
func readCommitCluster(cluster string) (all, takesNext bool) {
	for j, ch := range cluster {
		switch {
		case ch == 'a':
			return true, false
		case strings.ContainsRune(commitShortValue, ch):
			return false, j == len(cluster)-1
		case strings.ContainsRune(commitShortOptionalValue, ch):
			return false, false
		}
	}
	return false, false
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
		case args[0] == "pop" || args[0] == "apply":
			if stashRefGiven(args[1:]) {
				return ""
			}
			return implicitStashApply
		case !strings.HasPrefix(args[0], "-"):
			return "" // list, show, drop, branch, …
		}
	}
	if stashSelects(args) {
		return ""
	}
	return implicitStash
}

// stashRefGiven reports whether git stash pop/apply arguments name a
// stash: none of their options (--index, -q) takes a value, so any other word is one.
func stashRefGiven(args []string) bool {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return true
		}
	}
	return false
}

// stashSelects reports whether git stash push arguments name what to stash:
// a pathspec, or --staged.
func stashSelects(args []string) bool {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-m" || a == "--message" || a == "--pathspec-from-file":
			i++
		case a == "-S" || a == "--staged":
			return true // only what is already staged, which the agent chose
		case a == "--" || !strings.HasPrefix(a, "-") || strings.HasPrefix(a, "--pathspec-from-file="):
			return true // a pathspec follows or is here
		case !strings.HasPrefix(a, "--"):
			staged, takesNext := readStashCluster(a[1:])
			if staged {
				return true
			}
			if takesNext {
				i++
			}
		}
	}
	return false
}

// readStashCluster reads a cluster of git stash short options: whether it
// holds -S, and whether it ends in -m, whose value is then the next word.
// -m ends the cluster: the rest of it is the message.
func readStashCluster(cluster string) (staged, takesNext bool) {
	for j, ch := range cluster {
		switch ch {
		case 'S':
			return true, false
		case 'm':
			return false, j == len(cluster)-1
		}
	}
	return false, false
}

func checkReset(argv []string) string {
	sub, args := gitSubcommand(argv)
	if sub != "reset" {
		return ""
	}
	for _, a := range args {
		if a == "--" {
			return ""
		}
		if a == "--hard" {
			return resetHard
		}
	}
	return ""
}

// rootPathspec holds the pathspecs that name the whole tree.
var rootPathspec = set(".", "./", ":/")

func checkCheckout(argv []string) string {
	sub, args := gitSubcommand(argv)
	if sub != "checkout" {
		return ""
	}
	options := true
	for _, a := range args {
		switch {
		case rootPathspec[a]:
			return fmt.Sprintf(treeWideDiscard, sub, sub)
		case !options:
		case a == "--":
			options = false
		case a == "--force" || shortClusterHas(a, 'f', "bB"):
			return fmt.Sprintf(forcedSwitch, sub)
		}
	}
	return ""
}

func checkRestore(argv []string) string {
	sub, args := gitSubcommand(argv)
	if sub != "restore" {
		return ""
	}
	var staged, worktree, root bool
	options := true
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case rootPathspec[a]:
			root = true
		case !options || !strings.HasPrefix(a, "-"):
		case a == "--":
			options = false
		case a == "--staged":
			staged = true
		case a == "--worktree":
			worktree = true
		case a == "--source" || a == "--pathspec-from-file":
			i++
		case !strings.HasPrefix(a, "--"):
			s, w, takesNext := readRestoreCluster(a[1:])
			staged, worktree = staged || s, worktree || w
			if takesNext {
				i++
			}
		}
	}
	// Without --staged, restore writes the worktree; with it, only --worktree does.
	if root && (worktree || !staged) {
		return fmt.Sprintf(treeWideDiscard, sub, sub)
	}
	return ""
}

func checkClean(argv []string) string {
	sub, args := gitSubcommand(argv)
	if sub != "clean" {
		return ""
	}
	var force, dryRun, named bool
	options := true
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case !options || !strings.HasPrefix(a, "-"):
			named = named || !rootPathspec[a]
		case a == "--":
			options = false
		case a == "--force":
			force = true
		case a == "--dry-run":
			dryRun = true
		case a == "--exclude":
			i++
		case !strings.HasPrefix(a, "--"):
			f, n, takesNext := readCleanCluster(a[1:])
			force, dryRun = force || f, dryRun || n
			if takesNext {
				i++
			}
		}
	}
	if force && !dryRun && !named {
		return treeWideClean
	}
	return ""
}

// readCleanCluster reads a cluster of git clean short options: whether it
// holds -f and -n, and whether it ends in -e, whose value is then the next word.
// -e ends the cluster: the rest of it is the pattern.
func readCleanCluster(cluster string) (force, dryRun, takesNext bool) {
	for j, ch := range cluster {
		switch ch {
		case 'f':
			force = true
		case 'n':
			dryRun = true
		case 'e':
			return force, dryRun, j == len(cluster)-1
		}
	}
	return force, dryRun, false
}

// readRestoreCluster reads a cluster of git restore short options: whether it
// holds -S and -W, and whether it ends in -s, whose value is then the next word.
// -s ends the cluster: the rest of it is the source.
func readRestoreCluster(cluster string) (staged, worktree, takesNext bool) {
	for j, ch := range cluster {
		switch ch {
		case 'S':
			staged = true
		case 'W':
			worktree = true
		case 's':
			return staged, worktree, j == len(cluster)-1
		}
	}
	return staged, worktree, false
}

func checkSwitch(argv []string) string {
	sub, args := gitSubcommand(argv)
	if sub != "switch" {
		return ""
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			return ""
		case a == "-c" || a == "-C" || a == "--create" || a == "--force-create" || a == "--orphan":
			i++
		case a == "--force" || a == "--discard-changes" || shortClusterHas(a, 'f', "cC"):
			return fmt.Sprintf(forcedSwitch, sub)
		}
	}
	return ""
}

// shortClusterHas reports whether a short option cluster holds flag before any
// of the options in valueOpts, whose value is the rest of the cluster.
func shortClusterHas(word string, flag rune, valueOpts string) bool {
	if !strings.HasPrefix(word, "-") || strings.HasPrefix(word, "--") {
		return false
	}
	for _, ch := range word[1:] {
		if ch == flag {
			return true
		}
		if strings.ContainsRune(valueOpts, ch) {
			return false
		}
	}
	return false
}

var (
	// Docker and Compose options, before their subcommand, that take the next word.
	dockerGlobalValue  = set("-H", "--host", "-c", "--context", "--config", "-l", "--log-level", "--tlscacert", "--tlscert", "--tlskey")
	composeGlobalValue = set("-f", "--file", "-p", "--project-name", "--project-directory", "--env-file", "--profile", "--parallel", "--ansi", "--progress")
	// run/create options that take no value; every other option takes one.
	runBoolLong  = set("--rm", "--detach", "--interactive", "--tty", "--privileged", "--init", "--publish-all", "--read-only", "--sig-proxy", "--no-healthcheck", "--oom-kill-disable", "--quiet", "--help", "--no-deps", "--service-ports", "--use-aliases", "--build", "--remove-orphans", "--quiet-pull")
	runBoolShort = "ditPqT"
)

func set(words ...string) map[string]bool {
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
}

// skipOptions returns the index of the first word in args that is not an
// option, stepping over the value of every option in takesValue.
func skipOptions(args []string, takesValue func(string) bool) int {
	k := 0
	for k < len(args) && strings.HasPrefix(args[k], "-") {
		if takesValue(args[k]) {
			k++
		}
		k++
	}
	return min(k, len(args))
}

// dockerRunOptions returns the options of a docker run/create, docker
// container run/create or docker compose run: the words between the
// subcommand and the image or service, where the container's own command —
// and its own -v — begins. Any other docker command yields none.
func dockerRunOptions(args []string) []string {
	args = args[skipOptions(args, func(a string) bool { return dockerGlobalValue[a] }):]
	switch {
	case len(args) > 1 && args[0] == "container":
		args = args[1:]
	case len(args) > 0 && args[0] == "compose":
		args = args[1:]
		args = args[skipOptions(args, func(a string) bool { return composeGlobalValue[a] }):]
		if len(args) == 0 || args[0] != "run" {
			return nil
		}
	}
	if len(args) == 0 || args[0] != "run" && args[0] != "create" {
		return nil
	}
	opts := args[1:]
	return opts[:skipOptions(opts, runOptionTakesValue)]
}

// runOptionTakesValue reports whether a run/create option takes the next word.
func runOptionTakesValue(a string) bool {
	if strings.HasPrefix(a, "--") {
		return !strings.Contains(a, "=") && !runBoolLong[a]
	}
	cluster := a[1:]
	if strings.Trim(cluster, runBoolShort) == "" {
		return false // -it, -d, …
	}
	return len(cluster) == 1 // -e VALUE; -eVALUE carries its own
}

var mountSource = regexp.MustCompile(`(?:^|,)(?:src|source)=([^,]+)`)

// mountSources returns the source half of every -v/--volume and --mount, in
// any spelling.
func mountSources(args []string) []string {
	var out []string
	volume := func(spec string) {
		// A spec with no colon is an anonymous volume at that container path.
		if src, _, bind := strings.Cut(spec, ":"); bind {
			out = append(out, src)
		}
	}
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

// pathWithin reports whether the clean path p is root or lies below it.
func pathWithin(p, root string) bool {
	root = strings.TrimSuffix(root, "/")
	return root != "" && (p == root || strings.HasPrefix(p, root+"/"))
}

func checkDocker(argv []string, vars map[string]string) string {
	if len(argv) > 0 && argv[0] == "docker-compose" {
		argv = append([]string{"docker", "compose"}, argv[1:]...)
	}
	if len(argv) == 0 || argv[0] != "docker" {
		return ""
	}
	for _, src := range mountSources(dockerRunOptions(argv[1:])) {
		if p, ok := containerLocalSource(src, vars); ok {
			return fmt.Sprintf(containerLocalMount, p)
		}
	}
	return ""
}

// containerLocalSource resolves a bind source the way the docker CLI does,
// against the shell's working directory, and reports it when the host daemon
// cannot see it: under /tmp, $HOME or /workspace (the container's second
// mount of the workspace; the host has none), not under the host workspace.
func containerLocalSource(src string, vars map[string]string) (string, bool) {
	if !strings.HasPrefix(src, "/") && !strings.HasPrefix(src, ".") {
		return "", false // a named volume, or a variable left unexpanded
	}
	if !strings.HasPrefix(src, "/") {
		src = path.Join(vars["PWD"], src)
	}
	src = path.Clean(src)
	if pathWithin(src, vars["TOOLBOX_HOST_WORKSPACE"]) {
		return "", false
	}
	for _, root := range []string{"/tmp", "/workspace", vars["HOME"]} {
		if pathWithin(src, root) {
			return src, true
		}
	}
	return "", false
}
