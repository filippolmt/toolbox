package cmd

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/configedit"
	"github.com/filippolmt/toolbox/internal/fsx"
	"github.com/filippolmt/toolbox/internal/workspace"
)

var shellsCmd = &cobra.Command{
	Use:   "shells",
	Short: "Manage named shell shortcuts (shells: block)",
	Long: `Manage the shells: block of .toolbox.yaml from the command line.

Read commands (list, get) consume the fully-resolved configuration — the
same precedence as toolbox shell. Write commands (add, set, remove) edit one
file in place, preserving comments and key order, and target it via --where:
  --where global   ~/.toolbox.yaml (default)
  --where local    the walked-up project .toolbox.yaml, creating
                   ./.toolbox.yaml when none is found

--dry-run prints the file the write would produce — same validation, nothing
touched on disk.`,
	Args: usageArgs(cobra.NoArgs),
}

var shellsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List configured named shells",
	Args:  usageArgs(cobra.NoArgs),
	RunE:  runShellsList,
}

var shellsGetCmd = &cobra.Command{
	Use:   "get <name>",
	Short: "Show one named shell's resolved entry",
	Args:  usageArgs(cobra.ExactArgs(1)),
	RunE:  runShellsGet,
}

var (
	shellsAddPath      string
	shellsAddCreateDir bool
	shellsAddEnv       []string
	shellsAddWhere     string
	shellsAddDryRun    bool
)

var shellsAddCmd = &cobra.Command{
	Use:   "add <name> --path <dir>",
	Short: "Add (or replace) a named shell",
	Long: `Write shells.<name>.path (and optional env overlay) to the --where-resolved
config file, preserving comments and sibling keys.`,
	Example: `  toolbox shells add infra --path ~/work/infra
  toolbox shells add qa --path /tmp/qa --create-dir --env DEBUG=1 --where local`,
	Args: usageArgs(cobra.ExactArgs(1)),
	RunE: runShellsAdd,
}

var (
	shellsSetEnv    []string
	shellsSetWhere  string
	shellsSetDryRun bool
)

var shellsSetCmd = &cobra.Command{
	Use:   "set <name> --env K=V",
	Short: "Set env overlay keys on a named shell",
	Long: `Upsert shells.<name>.env entries in the --where-resolved config file.
Reserved keys (the TOOLBOX_ prefix and PWD) are rejected before writing.`,
	Example: `  toolbox shells set infra --env AWS_PROFILE=staging
  toolbox shells set infra --env FOO=bar --env BAZ=qux --where local`,
	Args: usageArgs(cobra.ExactArgs(1)),
	RunE: runShellsSet,
}

var (
	shellsRemovePurge  bool
	shellsRemoveWhere  string
	shellsRemoveDryRun bool
)

var shellsRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove a named shell entry",
	Long: `Delete shells.<name> from the --where-resolved config file. With
--purge-dir the configured path directory is removed from the host too.`,
	Example: `  toolbox shells remove infra
  toolbox shells remove qa --purge-dir --where local`,
	Args: usageArgs(cobra.ExactArgs(1)),
	RunE: runShellsRemove,
}

func init() {
	shellsAddCmd.Flags().StringVar(&shellsAddPath, "path", "", "host directory the shell opens in (required)")
	_ = shellsAddCmd.MarkFlagRequired("path")
	shellsAddCmd.Flags().BoolVar(&shellsAddCreateDir, "create-dir", false, "create the path directory when missing")
	shellsAddCmd.Flags().StringArrayVar(&shellsAddEnv, "env", nil, "env overlay entry K=V (repeatable)")
	registerWriteFlags(shellsAddCmd, &shellsAddWhere, &shellsAddDryRun)

	shellsSetCmd.Flags().StringArrayVar(&shellsSetEnv, "env", nil, "env overlay entry K=V (repeatable, required)")
	registerWriteFlags(shellsSetCmd, &shellsSetWhere, &shellsSetDryRun)

	shellsRemoveCmd.Flags().BoolVar(&shellsRemovePurge, "purge-dir", false, "also remove the configured path directory")
	registerWriteFlags(shellsRemoveCmd, &shellsRemoveWhere, &shellsRemoveDryRun)

	shellsCmd.AddCommand(shellsListCmd)
	shellsCmd.AddCommand(shellsGetCmd)
	shellsCmd.AddCommand(shellsAddCmd)
	shellsCmd.AddCommand(shellsSetCmd)
	shellsCmd.AddCommand(shellsRemoveCmd)
	rootCmd.AddCommand(shellsCmd)
}

func runShellsList(cmd *cobra.Command, _ []string) error {
	if cfg == nil {
		return errConfigNotLoaded
	}
	out := cmd.OutOrStdout()
	if len(cfg.Shells) == 0 {
		_, _ = fmt.Fprintln(out, "no named shells configured (toolbox shells add <name> --path <dir>)")
		return nil
	}
	width := 0
	names := slices.Sorted(maps.Keys(cfg.Shells))
	for _, n := range names {
		width = max(width, len(n))
	}
	for _, n := range names {
		_, _ = fmt.Fprintf(out, "%-*s  %s\n", width, n, cfg.Shells[n].Path)
	}
	return nil
}

// shellFileKey returns the key the write commands must edit under shells: in
// the file at target — configedit.ShellKeyIn's rule applied to the keys that
// file currently carries.
func shellFileKey(target, name string) (string, error) {
	fileShells, err := configedit.UserShells(target)
	if err != nil {
		return "", err
	}
	return configedit.ShellKeyIn(slices.Sorted(maps.Keys(fileShells)), name), nil
}

func runShellsGet(cmd *cobra.Command, args []string) error {
	if cfg == nil {
		return errConfigNotLoaded
	}
	name := args[0]
	s, ok := cfg.Shells[config.NormalizeShellKey(name)]
	if !ok {
		return &usageError{err: fmt.Errorf("unknown shell %q%s",
			name, configedit.DidYouMean(name, slices.Sorted(maps.Keys(cfg.Shells))))}
	}
	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(out, "path: %s\n", s.Path)
	if len(s.Env) > 0 {
		_, _ = fmt.Fprintln(out, "env:")
		for _, k := range slices.Sorted(maps.Keys(s.Env)) {
			_, _ = fmt.Fprintf(out, "  %s: %s\n", k, s.Env[k])
		}
	}
	return nil
}

func runShellsAdd(cmd *cobra.Command, args []string) error {
	name := args[0]
	if _, err := validateShellName(name); err != nil {
		return &usageError{err: err}
	}

	home, _ := os.UserHomeDir()
	path := fsx.ExpandTilde(strings.TrimSpace(shellsAddPath), home)
	if err := workspace.ValidateAbsolute(path); err != nil {
		return err
	}
	env, err := parseEnvPairs(shellsAddEnv)
	if err != nil {
		return err
	}

	// --create-dir is a host-side effect, so a dry run must not perform it: it
	// says what the real run would create and leaves the disk alone. A missing
	// directory is a doctor warning, never an error, so the preview is still
	// the candidate the write would produce.
	if shellsAddCreateDir {
		if shellsAddDryRun {
			reportSkippedSideEffect(cmd.ErrOrStderr(), "--create-dir would create %s", path)
		} else if err := os.MkdirAll(path, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", path, err)
		}
	}

	target, cwd, err := resolveWriteTarget(shellsAddWhere)
	if err != nil {
		return err
	}
	key, err := shellFileKey(target, name)
	if err != nil {
		return err
	}
	return applyOrPreview(cmd.OutOrStdout(), target, cwd, shellsAddDryRun,
		configedit.Shell(key, path, env))
}

func runShellsSet(cmd *cobra.Command, args []string) error {
	name := args[0]
	if len(shellsSetEnv) == 0 {
		return &usageError{err: errors.New("set requires at least one --env K=V")}
	}
	env, err := parseEnvPairs(shellsSetEnv)
	if err != nil {
		return err
	}
	if cfg == nil {
		return errConfigNotLoaded
	}
	if _, ok := cfg.Shells[config.NormalizeShellKey(name)]; !ok {
		return &usageError{err: fmt.Errorf("unknown shell %q%s",
			name, configedit.DidYouMean(name, slices.Sorted(maps.Keys(cfg.Shells))))}
	}

	target, cwd, err := resolveWriteTarget(shellsSetWhere)
	if err != nil {
		return err
	}
	key, err := shellFileKey(target, name)
	if err != nil {
		return err
	}
	return applyOrPreview(cmd.OutOrStdout(), target, cwd, shellsSetDryRun,
		configedit.ShellEnv(key, env))
}

func runShellsRemove(cmd *cobra.Command, args []string) error {
	name := args[0]
	target, cwd, err := resolveWriteTarget(shellsRemoveWhere)
	if err != nil {
		return err
	}

	fileShells, err := configedit.UserShells(target)
	if err != nil {
		return err
	}
	key, err := shellFileKey(target, name)
	if err != nil {
		return err
	}
	entry, ok := fileShells[key]
	if !ok {
		return &usageError{err: fmt.Errorf("shell %q not found in %s%s",
			name, target, configedit.DidYouMean(name, slices.Sorted(maps.Keys(fileShells))))}
	}

	out := cmd.OutOrStdout()
	if err := applyOrPreview(out, target, cwd, shellsRemoveDryRun, configedit.RemoveShell(key)); err != nil {
		return err
	}

	if !shellsRemovePurge {
		return nil
	}
	// Deleting a directory is the one thing about this command a dry run could
	// not take back, so it is named rather than done.
	if shellsRemoveDryRun {
		reportSkippedSideEffect(cmd.ErrOrStderr(), "--purge-dir would remove %q", entry)
		return nil
	}
	return purgeShellDir(out, entry)
}

// purgeShellDir removes the configured shell directory. Symlinks and
// non-directories are refused — --purge-dir deletes only what `--create-dir`
// could have created.
func purgeShellDir(out io.Writer, path string) error {
	if strings.TrimSpace(path) == "" {
		_, _ = fmt.Fprintln(out, "  nothing to purge (entry had no path)")
		return nil
	}
	home, _ := os.UserHomeDir()
	expanded := fsx.ExpandTilde(path, home)
	info, err := os.Lstat(expanded)
	switch {
	case os.IsNotExist(err):
		_, _ = fmt.Fprintf(out, "  %s: already absent\n", expanded)
		return nil
	case err != nil:
		return fmt.Errorf("stat %s: %w", expanded, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("refusing to purge %s: not a directory", expanded)
	}
	if err := os.RemoveAll(expanded); err != nil {
		return fmt.Errorf("purge %s: %w", expanded, err)
	}
	_, _ = fmt.Fprintf(out, "  %s: removed\n", expanded)
	return nil
}

// parseEnvPairs parses repeated --env K=V flags into a validated map.
func parseEnvPairs(pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	env := make(map[string]string, len(pairs))
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		if !ok {
			return nil, &usageError{err: fmt.Errorf("invalid --env %q: must be K=V", p)}
		}
		env[k] = v
	}
	if err := config.ValidateEnv(env); err != nil {
		return nil, &usageError{err: err}
	}
	return env, nil
}
