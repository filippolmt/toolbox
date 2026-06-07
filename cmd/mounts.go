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
                   ./.toolbox.yaml when none is found`,
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

var mountsDisableWhere string

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

var mountsRemoveWhere string

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

var mountsRootWhere string

var mountsRootCmd = &cobra.Command{
	Use:   "root <path>",
	Short: "Set mounts_root (retargets default mount sources)",
	Long: `Validate and write the top-level mounts_root: key, retargeting every
default mount whose source lives under ~/.toolbox/ to the given prefix.`,
	Example: `  toolbox mounts root ~/encrypted/toolbox
  toolbox mounts root /vault/toolbox --where local`,
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
	mountsAddCmd.Flags().StringVar(&mountsAddWhere, "where", "global", "config file to write: global|local")

	mountsDisableCmd.Flags().StringVar(&mountsDisableWhere, "where", "global", "config file to write: global|local")
	mountsRemoveCmd.Flags().StringVar(&mountsRemoveWhere, "where", "global", "config file to write: global|local")
	mountsRootCmd.Flags().StringVar(&mountsRootWhere, "where", "global", "config file to write: global|local")

	mountsCmd.AddCommand(mountsListCmd)
	mountsCmd.AddCommand(mountsAddCmd)
	mountsCmd.AddCommand(mountsDisableCmd)
	mountsCmd.AddCommand(mountsRemoveCmd)
	mountsCmd.AddCommand(mountsRootCmd)
	rootCmd.AddCommand(mountsCmd)
}

func runMountsList(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()
	if mountsListDefaultsOnly {
		for _, m := range mountplan.Defaults() {
			printMountRow(out, m, "default")
		}
		return nil
	}

	if cfg == nil {
		return errors.New("internal: configuration not loaded")
	}
	resolved, err := mountplan.Merge(cfg)
	if err != nil {
		return err
	}

	defaultNames := map[string]struct{}{}
	for _, m := range mountplan.Defaults() {
		defaultNames[m.Name] = struct{}{}
	}
	userNames := map[string]struct{}{}
	for _, m := range cfg.Mounts {
		if m.Name != "" {
			userNames[m.Name] = struct{}{}
		}
	}

	resolvedNames := map[string]struct{}{}
	for _, m := range resolved {
		if m.Name != "" {
			resolvedNames[m.Name] = struct{}{}
		}
		printMountRow(out, m, classifyMount(m, defaultNames, userNames))
	}

	// Defaults dropped from the resolved set were disabled (by a user patch
	// or a feature toggle); surface them so the view stays complete.
	for _, m := range mountplan.Defaults() {
		if _, ok := resolvedNames[m.Name]; !ok {
			printMountRow(out, m, "disabled")
		}
	}
	return nil
}

func classifyMount(m config.Mount, defaultNames, userNames map[string]struct{}) string {
	_, isDefault := defaultNames[m.Name]
	_, isUser := userNames[m.Name]
	switch {
	case isDefault && isUser:
		return "patched"
	case isDefault:
		return "default"
	default:
		return "user"
	}
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

	targetPath, err := resolveWriteTarget(mountsAddWhere)
	if err != nil {
		return err
	}
	existed := fileExists(targetPath)
	changed, err := configedit.AddMount(targetPath, config.Mount{
		Name:     name,
		Source:   source,
		Target:   target,
		ReadOnly: mountsAddReadonly,
	})
	if err != nil {
		return err
	}
	reportWrite(cmd.OutOrStdout(), targetPath, existed, changed)
	return nil
}

func runMountsDisable(cmd *cobra.Command, args []string) error {
	name := args[0]
	// A {name, disabled: true} patch referencing a name unknown to the merge
	// fails the next config load — validate against defaults + user entries
	// before writing.
	known := knownMountNames()
	if !slices.Contains(known, name) {
		return &usageError{err: fmt.Errorf("unknown mount %q%s",
			name, configedit.DidYouMean(name, known))}
	}

	targetPath, err := resolveWriteTarget(mountsDisableWhere)
	if err != nil {
		return err
	}
	existed := fileExists(targetPath)
	changed, err := configedit.DisableMount(targetPath, name)
	if err != nil {
		return err
	}
	reportWrite(cmd.OutOrStdout(), targetPath, existed, changed)
	return nil
}

func runMountsRemove(cmd *cobra.Command, args []string) error {
	name := args[0]
	targetPath, err := resolveWriteTarget(mountsRemoveWhere)
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

	changed, err := configedit.RemoveMount(targetPath, name)
	if err != nil {
		return err
	}
	reportWrite(cmd.OutOrStdout(), targetPath, true, changed)
	return nil
}

func runMountsRoot(cmd *cobra.Command, args []string) error {
	root := args[0]
	if err := config.ValidateMountsRoot(root); err != nil {
		return &usageError{err: err}
	}

	targetPath, err := resolveWriteTarget(mountsRootWhere)
	if err != nil {
		return err
	}
	existed := fileExists(targetPath)
	changed, err := configedit.SetMountsRoot(targetPath, root)
	if err != nil {
		return err
	}
	reportWrite(cmd.OutOrStdout(), targetPath, existed, changed)
	return nil
}

// knownMountNames returns every name the merge can resolve: the canonical
// defaults plus named user entries from the resolved configuration.
func knownMountNames() []string {
	names := []string{}
	for _, m := range mountplan.Defaults() {
		if m.Name != "" {
			names = append(names, m.Name)
		}
	}
	if cfg != nil {
		for _, m := range cfg.Mounts {
			if m.Name != "" && !slices.Contains(names, m.Name) {
				names = append(names, m.Name)
			}
		}
	}
	slices.Sort(names)
	return names
}
