package cmd

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/configedit"
	"github.com/filippolmt/toolbox/internal/mountplan"
	"github.com/filippolmt/toolbox/internal/proximo"
)

var mountsCmd = &cobra.Command{
	Use:   "mounts",
	Short: "Manage bind-mount entries (mounts: / mounts_root:)",
	Long: `Manage the mounts: list and mounts_root: key of .toolbox.yaml from the
command line, mirroring the merge semantics of the load path: add writes the
replace/append form, disable writes a {name, disabled: true} patch, remove
deletes a user entry (defaults can only be disabled, never deleted).

Write commands accept --where:
  --where global   ~/.toolbox.yaml (default)
  --where local    the walked-up project .toolbox.yaml, creating
                   ./.toolbox.yaml when none is found

--dry-run prints the file the write would produce — same validation, nothing
touched on disk.`,
	Args: usageArgs(cobra.NoArgs),
}

var mountsListDefaultsOnly bool

var mountsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the effective mount set with classification",
	Long: `Print the resolved mount set (defaults retargeted by mounts_root, then
patched/replaced/appended/disabled by mounts:). Each row is classified as
(default), (patched), (disabled), or (user).`,
	Args: usageArgs(cobra.NoArgs),
	RunE: runMountsList,
}

var (
	mountsAddSource   string
	mountsAddTarget   string
	mountsAddReadonly bool
	mountsAddWhere    string
	mountsAddDryRun   bool
)

var mountsAddCmd = &cobra.Command{
	Use:   "add <name> --source <host-path> --target <container-path>",
	Short: "Add (or replace) a mount entry",
	Long: `Write a replace/append-form entry (name + source + target, optional
readonly) to the --where-resolved config file. A name matching a default
replaces that default entirely; any other name appends a user mount.`,
	Example: `  toolbox mounts add scratch --source ~/scratch --target /scratch --readonly
  toolbox mounts add scratch --source ~/scratch --target /scratch --where local`,
	Args: usageArgs(cobra.ExactArgs(1)),
	RunE: runMountsAdd,
}

var (
	mountsDisableWhere  string
	mountsDisableDryRun bool
)

var mountsDisableCmd = &cobra.Command{
	Use:   "disable <name>",
	Short: "Disable a mount by name",
	Long: `Write a {name, disabled: true} patch to the --where-resolved config file,
removing the named mount (default or user) from the resolved set.`,
	Example: `  toolbox mounts disable docker-sock
  toolbox mounts disable scratch --where local`,
	Args: usageArgs(cobra.ExactArgs(1)),
	RunE: runMountsDisable,
}

var (
	mountsRemoveWhere  string
	mountsRemoveDryRun bool
)

var mountsRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove a user mount entry",
	Long: `Delete a user-declared mounts: entry from the --where-resolved config
file. Default mounts are not stored in the file and can only be disabled
(toolbox mounts disable <name>), never deleted.`,
	Example: `  toolbox mounts remove scratch
  toolbox mounts remove scratch --where local`,
	Args: usageArgs(cobra.ExactArgs(1)),
	RunE: runMountsRemove,
}

var (
	mountsRootWhere  string
	mountsRootDryRun bool
)

var mountsRootCmd = &cobra.Command{
	Use:   "root <path>",
	Short: "Set mounts_root (retargets default mount sources)",
	Long: `Validate and write the top-level mounts_root: key, retargeting every
default mount whose source lives under ~/.toolbox/ to the given prefix.

An empty path resets the retarget: the key is removed from the file rather
than written as an override that means "no override", so the defaults go back
to ~/.toolbox/ and whichever lower layer still sets mounts_root takes over.`,
	Example: `  toolbox mounts root ~/encrypted/toolbox
  toolbox mounts root /vault/toolbox --where local
  toolbox mounts root ""   # reset: drop the key from the file`,
	Args: usageArgs(cobra.ExactArgs(1)),
	RunE: runMountsRoot,
}

func init() {
	mountsListCmd.Flags().BoolVar(&mountsListDefaultsOnly, "defaults-only", false, "show only the canonical default mount set")

	mountsAddCmd.Flags().StringVar(&mountsAddSource, "source", "", "host path to bind (required)")
	_ = mountsAddCmd.MarkFlagRequired("source")
	mountsAddCmd.Flags().StringVar(&mountsAddTarget, "target", "", "container path to bind to (required)")
	_ = mountsAddCmd.MarkFlagRequired("target")
	mountsAddCmd.Flags().BoolVar(&mountsAddReadonly, "readonly", false, "mount read-only")
	registerWriteFlags(mountsAddCmd, &mountsAddWhere, &mountsAddDryRun)

	registerWriteFlags(mountsDisableCmd, &mountsDisableWhere, &mountsDisableDryRun)
	registerWriteFlags(mountsRemoveCmd, &mountsRemoveWhere, &mountsRemoveDryRun)
	registerWriteFlags(mountsRootCmd, &mountsRootWhere, &mountsRootDryRun)

	mountsCmd.AddCommand(mountsListCmd)
	mountsCmd.AddCommand(mountsAddCmd)
	mountsCmd.AddCommand(mountsDisableCmd)
	mountsCmd.AddCommand(mountsRemoveCmd)
	mountsCmd.AddCommand(mountsRootCmd)
	rootCmd.AddCommand(mountsCmd)
}

func runMountsList(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()
	defaults := mountplan.Defaults()
	if mountsListDefaultsOnly {
		for _, m := range defaults {
			printMountRow(out, m, "default")
		}
		return nil
	}

	if cfg == nil {
		return errConfigNotLoaded
	}
	host := hostBestEffort()
	classified, err := mountplan.Classify(host, cfg, proximo.Resolve(host, cfg))
	if err != nil {
		return err
	}
	for _, m := range classified {
		printMountRow(out, m.Mount, m.Origin.String())
	}
	return nil
}

func printMountRow(w io.Writer, m config.Mount, class string) {
	name := m.Name
	if name == "" {
		name = "(anonymous)"
	}
	flags := ""
	if m.ReadOnly {
		flags = " [ro]"
	}
	_, _ = fmt.Fprintf(w, "%-16s %s -> %s%s (%s)\n", name, m.Source, m.Target, flags, class)
}

func runMountsAdd(cmd *cobra.Command, args []string) error {
	name := args[0]
	source := strings.TrimSpace(mountsAddSource)
	target := strings.TrimSpace(mountsAddTarget)
	if name == "" {
		return &usageError{err: errors.New("mount name must not be empty")}
	}
	if source == "" || target == "" {
		return &usageError{err: errors.New("--source and --target must not be empty")}
	}

	targetPath, cwd, err := resolveWriteTarget(mountsAddWhere)
	if err != nil {
		return err
	}
	return applyOrPreview(cmd.OutOrStdout(), targetPath, cwd, mountsAddDryRun,
		configedit.Mount(config.Mount{
			Name:     name,
			Source:   source,
			Target:   target,
			ReadOnly: mountsAddReadonly,
		}))
}

func runMountsDisable(cmd *cobra.Command, args []string) error {
	name := args[0]
	if cfg == nil {
		return errConfigNotLoaded
	}
	// A {name, disabled: true} patch referencing a name unknown to the merge
	// fails the next config load — validate against defaults + user entries
	// before writing.
	host := hostBestEffort()
	known, err := mountplan.Names(host, cfg, proximo.Resolve(host, cfg))
	if err != nil {
		return err
	}
	if !slices.Contains(known, name) {
		return &usageError{err: fmt.Errorf("unknown mount %q%s",
			name, configedit.DidYouMean(name, known))}
	}

	targetPath, cwd, err := resolveWriteTarget(mountsDisableWhere)
	if err != nil {
		return err
	}
	return applyOrPreview(cmd.OutOrStdout(), targetPath, cwd, mountsDisableDryRun,
		configedit.MountDisabled(name))
}

func runMountsRemove(cmd *cobra.Command, args []string) error {
	name := args[0]
	targetPath, cwd, err := resolveWriteTarget(mountsRemoveWhere)
	if err != nil {
		return err
	}

	userNames, err := configedit.UserMountNames(targetPath)
	if err != nil {
		return err
	}
	if !slices.Contains(userNames, name) {
		for _, m := range mountplan.Defaults() {
			if m.Name == name {
				return fmt.Errorf(
					"%q is a default mount and cannot be deleted; disable it instead: toolbox mounts disable %s",
					name, name)
			}
		}
		return &usageError{err: fmt.Errorf("mount %q not found in %s%s",
			name, targetPath, configedit.DidYouMean(name, userNames))}
	}

	return applyOrPreview(cmd.OutOrStdout(), targetPath, cwd, mountsRemoveDryRun,
		configedit.RemoveMount(name))
}

func runMountsRoot(cmd *cobra.Command, args []string) error {
	root := args[0]
	if err := config.ValidateKey("mounts_root", root); err != nil {
		return &usageError{err: err}
	}

	targetPath, cwd, err := resolveWriteTarget(mountsRootWhere)
	if err != nil {
		return err
	}
	// mounts_root is an ordinary top-level scalar, so it takes the same
	// mutation every other one does — empty value included, which removes the
	// key rather than persisting an override that means "no override".
	// TestMountsRootEmptyValueRemovesTheKey holds that reset at this surface.
	return applyOrPreview(cmd.OutOrStdout(), targetPath, cwd, mountsRootDryRun,
		configedit.Scalar("mounts_root", root))
}
