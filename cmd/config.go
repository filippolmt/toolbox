package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/filippolmt/toolbox/internal/bridge"
	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/configedit"
	"github.com/filippolmt/toolbox/internal/configexample"
	"github.com/filippolmt/toolbox/internal/configio"
	"github.com/filippolmt/toolbox/internal/configrender"
	"github.com/filippolmt/toolbox/internal/configui"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Inspect and scaffold .toolbox.yaml",
	Long: `Inspect the resolved configuration or print an annotated .toolbox.yaml template.

The Plan resolution honours the same precedence as toolbox shell:
  --config flag > project .toolbox.yaml (walk-up) > ~/.toolbox.yaml > defaults.
For image / registry_mirror / pull / bridge, a TOOLBOX_* env var overrides all
file layers.`,
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
			return configrender.Resolved(cmd.OutOrStdout(), cfg)
		}
		cwd, _ := os.Getwd()
		prov, err := configedit.Compute(cwd, cfgFile)
		if err != nil {
			return err
		}
		return configrender.ResolvedWithOrigin(cmd.OutOrStdout(), cfg, prov, cfgFile)
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

var (
	configSetImage          string
	configSetRegistryMirror string
	configSetPull           string
	configSetAgent          string
	configSetWhere          string
)

var configSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Set scalar keys (image / registry_mirror / pull / agent)",
	Long: `Write the supported scalar keys to the --where-resolved config file,
preserving comments and sibling keys. Only the flags you pass are written;
passing an empty value resets that key to its default.

  --image            full image ref override (host/path:tag or digest)
  --registry-mirror  relocate only the registry host — point the canonical
                     image at a proxy hub / pull-through cache (Harbor,
                     Artifactory, Nexus, ECR pull-through)
  --pull             registry-sync policy: auto (default) | always | never
  --agent            default AI agent for 'toolbox worktree': claude | codex

  --where global     ~/.toolbox.yaml (default)
  --where local      the walked-up project .toolbox.yaml, creating
                     ./.toolbox.yaml when none is found`,
	Example: `  toolbox config set --where global --registry-mirror harbor.corp.io/ghcr-proxy
  toolbox config set --pull never --where local
  toolbox config set --image ""   # reset to the canonical default`,
	Args: usageArgs(cobra.NoArgs),
	RunE: runConfigSet,
}

var configUICmd = &cobra.Command{
	Use:   "ui",
	Short: "Interactively view and edit .toolbox.yaml",
	Long: `Launch an interactive terminal UI to view and edit toolbox configuration
across the Global (~/.toolbox.yaml) and Repo (./.toolbox.yaml) layers.

Each key shows its resolved effective value and the layer that supplies it
(default / global / repo / env). Edits are validated with the config doctor
before being written through the comment-preserving writer. Requires an
interactive terminal. Edits the global and repo layers only — the global
--config override is not an editable layer and does not apply here.`,
	Args: usageArgs(cobra.NoArgs),
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, _ := os.Getwd()
		return configui.Run(cwd)
	},
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

	configSetCmd.Flags().StringVar(&configSetImage, "image", "", "full image ref override (empty resets to default)")
	configSetCmd.Flags().StringVar(&configSetRegistryMirror, "registry-mirror", "", "relocate the registry host (proxy hub / pull-through cache)")
	configSetCmd.Flags().StringVar(&configSetPull, "pull", "", "registry-sync policy: auto|always|never")
	configSetCmd.Flags().StringVar(&configSetAgent, "agent", "", "default AI agent for 'toolbox worktree': claude|codex (empty resets to default)")
	configSetCmd.Flags().StringVar(&configSetWhere, "where", "global", "config file to write: global|local")

	configCmd.AddCommand(configShowCmd)
	configCmd.AddCommand(configSetCmd)
	configCmd.AddCommand(configExampleCmd)
	configCmd.AddCommand(configPathCmd)
	configCmd.AddCommand(configEditCmd)
	configCmd.AddCommand(configDoctorCmd)
	configCmd.AddCommand(configUICmd)
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

func runConfigSet(cmd *cobra.Command, _ []string) error {
	flags := cmd.Flags()
	// Each candidate maps a --flag to its config key; only flags the user
	// actually passed are collected (Changed), validated up front through the
	// one per-key rule in config, then written together so a partial write never
	// lands behind a later rejection — and all keys share one read-parse-write
	// cycle. The up-front pass is purely for the usage-error exit code: an
	// invalid value would be rejected by the write gate regardless.
	candidates := []struct{ flag, key, value string }{
		{"image", "image", configSetImage},
		{"registry-mirror", "registry_mirror", configSetRegistryMirror},
		{"pull", "pull", configSetPull},
		{"agent", "agent", configSetAgent},
	}
	var edits []configedit.ScalarEdit
	for _, c := range candidates {
		if !flags.Changed(c.flag) {
			continue
		}
		if err := config.ValidateKey(c.key, c.value); err != nil {
			return &usageError{err: err}
		}
		edits = append(edits, configedit.ScalarEdit{Key: c.key, Value: c.value})
	}
	if len(edits) == 0 {
		return &usageError{err: fmt.Errorf("set requires at least one of --image, --registry-mirror, --pull, --agent")}
	}

	target, cwd, err := resolveWriteTarget(configSetWhere)
	if err != nil {
		return err
	}
	existed := fileExists(target)
	changed, err := configedit.SetScalars(target, cwd, edits)
	if err != nil {
		return err
	}
	reportWrite(cmd.OutOrStdout(), target, existed, changed)
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
	if ok, advice := bridge.CheckHostCredentialHelper(); !ok {
		findings = append(findings, configedit.Finding{Severity: configedit.SeverityWarning, Message: advice})
	}
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
