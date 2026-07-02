package cmd

import (
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"slices"
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
	// Each candidate maps a --flag to its config key + validator; only flags
	// the user actually passed are collected (Changed), validated up front,
	// then written together so a partial write never lands behind a later
	// rejection — and all keys share one read-parse-write cycle.
	candidates := []struct {
		flag, key, value string
		validate         func(string) error
	}{
		{"image", "image", configSetImage, config.ValidateImageRef},
		{"registry-mirror", "registry_mirror", configSetRegistryMirror, config.ValidateRegistryMirror},
		{"pull", "pull", configSetPull, config.ValidatePull},
		{"agent", "agent", configSetAgent, config.ValidateAgent},
	}
	var edits []configedit.ScalarEdit
	for _, c := range candidates {
		if !flags.Changed(c.flag) {
			continue
		}
		if err := c.validate(c.value); err != nil {
			return &usageError{err: err}
		}
		edits = append(edits, configedit.ScalarEdit{Key: c.key, Value: c.value})
	}
	if len(edits) == 0 {
		return &usageError{err: fmt.Errorf("set requires at least one of --image, --registry-mirror, --pull, --agent")}
	}

	target, err := resolveWriteTarget(configSetWhere)
	if err != nil {
		return err
	}
	existed := fileExists(target)
	changed, err := configedit.SetScalars(target, edits)
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

// writeResolvedConfig renders cfg as a deterministic YAML document covering
// every config.SchemaKeys() field (TestConfigShowCoversSchema guards that a
// new field can't silently go unrendered). Hand-rolled to avoid promoting the
// yaml v3 module to a direct dependency. Users pipe this output: it stays
// deterministic (map keys sorted), so origin annotations live behind the
// --origin flag (nil prov = none).
func writeResolvedConfig(w io.Writer, c *config.Config) error {
	return writeResolvedConfigWithOrigin(w, c, nil, "")
}

// quoteIfEmpty renders an empty scalar as the explicit `""` token (matching
// the mounts_root convention) so an unset key reads as deliberately blank
// rather than a dangling `key:`.
func quoteIfEmpty(s string) string {
	if s == "" {
		return `""`
	}
	return s
}

// boolPtrStr renders a tri-state *bool config toggle: nil (unset) reads as
// `auto`, otherwise the literal bool. `config show` shows the declared state,
// not a host-derived resolution the renderer can't compute.
func boolPtrStr(p *bool) string {
	if p == nil {
		return "auto"
	}
	if *p {
		return "true"
	}
	return "false"
}

// writeSortedMap renders a string-keyed map as a YAML block with keys sorted
// for determinism: `key: {}` when empty, else `key:` followed by two-space
// `k: <val(v)>` entries. ann is the origin annotation appended to the header.
func writeSortedMap[V any](w io.Writer, key, ann string, m map[string]V, val func(V) string) error {
	if len(m) == 0 {
		_, err := fmt.Fprintf(w, "%s: {}%s\n", key, ann)
		return err
	}
	if _, err := fmt.Fprintf(w, "%s:%s\n", key, ann); err != nil {
		return err
	}
	for _, k := range slices.Sorted(maps.Keys(m)) {
		if _, err := fmt.Fprintf(w, "  %s: %s\n", k, val(m[k])); err != nil {
			return err
		}
	}
	return nil
}

// writeYAMLSlice renders a string slice as a YAML block at the given indent
// depth (0 = top level): `key: []` when empty, else `key:` followed by
// `- item` entries one level deeper. ann is appended to the header.
func writeYAMLSlice(w io.Writer, indent int, key, ann string, items []string) error {
	pad := strings.Repeat("  ", indent)
	if len(items) == 0 {
		_, err := fmt.Fprintf(w, "%s%s: []%s\n", pad, key, ann)
		return err
	}
	if _, err := fmt.Fprintf(w, "%s%s:%s\n", pad, key, ann); err != nil {
		return err
	}
	for _, item := range items {
		if _, err := fmt.Fprintf(w, "%s  - %s\n", pad, item); err != nil {
			return err
		}
	}
	return nil
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

	// agent renders its resolved effective value: an unset key falls back to
	// config.DefaultAgent (the same constant cmd resolves against), so
	// `config show` tells the truth about what a worktree session will launch.
	agent := c.Agent
	if agent == "" {
		agent = config.DefaultAgent
	}
	if _, err := fmt.Fprintf(w, "agent: %s%s\n", agent, ann("agent")); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(w, "image: %s%s\n", quoteIfEmpty(c.Image), ann("image")); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "registry_mirror: %s%s\n", quoteIfEmpty(c.RegistryMirror), ann("registry_mirror")); err != nil {
		return err
	}
	pull := c.Pull
	if pull == "" {
		pull = config.PullAuto
	}
	if _, err := fmt.Fprintf(w, "pull: %s%s\n", pull, ann("pull")); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(w, "mounts_root: %s%s\n", quoteIfEmpty(c.MountsRoot), ann("mounts_root")); err != nil {
		return err
	}

	// Tri-state toggles: nil renders as `auto` (the resolved effective value is
	// host-derived and can't be computed from *Config alone). The deprecated
	// browser_bridge alias is intentionally not rendered — only the canonical
	// bridge key is shown (browser_bridge is still tracked in provenance).
	if _, err := fmt.Fprintf(w, "bridge: %s%s\n", boolPtrStr(c.Bridge), ann("bridge")); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "proximo: %s%s\n", boolPtrStr(c.Proximo), ann("proximo")); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "managed_statusline: %s%s\n", boolPtrStr(c.ManagedStatusline), ann("managed_statusline")); err != nil {
		return err
	}

	if err := writeSortedMap(w, "sdd", ann("sdd"), c.SDD, func(s config.SDDSkill) string {
		return fmt.Sprintf("%t", s.Enabled)
	}); err != nil {
		return err
	}
	if err := writeSortedMap(w, "env", ann("env"), c.Env, func(v string) string { return v }); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(w, "worktree:%s\n", ann("worktree")); err != nil {
		return err
	}
	if err := writeYAMLSlice(w, 1, "seed", "", c.Worktree.Seed); err != nil {
		return err
	}

	if err := writeYAMLSlice(w, 0, "inherit_host_auth", ann("inherit_host_auth"), c.InheritHostAuth); err != nil {
		return err
	}

	if len(c.Shells) == 0 {
		if _, err := fmt.Fprintf(w, "shells: {}%s\n", ann("shells")); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintf(w, "shells:%s\n", ann("shells")); err != nil {
			return err
		}
		for _, name := range slices.Sorted(maps.Keys(c.Shells)) {
			s := c.Shells[name]
			if _, err := fmt.Fprintf(w, "  %s:%s\n", name, ann(configedit.ShellKey(name))); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(w, "    path: %s\n", s.Path); err != nil {
				return err
			}
			if len(s.Env) > 0 {
				if _, err := fmt.Fprintln(w, "    env:"); err != nil {
					return err
				}
				for _, k := range slices.Sorted(maps.Keys(s.Env)) {
					if _, err := fmt.Fprintf(w, "      %s: %s\n", k, s.Env[k]); err != nil {
						return err
					}
				}
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
		if _, err := fmt.Fprintf(w, "  - name: %s%s\n", m.Name, ann(configedit.MountKey(m.Name))); err != nil {
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
