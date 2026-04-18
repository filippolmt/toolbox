package cmd

import (
	"context"

	"github.com/filippolmt/toolbox/internal/build"
	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/container"
	"github.com/spf13/cobra"
)

var buildCmd = &cobra.Command{
	Use:   "build",
	Short: "Builda l'immagine Docker toolbox",
	Long: `Builda l'immagine Docker toolbox localmente.
Usa il Dockerfile e build context configurati in .toolbox.yaml.`,
	RunE: runBuild,
}

func runBuild(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	cli, err := container.NewClient()
	if err != nil {
		return err
	}
	defer cli.Close()

	ctx := context.Background()
	return build.BuildImage(ctx, cli, cfg)
}

func init() {
	rootCmd.AddCommand(buildCmd)
}
