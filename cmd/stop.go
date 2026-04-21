package cmd

import (
	"github.com/filippolmt/toolbox/internal/container"
	"github.com/spf13/cobra"
)

var stopAll bool

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop and remove toolbox containers",
	Long: `Stop and remove the toolbox container bound to the current directory.
With --all, stop and remove every toolbox container on the host.
All persistent data lives on the host-mounted volumes, so nothing is lost.`,
	RunE: runStop,
}

func runStop(cmd *cobra.Command, args []string) error {
	cli, err := container.NewClient()
	if err != nil {
		return err
	}
	defer cli.Close()

	ctx, stopSig := signalCtx()
	defer stopSig()

	if stopAll {
		return container.StopAll(ctx, cli)
	}

	workspace, err := resolveWorkspace()
	if err != nil {
		return err
	}
	return container.Stop(ctx, cli, workspace)
}

func init() {
	stopCmd.Flags().BoolVar(&stopAll, "all", false, "Stop every toolbox container on the host")
	rootCmd.AddCommand(stopCmd)
}
