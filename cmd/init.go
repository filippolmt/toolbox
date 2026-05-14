package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/filippolmt/toolbox/internal/configexample"
)

var initForce bool

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Write an annotated .toolbox.yaml in the current directory",
	Long: `Create a .toolbox.yaml in the current working directory, populated with
the same annotated template printed by toolbox config example. Refuses to
overwrite an existing file unless --force is given.`,
	Args: usageArgs(cobra.NoArgs),
	RunE: runInit,
}

func runInit(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve cwd: %w", err)
	}
	path := filepath.Join(cwd, ".toolbox.yaml")

	if !initForce {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%s already exists; pass --force to overwrite", path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat %s: %w", path, err)
		}
	}

	if err := os.WriteFile(path, []byte(configexample.Render()), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", path)
	return nil
}

func init() {
	initCmd.Flags().BoolVar(&initForce, "force", false, "Overwrite an existing .toolbox.yaml")
	rootCmd.AddCommand(initCmd)
}
