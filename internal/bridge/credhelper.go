package bridge

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// builtinHelpers are git's in-tree credential helpers: they ship with git
// itself, so a configured value here never needs a git-credential-<name>
// binary on PATH.
var builtinHelpers = map[string]bool{"store": true, "cache": true}

// gitQueryTimeout bounds the read-only git queries below. Both are local and
// instant in practice; the budget only keeps a wedged git (a config file on a
// stalled network filesystem) from hanging `toolbox bridge install`.
const gitQueryTimeout = 5 * time.Second

// CheckHostCredentialHelper reports whether the host git can persist HTTPS
// credentials — the prerequisite for the bridge to forward them into the
// container (see credential.go). It returns ok=false plus a human advice line
// when no credential.helper is configured, or when a configured plain-name
// helper's git-credential-<name> binary cannot be found where git looks for it
// (the libsecret-not-installed case). Never fatal: git absent or erroring is
// treated as "no helper configured".
func CheckHostCredentialHelper() (ok bool, advice string) {
	return checkHostCredentialHelper(gitOutput, runtime.GOOS, exec.LookPath)
}

// checkHostCredentialHelper is the injectable whole: git, GOOS and the PATH
// lookup arrive as parameters, so the wiring is unit-testable and not just the
// two pure halves it composes. Resolving a helper through git's exec-path
// before PATH is the whole point of the check (see lookHelperIn), so that
// composition belongs inside the tested seam rather than above it.
func checkHostCredentialHelper(git func(...string) string, goos string, lookPath func(string) (string, error)) (ok bool, advice string) {
	helpers := parseHelperList(git("config", "--get-all", "credential.helper"))
	execPath := git("--exec-path")
	return evaluateCredentialHelpers(helpers, goos, func(bin string) (string, error) {
		return lookHelperIn(execPath, bin, lookPath)
	})
}

// gitOutput runs a read-only git query and returns its trimmed stdout, or ""
// when git is absent, errors, or outruns gitQueryTimeout. Every caller here
// reads "" as "git told us nothing" rather than as a failure, so one shape
// serves every query.
func gitOutput(args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), gitQueryTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// parseHelperList splits `git config --get-all credential.helper` output into
// one helper value per line, dropping blanks.
func parseHelperList(out string) []string {
	var helpers []string
	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		if h := strings.TrimSpace(line); h != "" {
			helpers = append(helpers, h)
		}
	}
	return helpers
}

// evaluateCredentialHelpers is the pure decision core; host calls (GOOS,
// helper lookup) are injected so it is unit-testable without touching the
// environment. A plain-name helper (not starting with '!' or '/', not a git
// built-in) requires a git-credential-<name> binary that git can resolve.
func evaluateCredentialHelpers(helpers []string, goos string, lookHelper func(string) (string, error)) (ok bool, advice string) {
	if len(helpers) == 0 {
		return false, "no git credential.helper configured on the host; HTTPS git credentials won't persist and toolbox will keep prompting. " + configureHint(goos)
	}
	for _, h := range helpers {
		// git treats a helper value that is neither a shell command (`!…`) nor
		// an absolute path as `git-credential-<first-word>`, so a plain helper
		// may carry arguments (e.g. `store --file=~/x`). Classify and look up
		// on the first whitespace-delimited token, not the whole string.
		name := h
		if i := strings.IndexAny(h, " \t"); i >= 0 {
			name = h[:i]
		}
		if strings.HasPrefix(name, "!") || strings.HasPrefix(name, "/") || builtinHelpers[name] {
			continue
		}
		if _, err := lookHelper("git-credential-" + name); err != nil {
			return false, fmt.Sprintf("git credential.helper %q is configured but git-credential-%s is not installed (not in git's exec-path, nor on PATH); install it so credentials persist (see docs/bridge.md).", h, name)
		}
	}
	return true, ""
}

// lookHelperIn resolves a git-credential-<name> binary the way git itself
// does: an executable file of that name inside execPath (`git --exec-path`,
// the dir git prepends to PATH for its own subcommands) wins, otherwise the
// lookup falls back to PATH. An empty execPath — git absent or erroring —
// degrades to the plain PATH lookup.
//
// A PATH-only lookup is a false negative on macOS: both Apple git and Homebrew
// git ship git-credential-osxkeychain under libexec/git-core and never in a
// bin/ dir on PATH, so every macOS host was told to "install it" for a helper
// that already worked. What matters is that `git credential fill`
// (runHostCredential in credential.go) can reach the helper, and git resolves
// it through its exec-path.
func lookHelperIn(execPath, bin string, lookPath func(string) (string, error)) (string, error) {
	if execPath != "" {
		p := filepath.Join(execPath, bin)
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0 {
			return p, nil
		}
	}
	return lookPath(bin)
}

// configureHint is the OS-aware remediation for a missing credential.helper.
func configureHint(goos string) string {
	switch goos {
	case "darwin":
		return "Run: git config --global credential.helper osxkeychain"
	case "linux":
		return "Install git-credential-libsecret, then run: git config --global credential.helper libsecret (see docs/bridge.md)"
	default:
		return "Configure a git credential.helper (see docs/bridge.md)"
	}
}
