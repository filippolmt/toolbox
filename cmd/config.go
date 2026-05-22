package cmd

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/configexample"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Inspect and scaffold .toolbox.yaml",
	Long: `Inspect the resolved configuration or print an annotated .toolbox.yaml template.

The Plan resolution honours the same precedence as toolbox shell:
  --config flag > project .toolbox.yaml (walk-up) > ~/.toolbox.yaml > TOOLBOX_* env > defaults.`,
	Args: usageArgs(cobra.NoArgs),
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Print the fully-resolved configuration",
	Long: `Print the configuration that toolbox shell would consume right now,
after layering --config / project / global / env / defaults.`,
	Args: usageArgs(cobra.NoArgs),
	RunE: func(cmd *cobra.Command, args []string) error {
		return writeResolvedConfig(cmd.OutOrStdout(), cfg)
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

func init() {
	configCmd.AddCommand(configShowCmd)
	configCmd.AddCommand(configExampleCmd)
	rootCmd.AddCommand(configCmd)
}

// writeResolvedConfig renders cfg as a deterministic YAML document. Hand-
// rolled to avoid promoting the yaml v3 module to a direct dependency for a
// 4-field struct.
func writeResolvedConfig(w io.Writer, c *config.Config) error {
	if c == nil {
		return fmt.Errorf("config not initialised")
	}

	if _, err := fmt.Fprintf(w, "shell: %s\n", c.Shell); err != nil {
		return err
	}
	if c.MountsRoot != "" {
		if _, err := fmt.Fprintf(w, "mounts_root: %s\n", c.MountsRoot); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintln(w, "mounts_root: \"\""); err != nil {
			return err
		}
	}

	if len(c.InheritHostAuth) == 0 {
		if _, err := fmt.Fprintln(w, "inherit_host_auth: []"); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintln(w, "inherit_host_auth:"); err != nil {
			return err
		}
		for _, k := range c.InheritHostAuth {
			if _, err := fmt.Fprintf(w, "  - %s\n", k); err != nil {
				return err
			}
		}
	}

	if len(c.Mounts) == 0 {
		_, err := fmt.Fprintln(w, "mounts: []")
		return err
	}
	if _, err := fmt.Fprintln(w, "mounts:"); err != nil {
		return err
	}
	for _, m := range c.Mounts {
		if _, err := fmt.Fprintf(w, "  - name: %s\n", m.Name); err != nil {
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
