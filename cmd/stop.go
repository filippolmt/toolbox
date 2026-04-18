package cmd

import (
	"context"

	"github.com/filippolmt/toolbox/internal/container"
	"github.com/spf13/cobra"
)

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Ferma e rimuove il container toolbox",
	Long: `Ferma il container toolbox in esecuzione e lo rimuove.
Tutti i dati persistono sui volumi host montati.`,
	RunE: runStop,
}

func runStop(cmd *cobra.Command, args []string) error {
	cli, err := container.NewClient()
	if err != nil {
		return err
	}
	defer cli.Close()

	ctx := context.Background()
	return container.Stop(ctx, cli)
}

func init() {
	rootCmd.AddCommand(stopCmd)
}
