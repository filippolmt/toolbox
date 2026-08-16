package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/filippolmt/toolbox/internal/config"
)

// Wording shared across subcommands. Kept in one place so `toolbox mounts add
// --help` and `toolbox shells add --help` cannot drift apart.
const (
	// whereFlagUsage documents the --where flag every config-mutating
	// subcommand carries.
	whereFlagUsage = "config file to write: global|local"
)

// dockerClientErr wraps a failure to construct the moby client. A function
// rather than a shared format constant: `fmt.Errorf(const, err)` hides the
// format string from `go vet`'s printf checking and from grep at every call
// site, while the wording still lives in exactly one place.
func dockerClientErr(err error) error {
	return fmt.Errorf("failed to create Docker client: %w", err)
}

// errConfigNotLoaded reports that a subcommand ran without cobra's
// OnInitialize having populated cfg — a wiring bug, never a user error.
var errConfigNotLoaded = errors.New("internal: configuration not loaded")

// cfgFile holds the value of the --config flag (D-14: cobra-flag binding stays
// in cmd/root.go because the flag IS a cobra concern). Empty when unset.
var cfgFile string

// cfg holds the fully-resolved configuration produced by config.Plan during
// initConfig. Subcommands (build, shell, stop) consume this var directly.
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
