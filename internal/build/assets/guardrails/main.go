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
	"slices"
	"strings"
)

const (
	commitAll = "Blocked: git commit -a/--all stages every tracked change, including work that is not yours. " +
		"Run `git add <path>` for the files of this commit, then `git commit` without -a."
	containerLocalMount = "Blocked: bind mount source %s is container-local. The Docker daemon is the host's " +
		"and resolves the source on the host, so the mount would come up empty with no error. Put the files " +
		"under the workspace and mount $TOOLBOX_HOST_WORKSPACE followed by their path below it."
	implicitStash = "Blocked: a git stash without a pathspec, or of the whole tree, sweeps up every change in it, including work " +
		"that is not yours. Name what to stash: `git stash push -- <path>`."
	implicitStashApply = "Blocked: git stash pop/apply without a reference takes stash@{0}, which can be another " +
		"session's. Check `git stash list` and name yours: `git stash pop stash@{n}`."
	resetHard = "Blocked: git reset --hard throws away every uncommitted change in the tree, including work that " +
		"is not yours, and git keeps no copy. Use `git reset --keep <target>`, which refuses when it would lose " +
		"changes, or set yours aside first with `git stash push -- <path>`."
	treeWideDiscard = "Blocked: git %[1]s on the whole tree throws away every uncommitted change, including work " +
		"that is not yours. Name the files to discard: `git %[1]s -- <path>`."
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
	if sub, args := gitSubcommand(argv); sub != "" {
		if check, ok := gitChecks[sub]; ok {
			return check(args, gitWorkDir(argv, c.vars["PWD"]))
		}
		return ""
	}
	if c.docker {
		return checkDocker(argv, c.vars)
	}
	return ""
}

// gitChecks holds the rule for each git subcommand the hook checks, handed the
// subcommand's arguments and the directory git runs in.
var gitChecks = map[string]func(args []string, cwd string) string{
	"commit":   checkCommit,
	"stash":    checkStash,
	"reset":    checkReset,
	"checkout": checkCheckout,
	"restore":  checkRestore,
	"clean":    checkClean,
	"switch":   checkSwitch,
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

// gitWorkDir returns the directory git runs in: pwd, moved by each -C.
func gitWorkDir(argv []string, pwd string) string {
	for k := 1; k < len(argv) && strings.HasPrefix(argv[k], "-"); k++ {
		if !gitGlobalValue[argv[k]] {
			continue
		}
		k++
		if argv[k-1] == "-C" && k < len(argv) {
			if path.IsAbs(argv[k]) {
				pwd = path.Clean(argv[k])
			} else {
				pwd = path.Join(pwd, argv[k])
			}
		}
	}
	return pwd
}

// git options, before the subcommand, that take the next word.
var gitGlobalValue = set("-C", "-c", "--git-dir", "--work-tree", "--namespace", "--config-env", "--super-prefix", "--attr-source")

func checkCommit(args []string, _ string) string {
	if commitOptions.parse(args).flags["all"] {
		return commitAll
	}
	return ""
}

func checkStash(args []string, cwd string) string {
	if len(args) > 0 {
		switch {
		case args[0] == "save":
			return implicitStash // its words are a message, never a pathspec
		case args[0] == "push":
			args = args[1:]
		case args[0] == "pop" || args[0] == "apply":
			if len(stashApplyOptions.parse(args[1:]).operands()) > 0 {
				return "" // the stash is named; none of the options takes a value
			}
			return implicitStashApply
		case !strings.HasPrefix(args[0], "-"):
			return "" // list, show, drop, branch, …
		}
	}
	g := stashPushOptions.parse(args)
	// --staged keeps to what is already staged, which the agent chose.
	switch {
	case g.namesWholeTree(cwd):
		return implicitStash
	case g.flags["staged"] || g.flags["pathspec-from-file"] || len(g.operands()) > 0:
		return ""
	}
	return implicitStash
}

// gitOptions is what a check knows of one git subcommand's options: their
// long names, the short letter of each, those that take a value, and those
// whose value is optional, so it can only be attached.
type gitOptions struct {
	long     []string
	short    map[rune]string
	value    map[string]bool
	optional map[string]bool
}

// gitArgs is a subcommand's arguments read the way git's parse-options reads
// them: options may follow other words, until a "--".
type gitArgs struct {
	flags      map[string]bool // long names of the options given
	positional []string        // other words before "--": revisions and pathspecs alike
	paths      []string        // words after "--": pathspecs only
}

func (o gitOptions) parse(args []string) gitArgs {
	g := gitArgs{flags: map[string]bool{}}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			g.paths = append(g.paths, args[i+1:]...)
			return g
		case strings.HasPrefix(a, "--"):
			name, _, attached := strings.Cut(a[2:], "=")
			takesValue := false
			for _, n := range o.resolve(name) {
				g.flags[n] = true
				takesValue = takesValue || o.value[n]
			}
			if takesValue && !attached {
				i++
			}
		case strings.HasPrefix(a, "-") && len(a) > 1:
			if o.readCluster(a[1:], g.flags) {
				i++
			}
		default:
			g.positional = append(g.positional, a)
		}
	}
	return g
}

// resolve maps a long option as written to the names it can mean: the exact
// name, or every name it abbreviates. git rejects an ambiguous abbreviation,
// so whatever marking each candidate does, git runs no such command.
func (o gitOptions) resolve(name string) []string {
	var out []string
	for _, n := range o.long {
		if n == name {
			return []string{n}
		}
		if name != "" && strings.HasPrefix(n, name) {
			out = append(out, n)
		}
	}
	return out
}

// readCluster records a cluster of short options into flags and reports
// whether its last option takes the next word as its value. A value option
// ends the cluster: the rest of it is the value.
func (o gitOptions) readCluster(cluster string, flags map[string]bool) (takesNext bool) {
	for j, ch := range cluster {
		n, ok := o.short[ch]
		if !ok {
			continue
		}
		flags[n] = true
		if o.optional[n] {
			return false // the rest of the cluster is its value, never the next word
		}
		if o.value[n] {
			return j == len(cluster)-1
		}
	}
	return false
}

// operands returns the words that are not options: after "--" pathspecs only,
// before it revisions and pathspecs alike, since git tells them apart by the tree.
func (g gitArgs) operands() []string {
	return append(append([]string{}, g.positional...), g.paths...)
}

// namesWholeTree reports whether an operand, read as a pathspec from cwd, covers
// the whole tree or a parent of cwd.
func (g gitArgs) namesWholeTree(cwd string) bool {
	return slices.ContainsFunc(g.operands(), func(p string) bool { return wholeTree(p, cwd) })
}

// wholeTree reports whether pathspec p, given in cwd, covers cwd or more: ".",
// "..", "*", the top magic (":/", ":(top)" among other magic words), and an
// absolute path to cwd or a parent of it, in any spelling path.Clean folds.
func wholeTree(p, cwd string) bool {
	rest, top := stripMagic(p)
	if top && rest == "" {
		return true
	}
	if rest == "*" {
		rest = "."
	} else if strings.HasSuffix(rest, "/*") {
		rest = strings.TrimSuffix(rest, "*")
	}
	if rest == "" {
		return false
	}
	if path.IsAbs(rest) {
		root := path.Clean(rest)
		return root == "/" || pathWithin(cwd, root)
	}
	for _, segment := range strings.Split(path.Clean(rest), "/") {
		if segment != "." && segment != ".." {
			return false
		}
	}
	return true
}

// stripMagic removes a pathspec's magic prefix (":/" or ":(word,…)") and
// reports whether it anchors the pathspec at the top of the tree.
func stripMagic(p string) (rest string, top bool) {
	if after, ok := strings.CutPrefix(p, ":/"); ok {
		return after, true
	}
	if after, ok := strings.CutPrefix(p, ":("); ok {
		if words, rest, ok := strings.Cut(after, ")"); ok {
			return rest, slices.Contains(strings.Split(words, ","), "top")
		}
	}
	return p, false
}

var (
	commitOptions = gitOptions{
		long: []string{"all", "amend", "allow-empty", "allow-empty-message", "author", "message", "file",
			"reuse-message", "reedit-message", "fixup", "squash", "date", "cleanup", "template", "trailer",
			"pathspec-from-file", "untracked-files", "gpg-sign", "include", "only", "patch", "signoff", "edit",
			"no-edit", "verbose", "quiet", "dry-run", "no-verify"},
		short: map[rune]string{'a': "all", 'm': "message", 'F': "file", 'C': "reuse-message", 'c': "reedit-message",
			't': "template", 'u': "untracked-files", 'S': "gpg-sign", 'i': "include", 'o': "only", 'p': "patch",
			's': "signoff", 'e': "edit", 'v': "verbose", 'q': "quiet", 'n': "no-verify"},
		value: set("author", "message", "file", "reuse-message", "reedit-message", "fixup", "squash", "date",
			"cleanup", "template", "trailer", "pathspec-from-file"),
		optional: set("untracked-files", "gpg-sign"),
	}
	stashPushOptions = gitOptions{
		long: []string{"staged", "message", "keep-index", "no-keep-index", "include-untracked", "all", "patch",
			"quiet", "pathspec-from-file", "pathspec-file-nul"},
		short: map[rune]string{'S': "staged", 'm': "message", 'k': "keep-index", 'u': "include-untracked", 'a': "all",
			'p': "patch", 'q': "quiet"},
		value: set("message", "pathspec-from-file"),
	}
	stashApplyOptions = gitOptions{
		long:  []string{"index", "quiet"},
		short: map[rune]string{'q': "quiet"},
	}
	resetOptions = gitOptions{
		long:  []string{"hard", "soft", "mixed", "keep", "merge", "quiet", "patch", "pathspec-from-file"},
		short: map[rune]string{'q': "quiet", 'p': "patch"},
		value: set("pathspec-from-file"),
	}
	checkoutOptions = gitOptions{
		long:  []string{"force", "orphan", "pathspec-from-file", "ours", "theirs", "track", "merge", "conflict", "patch", "detach", "quiet"},
		short: map[rune]string{'f': "force", 'b': "new-branch", 'B': "reset-branch", 'q': "quiet", 'm': "merge", 'p': "patch", 't': "track"},
		value: set("new-branch", "reset-branch", "orphan", "pathspec-from-file", "conflict"),
	}
	restoreOptions = gitOptions{
		long:  []string{"staged", "worktree", "source", "pathspec-from-file", "ours", "theirs", "merge", "conflict", "patch", "quiet", "overlay", "no-overlay", "progress"},
		short: map[rune]string{'S': "staged", 'W': "worktree", 's': "source", 'q': "quiet", 'm': "merge", 'p': "patch"},
		value: set("source", "pathspec-from-file", "conflict"),
	}
	cleanOptions = gitOptions{
		long:  []string{"force", "dry-run", "exclude", "quiet", "interactive"},
		short: map[rune]string{'f': "force", 'n': "dry-run", 'e': "exclude", 'q': "quiet", 'i': "interactive"},
		value: set("exclude"),
	}
	switchOptions = gitOptions{
		long:  []string{"force", "discard-changes", "create", "force-create", "orphan", "detach", "guess", "track", "merge", "conflict", "quiet", "progress"},
		short: map[rune]string{'f': "force", 'c': "create", 'C': "force-create", 'd': "detach", 't': "track", 'm': "merge", 'q': "quiet"},
		value: set("create", "force-create", "orphan", "conflict"),
	}
)

func checkReset(args []string, _ string) string {
	if resetOptions.parse(args).flags["hard"] {
		return resetHard
	}
	return ""
}

func checkCheckout(args []string, cwd string) string {
	g := checkoutOptions.parse(args)
	switch {
	case g.namesWholeTree(cwd):
		return fmt.Sprintf(treeWideDiscard, "checkout")
	case g.flags["force"] && len(g.paths) == 0:
		// With files named after "--", -f only lets their unmerged entries go.
		return fmt.Sprintf(forcedSwitch, "checkout")
	}
	return ""
}

func checkRestore(args []string, cwd string) string {
	g := restoreOptions.parse(args)
	// Without --staged, restore writes the worktree; with it, only --worktree does.
	writesWorktree := g.flags["worktree"] || !g.flags["staged"]
	if writesWorktree && g.namesWholeTree(cwd) {
		return fmt.Sprintf(treeWideDiscard, "restore")
	}
	return ""
}

func checkClean(args []string, cwd string) string {
	g := cleanOptions.parse(args)
	if !g.flags["force"] || g.flags["dry-run"] {
		return ""
	}
	if len(g.operands()) == 0 || g.namesWholeTree(cwd) {
		return treeWideClean
	}
	return ""
}

func checkSwitch(args []string, _ string) string {
	g := switchOptions.parse(args)
	if g.flags["force"] || g.flags["discard-changes"] {
		return fmt.Sprintf(forcedSwitch, "switch")
	}
	return ""
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
