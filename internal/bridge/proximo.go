package bridge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/filippolmt/toolbox/internal/fsx"
)

// proximoAllowlist is the fixed set of proximo subcommands /proximo may
// execute. A client-supplied command never reaches exec without passing this
// gate; arguments after it are forwarded verbatim, so the gate is on the verb
// alone (ADR-0004). Deliberately excludes install/uninstall/trust, which need
// host root — an unattended container→host-root path is one a prompt-injected
// agent inherits — and `config`, whose one mutating form (`config tld`) rewrites
// the host resolver while its queries describe a host the container cannot change.
var proximoAllowlist = map[string]struct{}{
	"up":     {},
	"down":   {},
	"status": {},
	"errors": {},
	"skill":  {},
}

// isProximoOutputFlag reports whether arg is proximo's output-redirection
// flag. It is the one argument-shaped rule the daemon enforces (ADR-0004):
// `errors transcript -o FILE` over the bridge would write to the **host**
// filesystem, while shell redirection inside the container is the correct way
// to capture the output.
//
// Upstream spells it `--out` / `-o`, on the `errors dom` and `errors
// transcript` leaves (proximo internal/cli/errors.go:225,695); `--output` is
// matched too, cheaply, against the day it grows the synonym. The short form
// matches by *letter* rather than equality, so neither a cluster (`-jo`) nor an
// attached value (`-ofile`) slips past — `-o` is currently upstream's only
// short flag at all, so nothing legitimate is caught, and over-rejecting a
// future short that carries an `o` is the safe direction.
func isProximoOutputFlag(arg string) bool {
	if len(arg) < 2 || !strings.HasPrefix(arg, "-") || arg == "--" {
		return false
	}
	name, _, _ := strings.Cut(arg, "=")
	if long, ok := strings.CutPrefix(name, "--"); ok {
		return long == "out" || long == "output"
	}
	return strings.ContainsRune(name[1:], 'o')
}

// proximoErrorsCommand and proximoDomSubcommand name the one verb/subcommand
// pair the gate refuses outright. `errors dom` writes an HTML file on the HOST
// with or without a flag — upstream defaults the destination to
// os.TempDir()/proximo-dom-<id>.html (proximo internal/cli/errors.go:216-221) —
// so the --out rule cannot see it. Through the bridge the file lands where the
// container cannot read it: the subcommand buys nothing here and leaves a host
// write behind, so the verb gate refuses it. Its sibling `errors transcript`
// writes to stdout when given no --out, and stays bridged.
const (
	proximoErrorsCommand = "errors"
	proximoDomSubcommand = "dom"
)

// isProximoHostWrite reports whether command+args is the one combination that
// writes to the host filesystem no matter what flags accompany it.
func isProximoHostWrite(command string, args []string) bool {
	if command != proximoErrorsCommand {
		return false
	}
	return slices.Contains(args, proximoDomSubcommand)
}

// proximoSkillCommand is the one verb that runs in the home-rewritten
// execution mode — see "Proximo Execution Modes" in CONTEXT.md.
const proximoSkillCommand = "skill"

// proximoAgentHome carries the HOST paths behind the calling container's two
// agent homes, as that session's mount plan resolved them (sessionplan's
// TOOLBOX_HOST_AGENT_HOME / TOOLBOX_HOST_CODEX_HOME, forwarded by the shim).
// The daemon cannot derive them: mounts_root, --profile and inherit_host_auth
// each move the host source backing /home/toolbox/.claude and
// /home/toolbox/.codex, and inherit_host_auth can move one without the other.
// A zero value means the caller sent none — an older image, whose shim does
// not know the fields — and the default mounts root is assumed.
type proximoAgentHome struct {
	Home      string
	CodexHome string
}

// proximoSkillArgs forces global scope on a `skill` subcommand: upstream
// defaults to `project`, which resolves against the *daemon's* working
// directory — nowhere an agent looks.
//
// Only the install/uninstall leaves carry --scope; the `skill` parent has no
// flags of its own (proximo internal/cli/skill.go:22,37-43), so appending it
// unconditionally would turn a bare `proximo skill` into `unknown flag:
// --scope`. An explicit --scope the caller sent is left alone rather than
// silently overridden.
func proximoSkillArgs(args []string) []string {
	scoped := false
	for _, a := range args {
		if a == "--scope" || strings.HasPrefix(a, "--scope=") {
			return args
		}
		if a == "install" || a == "uninstall" {
			scoped = true
		}
	}
	if !scoped {
		return args
	}
	return append(slices.Clone(args), "--scope", "global")
}

// proximoAgentHomeEnv points HOME and CODEX_HOME at the host directories the
// calling session mounts as /home/toolbox/.claude and /home/toolbox/.codex, so
// `proximo skill install` writes where the *in-container* agents read.
// Upstream resolves an agent's base dir from $CODEX_HOME when set and from
// os.UserHomeDir() — i.e. $HOME — otherwise, so setting both covers Claude and
// Codex in one run.
//
// The paths come from the caller because only its session plan knows them.
// They are host paths chosen by the container, so each is checked to be a
// clean absolute path to an existing directory; anything else is refused
// rather than silently replaced with a default, which is what would turn a
// misdirected install into a no-op the caller never hears about. A caller that
// sends neither (an older image) falls back to the default mounts root.
func proximoAgentHomeEnv(env []string, agent proximoAgentHome) ([]string, error) {
	if agent.Home == "" && agent.CodexHome == "" {
		base, err := fsx.Home()
		if err != nil {
			return nil, err
		}
		agent = proximoAgentHome{Home: filepath.Join(base, ".toolbox")}
		agent.CodexHome = filepath.Join(agent.Home, ".codex")
		return setEnv(setEnv(env, "HOME", agent.Home), "CODEX_HOME", agent.CodexHome), nil
	}
	for _, f := range []struct{ label, dir string }{{FieldHome, agent.Home}, {FieldCodexHome, agent.CodexHome}} {
		if f.dir == "" {
			continue
		}
		if err := checkHostDir(f.label, f.dir); err != nil {
			return nil, err
		}
	}
	if agent.Home != "" {
		env = setEnv(env, "HOME", agent.Home)
	}
	if agent.CodexHome != "" {
		env = setEnv(env, "CODEX_HOME", agent.CodexHome)
	}
	return env, nil
}

// checkHostDir rejects a caller-supplied host path that is not a clean
// absolute path to an existing directory. The container chooses these, so they
// are validated before becoming a host process's HOME — and refused loudly
// rather than replaced with a default, which is what would turn a misdirected
// install into a no-op the caller never hears about.
func checkHostDir(label, dir string) error {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return fmt.Errorf("%s %q is not a clean absolute path", label, dir)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return fmt.Errorf("%s %q is not an existing directory on the host", label, dir)
	}
	return nil
}

// setEnv returns env with key set to value, replacing the existing entry when
// there is one and appending otherwise.
func setEnv(env []string, key, value string) []string {
	kv := key + "=" + value
	out := make([]string, 0, len(env)+1)
	replaced := false
	for _, e := range env {
		if strings.HasPrefix(e, key+"=") {
			e, replaced = kv, true
		}
		out = append(out, e)
	}
	if !replaced {
		out = append(out, kv)
	}
	return out
}

// proximoRunFailure wraps an infrastructure failure — a binary that would not
// run, a deadline — with the binary and verb that were about to execute. Only
// those: a non-zero exit from proximo itself is data the shim propagates, not
// an error to wrap.
const proximoRunFailure = "run %s %s: %w"

// proximoTimeout bounds a /proximo execution. Far above the shared 5s
// requestTimeout because the first `proximo up` pulls/builds the stack
// images; status/down complete in seconds.
const proximoTimeout = 120 * time.Second

// ErrProximoNotInstalled is returned when no proximo binary resolves on the
// host; the daemon surfaces it verbatim to the in-container shim.
var ErrProximoNotInstalled = errors.New("proximo not installed on host — install it there and run `proximo install` on the host (https://github.com/filippolmt/proximo); bootstrap needs root and is deliberately not bridged")

// proximoFallbackCandidates lists well-known proximo install locations probed
// when PATH lookup fails: the LaunchAgent / systemd user unit running the
// daemon typically has a minimal PATH without /opt/homebrew/bin (brew on
// Apple Silicon) or ~/go/bin (`go install`). An empty home yields no go/bin
// candidate rather than a bogus relative path.
//
// A var so a test can empty the list. The candidates are absolute, so no
// amount of t.Setenv reaches them: wherever one of them happens to exist —
// a host with proximo installed, or this project's own toolbox image, which
// bundles it at /usr/local/bin/proximo and is where the suite runs — the
// binary resolves and launchProximo's refusal branch is unreachable.
var proximoFallbackCandidates = func() []string {
	candidates := []string{
		"/opt/homebrew/bin/proximo",
		"/usr/local/bin/proximo",
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidates = append(candidates, filepath.Join(home, "go", "bin", "proximo"))
	}
	return candidates
}

// proximoChildPathDirs returns bin dirs appended to the child proximo
// process's PATH so its own lookups (docker, docker compose) survive the
// minimal LaunchAgent / systemd-user PATH — the same problem
// resolveProximoBinary solves for the proximo binary itself. binDir (dir of
// the resolved binary) leads; home-relative entries are skipped when home is
// empty.
func proximoChildPathDirs(binDir string) []string {
	dirs := []string{
		binDir,
		"/opt/homebrew/bin",
		"/usr/local/bin",
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		dirs = append(dirs,
			filepath.Join(home, "go", "bin"),
			filepath.Join(home, ".docker", "bin"),
			filepath.Join(home, ".orbstack", "bin"),
		)
	}
	return dirs
}

// appendPathDirs returns env with its PATH entry extended by dirs not already
// present; a PATH entry is added when none exists. Existing entries keep
// priority — dirs are fallbacks, mirroring resolveProximoBinary's "PATH
// first, well-known locations second" order.
func appendPathDirs(env []string, dirs []string) []string {
	if len(dirs) == 0 {
		return env
	}
	pathIdx := -1
	var entries []string
	for i, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			pathIdx = i
			entries = filepath.SplitList(strings.TrimPrefix(kv, "PATH="))
			break
		}
	}
	seen := make(map[string]struct{}, len(entries)+len(dirs))
	for _, e := range entries {
		seen[e] = struct{}{}
	}
	for _, d := range dirs {
		if _, ok := seen[d]; ok {
			continue
		}
		seen[d] = struct{}{}
		entries = append(entries, d)
	}
	kv := "PATH=" + strings.Join(entries, string(os.PathListSeparator))
	if pathIdx < 0 {
		return append(append([]string{}, env...), kv)
	}
	out := append([]string{}, env...)
	out[pathIdx] = kv
	return out
}

// resolveProximoBinary returns the proximo binary to exec: PATH lookup first,
// then the given fallback candidates in order.
func resolveProximoBinary(candidates []string) (string, error) {
	if p, err := exec.LookPath("proximo"); err == nil {
		return p, nil
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return c, nil
		}
	}
	return "", ErrProximoNotInstalled
}

// launchProximo executes one allowlisted proximo subcommand on the host, with
// the caller's arguments appended verbatim, and returns its combined output
// and exit code. A non-zero exit is NOT an error (the shim propagates it); err
// is reserved for infrastructure failures (binary missing, context deadline).
// Direct exec, no shell — argv is passed as a slice, so an argument never
// reaches a word-splitting or globbing context.
func launchProximo(ctx context.Context, command string, args []string, agent proximoAgentHome) (output []byte, exit int, err error) {
	bin, err := resolveProximoBinary(proximoFallbackCandidates())
	if err != nil {
		return nil, 0, err
	}
	// Child PATH augmented so proximo's own lookups (docker, compose) survive
	// the minimal service PATH — see proximoChildPathDirs.
	env := appendPathDirs(os.Environ(), proximoChildPathDirs(filepath.Dir(bin)))
	if command == proximoSkillCommand {
		agentEnv, err := proximoAgentHomeEnv(env, agent)
		if err != nil {
			return nil, 0, fmt.Errorf(proximoRunFailure, bin, command, err)
		}
		args, env = proximoSkillArgs(args), agentEnv
	}
	cmd := exec.CommandContext(ctx, bin, append([]string{command}, args...)...)
	cmd.Stdin = nil
	cmd.Env = env
	// Stop waiting on the stdout/stderr pipes shortly after a deadline kill:
	// proximo's docker-compose children inherit the pipes and would otherwise
	// hold CombinedOutput open past proximoTimeout.
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.CombinedOutput()
	if err != nil {
		// A deadline kill also surfaces as *exec.ExitError — classify it as an
		// infrastructure failure, not a command exit.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return out, 0, fmt.Errorf(proximoRunFailure, bin, command, ctxErr)
		}
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			return out, exitErr.ExitCode(), nil
		}
		return out, 0, fmt.Errorf(proximoRunFailure, bin, command, err)
	}
	return out, 0, nil
}
