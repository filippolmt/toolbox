package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/filippolmt/toolbox/internal/config"
)

// cfgFile holds the value of the --config flag (D-14: cobra-flag binding stays
// in cmd/root.go because the flag IS a cobra concern). Empty when unset.
var cfgFile string

// cfg holds the fully-resolved configuration produced by config.Plan during
// initConfig. Subcommands (build, shell, stop) currently still consume the
// pipeline via config.Load() (deprecated wrapper landing in Plan 05) — Phase
// 09 (Session Plan) sweeps those call sites onto this var directly.
var cfg *config.Config

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

// initConfig is the cobra OnInitialize hook. It is the sole call site for
// config.Plan in cmd/. Walk-up + global / project / explicit YAML loading +
// validation all live behind config.Plan (D-13 / Phase 08).
func initConfig() {
	cwd, _ := os.Getwd() // empty on error → Plan handles via filepath.Clean("") == "."
	c, err := config.Plan(cwd, cfgFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "toolbox: "+err.Error())
		os.Exit(1)
	}
	cfg = c
}
