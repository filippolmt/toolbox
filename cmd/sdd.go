package cmd

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/filippolmt/toolbox/internal/configedit"
	"github.com/filippolmt/toolbox/internal/sdd"
)

var sddCmd = &cobra.Command{
	Use:   "sdd",
	Short: "Manage repo-local Spec-Driven-Development integrations",
	Long: `Wire the current repository for one or more SDD skill packs.

Each integration is a per-repo opt-in keyed by sdd.<name> in .toolbox.yaml.
On the next 'toolbox shell' the entrypoint installs the pinned npm package
under the user's npm prefix and invokes the upstream initialiser inside
/workspace, materialising any repo-local artefacts.

Supported integrations are declared in internal/sdd.Skills and bumped by
Renovate (see renovate.json customManager entries).`,
}

var sddInitCmd = &cobra.Command{
	Use:   "init <name>",
	Short: "Enable an SDD integration in the current repo",
	Long: `Mark the current repository as opted into an SDD integration.

Edits up to two files:
  - the project .toolbox.yaml found by walking up from cwd (created in
    cwd when there is none): sets 'sdd.<name>: true', preserving
    comments and key order.
  - .gitignore in cwd — the workspace the container mounts: appends a
    fenced block listing the glob patterns declared in
    internal/sdd.Skill.GitignoreEntries. Skills that produce
    user-authored content leave .gitignore untouched.

The actual install runs on the next 'toolbox shell' via entrypoint.sh,
which sees the TOOLBOX_SDD_* env contract emitted by sessionplan.`,
	Args: usageArgs(cobra.ExactArgs(1)),
	RunE: runSDDInit,
}

var sddListCmd = &cobra.Command{
	Use:   "list",
	Short: "List supported SDD integrations and their pinned versions",
	Args:  usageArgs(cobra.NoArgs),
	RunE:  runSDDList,
}

func runSDDInit(cmd *cobra.Command, args []string) error {
	name := args[0]
	skill, ok := sdd.Lookup(name)
	if !ok {
		return &usageError{err: fmt.Errorf(
			"unknown sdd integration %q; supported: %s",
			name, strings.Join(sdd.Keys(), ", "),
		)}
	}

	// The flag is a Config Schema key, not an SDD artefact, so it goes where
	// every other writer puts a project-layer value: the walked-up file. The
	// fence, by contrast, governs the directory the workspace mount writes.
	// Why they may differ: CONTEXT.md#config-scope.
	yamlPath, cwd, err := resolveWriteTarget("local")
	if err != nil {
		return err
	}
	gitignorePath := filepath.Join(cwd, ".gitignore")
	res, err := configedit.EnableSDD(yamlPath, gitignorePath, cwd, skill)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	report := func(path string, changed bool) {
		state := "unchanged"
		if changed {
			state = "updated"
		}
		_, _ = fmt.Fprintf(out, "  %s: %s\n", path, state)
	}
	_, _ = fmt.Fprintf(out, "toolbox sdd init %s (pin v%s)\n", skill.Key, skill.Version)
	report(yamlPath, res.YAMLChanged)
	if res.GitignoreSkipped {
		_, _ = fmt.Fprintf(out, "  %s: skipped (skill produces user-authored content)\n", gitignorePath)
	} else {
		report(gitignorePath, res.GitignoreChanged)
	}
	_, _ = fmt.Fprintln(out, "Run 'toolbox shell' to bootstrap the integration inside the container.")
	return nil
}

func runSDDList(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintln(out, "Supported SDD integrations (pin in internal/sdd, Renovate-bumped):")
	for _, s := range sdd.Skills {
		_, _ = fmt.Fprintf(out, "  %-10s %s@%s\n", s.Key, s.NpmPackage, s.Version)
	}
	return nil
}

func init() {
	sddCmd.AddCommand(sddInitCmd)
	sddCmd.AddCommand(sddListCmd)
	rootCmd.AddCommand(sddCmd)
}
