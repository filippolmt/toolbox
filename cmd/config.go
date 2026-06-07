package cmd

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/configedit"
	"github.com/filippolmt/toolbox/internal/configexample"
	"github.com/filippolmt/toolbox/internal/configio"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Inspect and scaffold .toolbox.yaml",
	Long: `Inspect the resolved configuration or print an annotated .toolbox.yaml template.

The Plan resolution honours the same precedence as toolbox shell:
  --config flag > project .toolbox.yaml (walk-up) > ~/.toolbox.yaml > TOOLBOX_* env > defaults.`,
	Args: usageArgs(cobra.NoArgs),
}

var configShowOrigin bool

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Print the fully-resolved configuration",
	Long: `Print the configuration that toolbox shell would consume right now,
after layering --config / project / global / env / defaults.

With --origin every rendered key is annotated with the layer that set it —
(default), (~/.toolbox.yaml), (./.toolbox.yaml), or (--config <path>).
Mounts are attributed per entry name; other keys at container granularity.`,
	Args: usageArgs(cobra.NoArgs),
	RunE: func(cmd *cobra.Command, args []string) error {
		if !configShowOrigin {
			return writeResolvedConfig(cmd.OutOrStdout(), cfg)
		}
		cwd, _ := os.Getwd()
		prov, err := configedit.Compute(cwd, cfgFile)
		if err != nil {
			return err
		}
		return writeResolvedConfigWithOrigin(cmd.OutOrStdout(), cfg, prov, cfgFile)
	},
}

var configExampleCmd = &cobra.Command{
	Use:   "example",
	Short: "Print an annotated .toolbox.yaml template",
	Long: `Print a fully-annotated .toolbox.yaml template covering every supported
field. Pipe to a file (toolbox config example > .toolbox.yaml) or use
toolbox init to write it to the current directory.`,
	Args: usageArgs(cobra.NoArgs),
	Run: func(cmd *cobra.Command, args []string) {
		_, _ = fmt.Fprint(cmd.OutOrStdout(), configexample.Render())
	},
}

var configPathCmd = &cobra.Command{
	Use:   "path",
	Short: "Show the config layers in precedence order",
	Long: `List every layer that can contribute to the resolved configuration —
explicit --config, walked-up project file, global file, TOOLBOX_* env,
built-in defaults — in precedence order, marking which are present.`,
	Args: usageArgs(cobra.NoArgs),
	RunE: runConfigPath,
}

var configEditCmd = &cobra.Command{
	Use:   "edit",
	Short: "Open the highest-precedence config file in $EDITOR",
	Long: `Open $EDITOR (fallback vi) on the highest-precedence existing config file:
explicit --config > walked-up project .toolbox.yaml > ~/.toolbox.yaml.
When no config file exists anywhere, ~/.toolbox.yaml is created with a
documentation header first.`,
	Args: usageArgs(cobra.NoArgs),
	RunE: runConfigEdit,
}

var configDoctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Validate the configuration without modifying it",
	Long: `Check the configuration for problems: schema/merge errors, unknown
top-level keys (with suggestions), the legacy tools: block, empty or
missing shells.<name>.path, mount-merge failures, and duplicate resolved
mount targets. Exits non-zero if and only if an error-severity finding
exists; warnings alone exit 0.`,
	Args: usageArgs(cobra.NoArgs),
	RunE: runConfigDoctor,
}

func init() {
	configShowCmd.Flags().BoolVar(&configShowOrigin, "origin", false, "annotate each key with the layer that set it")
	configCmd.AddCommand(configShowCmd)
	configCmd.AddCommand(configExampleCmd)
	configCmd.AddCommand(configPathCmd)
	configCmd.AddCommand(configEditCmd)
	configCmd.AddCommand(configDoctorCmd)
	rootCmd.AddCommand(configCmd)
}

func runConfigPath(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()
	cwd, _ := os.Getwd()

	layer := func(label, detail string) {
		_, _ = fmt.Fprintf(out, "  %-24s %s\n", label, detail)
	}
	// A set --config short-circuits the file layers below it, mirroring
	// config.LoadLayers.
	shadow := ""
	if cfgFile != "" {
		shadow = " — ignored (--config overrides)"
	}

	_, _ = fmt.Fprintln(out, "Configuration layers (highest precedence first):")
	if cfgFile != "" {
		layer("--config", cfgFile+" (found)")
	} else {
		layer("--config", "(not set)")
	}

	if p := config.WalkUpProjectConfig(cwd); p != "" {
		layer("project .toolbox.yaml", p+" (found)"+shadow)
	} else {
		layer("project .toolbox.yaml", "(none found)")
	}

	globalPath, err := configio.GlobalConfigPath()
	switch {
	case err != nil:
		layer("global ~/.toolbox.yaml", "(home directory not resolvable)")
	default:
		if _, statErr := os.Stat(globalPath); statErr == nil {
			layer("global ~/.toolbox.yaml", globalPath+" (found)"+shadow)
		} else {
			layer("global ~/.toolbox.yaml", globalPath+" (not present)")
		}
	}

	envCount := 0
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "TOOLBOX_") {
			envCount++
		}
	}
	if envCount > 0 {
		layer("TOOLBOX_* env", fmt.Sprintf("(%d set)", envCount))
	} else {
		layer("TOOLBOX_* env", "(none set)")
	}
	layer("defaults", "(built-in)")
	return nil
}

func runConfigEdit(cmd *cobra.Command, _ []string) error {
	path, created, err := resolveEditTarget()
	if err != nil {
		return err
	}
	if created {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "created %s\n", path)
	}

	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	parts := strings.Fields(editor)
	editCmd := exec.Command(parts[0], append(parts[1:], path)...) //nolint:gosec // $EDITOR is the user's own choice
	editCmd.Stdin = os.Stdin
	editCmd.Stdout = os.Stdout
	editCmd.Stderr = os.Stderr
	return editCmd.Run()
}

// resolveEditTarget returns the highest-precedence existing config file
// (explicit --config > walked-up project > global), creating the global
// file with the documentation header when none exists anywhere.
func resolveEditTarget() (path string, created bool, err error) {
	if cfgFile != "" {
		return cfgFile, false, nil
	}
	cwd, _ := os.Getwd()
	if p := config.WalkUpProjectConfig(cwd); p != "" {
		return p, false, nil
	}
	globalPath, err := configio.GlobalConfigPath()
	if err != nil {
		return "", false, err
	}
	if _, statErr := os.Stat(globalPath); statErr == nil {
		return globalPath, false, nil
	}
	if err := configedit.EnsureFileWithHeader(globalPath); err != nil {
		return "", false, err
	}
	return globalPath, true, nil
}

func runConfigDoctor(cmd *cobra.Command, _ []string) error {
	cwd, _ := os.Getwd()
	findings := configedit.Doctor(cwd, cfgFile)
	out := cmd.OutOrStdout()

	if len(findings) == 0 {
		_, _ = fmt.Fprintln(out, "no findings")
		return nil
	}

	var errs, warns []configedit.Finding
	for _, f := range findings {
		if f.Severity == configedit.SeverityError {
			errs = append(errs, f)
		} else {
			warns = append(warns, f)
		}
	}
	if len(errs) > 0 {
		_, _ = fmt.Fprintln(out, "errors:")
		for _, f := range errs {
			_, _ = fmt.Fprintf(out, "  - %s\n", f.Message)
		}
	}
	if len(warns) > 0 {
		_, _ = fmt.Fprintln(out, "warnings:")
		for _, f := range warns {
			_, _ = fmt.Fprintf(out, "  - %s\n", f.Message)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("config doctor: %d error finding(s)", len(errs))
	}
	return nil
}

// writeResolvedConfig renders cfg as a deterministic YAML document. Hand-
// rolled to avoid promoting the yaml v3 module to a direct dependency for a
// 4-field struct. Users pipe this output: it must stay byte-for-byte stable,
// so origin annotations live behind the --origin flag (nil prov = none).
func writeResolvedConfig(w io.Writer, c *config.Config) error {
	return writeResolvedConfigWithOrigin(w, c, nil, "")
}

// writeResolvedConfigWithOrigin is writeResolvedConfig plus optional
// per-key origin annotations (git-config --show-origin style). With a nil
// prov the output is identical to the historical renderer.
func writeResolvedConfigWithOrigin(w io.Writer, c *config.Config, prov configedit.Provenance, explicitPath string) error {
	if c == nil {
		return fmt.Errorf("config not initialised")
	}
	ann := func(key string) string {
		if prov == nil {
			return ""
		}
		return " " + prov[key].LabelWithPath(explicitPath)
	}

	if _, err := fmt.Fprintf(w, "shell: %s%s\n", c.Shell, ann("shell")); err != nil {
		return err
	}
	if c.MountsRoot != "" {
		if _, err := fmt.Fprintf(w, "mounts_root: %s%s\n", c.MountsRoot, ann("mounts_root")); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintf(w, "mounts_root: \"\"%s\n", ann("mounts_root")); err != nil {
			return err
		}
	}

	if len(c.InheritHostAuth) == 0 {
		if _, err := fmt.Fprintf(w, "inherit_host_auth: []%s\n", ann("inherit_host_auth")); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintf(w, "inherit_host_auth:%s\n", ann("inherit_host_auth")); err != nil {
			return err
		}
		for _, k := range c.InheritHostAuth {
			if _, err := fmt.Fprintf(w, "  - %s\n", k); err != nil {
				return err
			}
		}
	}

	if len(c.Mounts) == 0 {
		_, err := fmt.Fprintf(w, "mounts: []%s\n", ann("mounts"))
		return err
	}
	if _, err := fmt.Fprintln(w, "mounts:"); err != nil {
		return err
	}
	for _, m := range c.Mounts {
		mountKey := "mounts"
		if m.Name != "" {
			mountKey = "mounts." + m.Name
		}
		if _, err := fmt.Fprintf(w, "  - name: %s%s\n", m.Name, ann(mountKey)); err != nil {
			return err
		}
		if m.Source != "" {
			if _, err := fmt.Fprintf(w, "    source: %s\n", m.Source); err != nil {
				return err
			}
		}
		if m.Target != "" {
			if _, err := fmt.Fprintf(w, "    target: %s\n", m.Target); err != nil {
				return err
			}
		}
		if m.ReadOnly {
			if _, err := fmt.Fprintln(w, "    readonly: true"); err != nil {
				return err
			}
		}
		if m.CreateIfMissing {
			if _, err := fmt.Fprintln(w, "    create_if_missing: true"); err != nil {
				return err
			}
		}
		if m.SymlinkFrom != "" {
			if _, err := fmt.Fprintf(w, "    symlink_from: %s\n", m.SymlinkFrom); err != nil {
				return err
			}
		}
		if m.Disabled {
			if _, err := fmt.Fprintln(w, "    disabled: true"); err != nil {
				return err
			}
		}
	}
	return nil
}
