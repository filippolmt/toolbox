package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/filippolmt/toolbox/internal/container"
	"github.com/filippolmt/toolbox/internal/workspace"
	"github.com/spf13/cobra"
)

var stopAll bool

var stopCmd = &cobra.Command{
	Use:   "stop [name|dir]",
	Short: "Stop and remove toolbox containers",
	Long: `Stop and remove the toolbox container bound to the current directory,
to a configured named shell (toolbox stop infra), or to an absolute path
that was used as a one-shot session (toolbox stop /tmp).
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
		// Absolute paths mirror the `toolbox shell <abs-path>` quick form
		// and stop the workspace-hash container associated with that path.
		// Any other positional value is treated as a named-shell key.
		// workspace.ResolveExplicit reuses the shell side's validation so
		// `toolbox stop /bad:path` fails identically to `toolbox shell`.
		if filepath.IsAbs(args[0]) {
			ws, err := workspace.ResolveExplicit(args[0])
			if err != nil {
				return err
			}
			return container.Stop(ctx, cli, ws)
		}
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
