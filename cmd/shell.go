package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/container"
)

var shellCmd = &cobra.Command{
	Use:   "shell",
	Short: "Avvia una sessione shell nel container toolbox",
	Long: `Avvia il container toolbox e apre una sessione bash interattiva.
Se il container e' gia' running, apre una nuova sessione nello stesso container.`,
	RunE: runShell,
}

func runShell(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	cli, err := container.NewClient()
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %w", err)
	}
	defer cli.Close()

	ctx := context.Background()
	return container.Shell(ctx, cli, cfg)
}

func init() {
	rootCmd.AddCommand(shellCmd)
}
