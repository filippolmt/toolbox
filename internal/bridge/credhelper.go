package bridge

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// builtinHelpers are git's in-tree credential helpers: they ship with git
// itself, so a configured value here never needs a git-credential-<name>
// binary on PATH.
var builtinHelpers = map[string]bool{"store": true, "cache": true}

// CheckHostCredentialHelper reports whether the host git can persist HTTPS
// credentials — the prerequisite for the bridge to forward them into the
// container (see credential.go). It returns ok=false plus a human advice line
// when no credential.helper is configured, or when a configured plain-name
// helper's git-credential-<name> binary is missing from PATH (the
// libsecret-not-installed case). Never fatal: git absent or erroring is
// treated as "no helper configured".
func CheckHostCredentialHelper() (ok bool, advice string) {
	var helpers []string
	if out, err := exec.Command("git", "config", "--get-all", "credential.helper").Output(); err == nil {
		for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
			if h := strings.TrimSpace(line); h != "" {
				helpers = append(helpers, h)
			}
		}
	}
	return evaluateCredentialHelpers(helpers, runtime.GOOS, exec.LookPath)
}

// evaluateCredentialHelpers is the pure decision core; host calls (GOOS,
// PATH lookup) are injected so it is unit-testable without touching the
// environment. A plain-name helper (not starting with '!' or '/', not a git
// built-in) requires a git-credential-<name> binary on PATH.
func evaluateCredentialHelpers(helpers []string, goos string, lookPath func(string) (string, error)) (ok bool, advice string) {
	if len(helpers) == 0 {
		return false, "no git credential.helper configured on the host; HTTPS git credentials won't persist and toolbox will keep prompting. " + configureHint(goos)
	}
	for _, h := range helpers {
		if strings.HasPrefix(h, "!") || strings.HasPrefix(h, "/") || builtinHelpers[h] {
			continue
		}
		if _, err := lookPath("git-credential-" + h); err != nil {
			return false, fmt.Sprintf("git credential.helper %q is configured but git-credential-%s is not on PATH; install it so credentials persist (see docs/bridge.md).", h, h)
		}
	}
	return true, ""
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
