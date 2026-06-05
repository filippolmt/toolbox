package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/filippolmt/toolbox/internal/configio"
	"github.com/filippolmt/toolbox/internal/sdd"
)

const (
	sddYAMLKey       = "sdd"
	sddFenceTemplate = "sdd-managed/"
)

func gitignoreFenceStart(skill string) string {
	return "# >>> " + sddFenceTemplate + skill + " (toolbox)"
}

func gitignoreFenceEnd(skill string) string {
	return "# <<< " + sddFenceTemplate + skill + " (toolbox)"
}

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

Edits up to two files in cwd:
  - .toolbox.yaml: sets 'sdd.<name>: true', preserving comments and key
    order. Creates the file if missing.
  - .gitignore: appends a fenced block listing the glob patterns
    declared in internal/sdd.Skill.GitignoreEntries. Skills that
    produce user-authored content leave .gitignore untouched.

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

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve cwd: %w", err)
	}

	yamlPath := filepath.Join(cwd, ".toolbox.yaml")
	yamlChanged, err := upsertSDDFlag(yamlPath, skill.Key)
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
	report(yamlPath, yamlChanged)

	gitignorePath := filepath.Join(cwd, ".gitignore")
	if len(skill.GitignoreEntries) == 0 {
		_, _ = fmt.Fprintf(out, "  %s: skipped (skill produces user-authored content)\n", gitignorePath)
	} else {
		gitignoreChanged, err := upsertGitignoreFence(gitignorePath, skill)
		if err != nil {
			return err
		}
		report(gitignorePath, gitignoreChanged)
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

// upsertSDDFlag writes `sdd.<key>: true` to path via configio.UpsertFile so
// user-authored comments and key order survive. Returns (changed, error)
// where changed reflects whether the rendered byte stream differs from
// what is on disk (idempotent re-runs report unchanged).
//
// An existing object-form entry (`sdd.<key>: {steps: [...]}`) is left
// untouched: it already means enabled, and SetMapBool would clobber the
// user's steps override with the bool shorthand.
func upsertSDDFlag(path, skillKey string) (bool, error) {
	return configio.UpsertFile(path, func(doc *yaml.Node) {
		sddMap := configio.EnsureChildMap(doc, sddYAMLKey)
		if v := configio.ChildValue(sddMap, skillKey); v != nil && v.Kind == yaml.MappingNode {
			return
		}
		configio.SetMapBool(sddMap, skillKey, true)
	})
}

// upsertGitignoreFence appends or replaces the per-skill fenced block in
// path. Each skill owns its own fence pair so `toolbox sdd init <other>`
// touches only the relevant section.
func upsertGitignoreFence(path string, skill sdd.Skill) (bool, error) {
	body := renderGitignoreBlock(skill)
	start := gitignoreFenceStart(skill.Key)
	end := gitignoreFenceEnd(skill.Key)

	existing, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return true, configio.AtomicWriteFile(path, []byte(body+"\n"), 0o600)
	}
	if err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}

	startIdx := bytes.Index(existing, []byte(start))
	endIdx := bytes.Index(existing, []byte(end))
	if startIdx < 0 || endIdx <= startIdx {
		separator := "\n"
		if len(existing) > 0 && !bytes.HasSuffix(existing, []byte("\n")) {
			separator = "\n\n"
		} else if len(existing) == 0 {
			separator = ""
		}
		updated := append([]byte{}, existing...)
		updated = append(updated, []byte(separator+body+"\n")...)
		return true, configio.AtomicWriteFile(path, updated, 0o600)
	}

	tail := endIdx + len(end)
	if tail < len(existing) && existing[tail] == '\n' {
		tail++
	}
	replaced := append([]byte{}, existing[:startIdx]...)
	replaced = append(replaced, []byte(body+"\n")...)
	replaced = append(replaced, existing[tail:]...)
	if bytes.Equal(replaced, existing) {
		return false, nil
	}
	return true, configio.AtomicWriteFile(path, replaced, 0o600)
}

func renderGitignoreBlock(skill sdd.Skill) string {
	var b strings.Builder
	b.WriteString(gitignoreFenceStart(skill.Key))
	b.WriteString("\n")
	for _, e := range skill.GitignoreEntries {
		b.WriteString(e)
		b.WriteString("\n")
	}
	b.WriteString(gitignoreFenceEnd(skill.Key))
	return b.String()
}

func init() {
	sddCmd.AddCommand(sddInitCmd)
	sddCmd.AddCommand(sddListCmd)
	rootCmd.AddCommand(sddCmd)
}
