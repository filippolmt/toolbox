package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/filippolmt/toolbox/internal/catalog"
)

var cfgFile string

// usageError marks a flag- or argument-parsing failure so Execute can map it
// to the conventional exit code 2 (vs. 1 for runtime errors).
type usageError struct{ err error }

func (e *usageError) Error() string { return e.err.Error() }
func (e *usageError) Unwrap() error { return e.err }

// usageArgs wraps a cobra positional-args validator so that any failure
// surfaces as *usageError. Cobra routes flag-parsing errors through
// SetFlagErrorFunc but gives no equivalent hook for positional-args
// validators — this bridges that gap so `toolbox shell extra` exits 2 like
// `toolbox --bad-flag`.
func usageArgs(v cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if v == nil {
			return nil
		}
		if err := v(cmd, args); err != nil {
			return &usageError{err: err}
		}
		return nil
	}
}

var rootCmd = &cobra.Command{
	Use:           "toolbox",
	Short:         "Manage the toolbox development container",
	Long:          "CLI to start, stop, and build the toolbox container.",
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Execute runs the root command. Invoked from main.go.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "toolbox: "+err.Error())
		if _, ok := errors.AsType[*usageError](err); ok {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "",
		"config file (default: .toolbox.yaml, ~/.toolbox.yaml)")
	rootCmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &usageError{err: err}
	})
}

func initConfig() {
	// 1. Built-in defaults apply to every config-resolution path so that
	//    missing keys (e.g. a new tool added in code but not yet in the user's
	//    yaml) resolve to the documented default value.
	setDefaults()

	if cfgFile != "" {
		// Explicit --config: must be read; failure is hard. User asked for
		// this file on purpose.
		viper.SetConfigFile(cfgFile)
		if err := viper.ReadInConfig(); err != nil {
			fmt.Fprintln(os.Stderr, "toolbox: failed to read "+cfgFile+": "+err.Error())
			os.Exit(1)
		}
	} else {
		// 2. Global config (~/.toolbox.yaml). Skip if HOME is unresolvable
		// — AddConfigPath("") would silently read from CWD.
		viper.SetConfigName(".toolbox")
		viper.SetConfigType("yaml")
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			viper.AddConfigPath(home)
			_ = viper.ReadInConfig() // ok if missing (D-05)
		} else if err != nil {
			fmt.Fprintln(os.Stderr, "toolbox: skipping global config: "+err.Error())
		}

		// 3. Project config (.toolbox.yaml) -- merged on top of global (D-04).
		// Walks up from CWD to support running `toolbox shell` from any subdir
		// of the workspace (mirrors how git resolves .gitignore / .git).
		// Stops at HOME and at the filesystem root so a project file directly
		// under HOME can't shadow the global config silently.
		if cwd, err := os.Getwd(); err == nil {
			if path := findProjectConfig(cwd); path != "" {
				data, rerr := os.ReadFile(path)
				if rerr != nil {
					fmt.Fprintln(os.Stderr, "toolbox: failed to read "+path+": "+rerr.Error())
					os.Exit(1)
				}
				if merr := viper.MergeConfig(bytes.NewReader(data)); merr != nil {
					fmt.Fprintln(os.Stderr, "toolbox: failed to parse "+path+": "+merr.Error())
					os.Exit(1)
				}
			}
		}
	}

	// 4. Env var overrides (TOOLBOX_IMAGE_NAME, etc.)
	viper.SetEnvPrefix("TOOLBOX")
	viper.AutomaticEnv()
}

// findProjectConfig walks up from start looking for a `.toolbox.yaml` file
// and returns the first match's absolute path. Search stops at the user's
// HOME directory (so the global ~/.toolbox.yaml is not re-read as a
// project file) and at the filesystem root. Returns "" when no project
// config is found along the way.
func findProjectConfig(start string) string {
	home, _ := os.UserHomeDir()
	cur := filepath.Clean(start)
	for {
		// HOME is the last directory we will *not* read a project config from
		// — its `.toolbox.yaml` is the global and is handled separately.
		if home != "" && cur == home {
			return ""
		}
		candidate := filepath.Join(cur, ".toolbox.yaml")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}

// setDefaults sets default values per individual field.
// Do NOT use nested objects; that breaks MergeInConfig (Pitfall 2).
// Default mounts are handled in config.Load() as a fallback.
func setDefaults() {
	// Every opt-out tool is on by default. Tool selection is applied at
	// build time via `ARG INSTALL_<TOOL>` in internal/build/assets/Dockerfile.
	// Derived from catalog.Keys() so the two lists can't drift.
	for _, k := range catalog.Keys() {
		viper.SetDefault("tools."+k, true)
	}
}
