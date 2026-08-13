package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/term"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/configedit"
	"github.com/filippolmt/toolbox/internal/configio"
	"github.com/filippolmt/toolbox/internal/sessionplan"
	"github.com/filippolmt/toolbox/internal/workspace"
)

var shellStdinIsTerminal = term.IsTerminal

// shellHashLikeRe matches names that look like the trailing hash of the
// workspace container format (`toolbox-<base>-<8hex>`). Refused so a named
// shell cannot impersonate a workspace-derived container.
var shellHashLikeRe = regexp.MustCompile(
	fmt.Sprintf(`^[a-f0-9]{%d}$`, sessionplan.WorkspaceHashLen),
)

// resolveShellWorkspace returns the workspace path and (for named shells)
// the shell name as the user typed it. The raw form is what travels on to
// sessionplan.PlanInput.Name, which owns both derivations — the sanitized
// container suffix and the normalized cfg.Shells key. The sanitized form
// stays local: validateShellName needs it for the collision and length
// checks, and ensureNamedShellPath for its error strings.
//
// The positional argument is interpreted as:
//   - absent           -> workspace.Resolve() (current working directory)
//   - absolute path    -> direct workspace (no config touched, container
//     name derives from the path hash, same as the no-arg flow)
//   - anything else    -> named-shell lookup in cfg.Shells, with bootstrap
//     when missing
//
// The absolute-path form is the quick-session escape hatch
// (`toolbox shell /tmp`) — zero config, zero state.
func resolveShellWorkspace(args []string, create bool, createPath string) (string, string, error) {
	if len(args) == 0 {
		ws, err := workspace.Resolve()
		return ws, "", err
	}

	if filepath.IsAbs(args[0]) {
		return resolveDirectWorkspace(args[0])
	}

	name := args[0]
	sanitized, err := validateShellName(name)
	if err != nil {
		return "", "", err
	}

	path, configured, err := shellPathFor(name)
	if err != nil {
		return "", "", err
	}
	if !configured {
		return bootstrapMissingNamedShell(name, sanitized, create, createPath)
	}
	if path == "" {
		return "", "", fmt.Errorf("shell %q has empty path", name)
	}

	ws, err := ensureNamedShellPath(sanitized, path, create)
	if err != nil {
		return "", "", err
	}
	return ws, name, nil
}

// resolveDirectWorkspace validates an absolute path supplied as the shell
// positional and returns it as the workspace. No config is touched, no
// name is returned — downstream sessionplan.Plan will derive the container
// name from the path hash, matching the no-arg `toolbox shell` flow.
// workspace.ResolveExplicit (not Lstat-based) is intentional: the user
// explicitly typed the path, so a one-off symlink follow is the right
// trade — the named-shell flow uses Lstat because that path is config-
// supplied and persists across invocations, widening the TOCTOU window.
func resolveDirectWorkspace(path string) (string, string, error) {
	clean, err := workspace.ResolveExplicit(path)
	if err != nil {
		return "", "", err
	}
	return clean, "", nil
}

// validateShellName returns the sanitized form on success and rejects
// inputs that would either yield an invalid container suffix or collide
// with the workspace-hash naming format (`toolbox-<base>-<8hex>`).
func validateShellName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", errors.New("shell name must not be empty")
	}
	sanitized := sessionplan.SanitizeShellName(trimmed)
	if sanitized == "" {
		return "", fmt.Errorf("shell name %q has no allowed characters (use [a-z0-9-])", name)
	}
	if shellHashLikeRe.MatchString(sanitized) {
		return "", fmt.Errorf("shell name %q is ambiguous with the workspace-hash container suffix; pick a different name", name)
	}
	if len(sanitized) > sessionplan.MaxNamedShellNameLen {
		return "", fmt.Errorf("shell name %q is too long (max %d sanitized chars)", name, sessionplan.MaxNamedShellNameLen)
	}
	return sanitized, nil
}

func shellPathFor(name string) (string, bool, error) {
	if cfg == nil {
		return "", false, errors.New("internal: configuration not loaded")
	}
	if cfg.Shells == nil {
		return "", false, nil
	}
	s, ok := cfg.Shells[config.NormalizeShellKey(name)]
	if !ok {
		return "", false, nil
	}
	return strings.TrimSpace(s.Path), true, nil
}

func bootstrapMissingNamedShell(name, sanitized string, create bool, createPath string) (string, string, error) {
	home, _ := os.UserHomeDir()
	path := defaultShellPath(home, name)
	if createPath != "" {
		path = createPath
	}

	if create {
		if err := upsertShellInUserConfig(home, name, path); err != nil {
			return "", "", err
		}
		ws, err := ensureNamedShellPath(sanitized, path, true)
		if err != nil {
			return "", "", err
		}
		return ws, name, nil
	}

	if !shellStdinIsTerminal(int(os.Stdin.Fd())) {
		return "", "", errors.New(missingShellHint(home, name))
	}

	reader := bufio.NewReader(os.Stdin)
	chosenPath, err := promptPath(reader, os.Stderr, name, path)
	if err != nil {
		return "", "", err
	}

	createDir, err := promptYesNo(reader, os.Stderr, "  create directory?", true)
	if err != nil {
		return "", "", err
	}
	addConfig, err := promptYesNo(reader, os.Stderr, "  add to ~/.toolbox.yaml?", true)
	if err != nil {
		return "", "", err
	}

	if addConfig {
		if err := upsertShellInUserConfig(home, name, chosenPath); err != nil {
			return "", "", err
		}
	}
	ws, err := ensureNamedShellPath(sanitized, chosenPath, createDir)
	if err != nil {
		return "", "", err
	}
	return ws, name, nil
}

// ensureNamedShellPath validates (and optionally creates) the configured
// directory and returns it as the workspace. sanitized is only what the error
// strings name the shell by; the caller owns the name it hands back.
func ensureNamedShellPath(sanitized, path string, createDir bool) (string, error) {
	if err := workspace.ValidateAbsolute(path); err != nil {
		return "", err
	}

	info, err := os.Lstat(path)
	switch {
	case err == nil:
	case os.IsNotExist(err):
		if !createDir {
			return "", errors.New(missingPathHint(sanitized, path))
		}
		if mkErr := os.MkdirAll(path, 0o755); mkErr != nil {
			return "", fmt.Errorf("create %s: %w", path, mkErr)
		}
		info, err = os.Lstat(path)
		if err != nil {
			return "", fmt.Errorf("stat %s: %w", path, err)
		}
	default:
		return "", fmt.Errorf("stat %s: %w", path, err)
	}

	// A symlink at the final element is refused: a TOCTOU swap between
	// this check and the Docker bind-mount stage would redirect the
	// container's mount source to an attacker-controlled target.
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("shell %q path %s is a symlink; point shells.%s.path at the resolved target directly", sanitized, path, sanitized)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("shell %q path %s is not a directory", sanitized, path)
	}
	return path, nil
}

// defaultShellPath returns the auto-bootstrap path for a missing named
// shell. Prefers $HOME/toolbox-shells/<name> for persistence across reboots
// and to avoid the world-writable / tmpfs semantics of /tmp; falls back to
// /tmp/<name> only when home cannot be resolved.
//
// The directory is named after the canonical key, not the spelling typed at the
// prompt, so `toolbox shell Infra --create` and `toolbox shell infra --create`
// cannot bootstrap the one config entry they share to two different paths.
func defaultShellPath(home, name string) string {
	name = config.NormalizeShellKey(name)
	if home == "" {
		return filepath.Join("/tmp", name)
	}
	return filepath.Join(home, "toolbox-shells", name)
}

// missingShellHint is the copy-pasteable block printed when a named shell is
// absent from the config. The first line echoes the name as typed; the YAML and
// the --create command use the canonical key, so following the hint by hand
// produces the same entry the bootstrap would have written.
func missingShellHint(home, name string) string {
	key := config.NormalizeShellKey(name)
	path := defaultShellPath(home, name)
	return fmt.Sprintf(`shell %q not configured

Add to ~/.toolbox.yaml:

  shells:
    %s:
      path: %s

%s`, name, key, path, createHint(key, path))
}

func missingPathHint(name, path string) string {
	return fmt.Sprintf(`path %s does not exist

%s`, path, createHint(name, path))
}

// createHint composes the shared `mkdir -p` + `--create` tail used by both
// the missing-shell and missing-path error messages. Keeps the two
// templates in lockstep so a flag rename only edits one literal.
func createHint(name, path string) string {
	return fmt.Sprintf(`Create the directory:

  mkdir -p %s

Or re-run with auto-create:

  toolbox shell %s --create`, path, name)
}

func promptPath(r *bufio.Reader, w io.Writer, name, defaultPath string) (string, error) {
	_, _ = fmt.Fprintf(w, "shell %q not configured.\n\n", name)
	_, _ = fmt.Fprintf(w, "  path [%s]: ", defaultPath)
	line, err := r.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return defaultPath, nil
	}
	return line, nil
}

func promptYesNo(r *bufio.Reader, w io.Writer, label string, defaultYes bool) (bool, error) {
	suffix := "[y/N]"
	if defaultYes {
		suffix = "[Y/n]"
	}
	_, _ = fmt.Fprintf(w, "%s %s ", label, suffix)
	line, err := r.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	line = strings.ToLower(strings.TrimSpace(line))
	if line == "" {
		return defaultYes, nil
	}
	switch line {
	case "y", "yes":
		return true, nil
	case "n", "no":
		return false, nil
	default:
		return defaultYes, nil
	}
}

// upsertShellInUserConfig writes shells.<name>.path to ~/.toolbox.yaml through
// configedit.SetShell — the same writer `toolbox shells add` uses, so the
// --create bootstrap inherits the docs header on file creation and the doctor
// gate, instead of open-coding the node edit. home is resolved once by the
// caller and threaded in so the --create path does not pay for repeated
// os.UserHomeDir() lookups. The key is resolved through shellFileKey, so
// bootstrapping `toolbox shell Infra --create` writes the same canonical key
// the loader will look the shell up under.
func upsertShellInUserConfig(home, name, path string) error {
	if home == "" {
		h, err := configio.GlobalConfigDir()
		if err != nil {
			return err
		}
		home = h
	}
	cfgPath := filepath.Join(home, ".toolbox.yaml")

	key, err := shellFileKey(cfgPath, name)
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve cwd: %w", err)
	}
	_, err = configedit.SetShell(cfgPath, cwd, key, path)
	return err
}
