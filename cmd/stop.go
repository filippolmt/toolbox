package cmd

import (
	"fmt"

	"github.com/filippolmt/toolbox/internal/container"
	"github.com/filippolmt/toolbox/internal/workspace"
	"github.com/spf13/cobra"
)

var stopAll bool

var stopCmd = &cobra.Command{
	Use:   "stop [name]",
	Short: "Stop and remove toolbox containers",
	Long: `Stop and remove the toolbox container bound to the current directory.
With --all, stop and remove every toolbox container on the host.
All persistent data lives on the host-mounted volumes, so nothing is lost.`,
	Args: usageArgs(cobra.MaximumNArgs(1)),
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
		if len(args) > 0 {
			return &usageError{err: fmt.Errorf("--all cannot be used with a shell name")}
		}
		return container.StopAll(ctx, cli)
	}
	if len(args) > 0 {
		return container.StopByName(ctx, cli, args[0])
	}

	ws, err := workspace.Resolve()
	if err != nil {
		return err
	}
	return container.Stop(ctx, cli, ws)
}

func init() {
	stopCmd.Flags().BoolVar(&stopAll, "all", false, "Stop every toolbox container on the host")
	rootCmd.AddCommand(stopCmd)
}
