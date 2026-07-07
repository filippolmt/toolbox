package cmd

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/filippolmt/toolbox/internal/container"
)

var listCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List toolbox containers on the host",
	Long: `List every toolbox container on the host, across all directories, with
its workspace path and status. Containers are created with AutoRemove, so in
practice the list shows the shells you have running right now.`,
	Args: usageArgs(cobra.NoArgs),
	RunE: runList,
}

func runList(cmd *cobra.Command, _ []string) error {
	cli, err := container.NewClient()
	if err != nil {
		return err
	}
	defer cli.Close()

	ctx, stopSig := signalCtx()
	defer stopSig()

	items, err := container.List(ctx, cli)
	if err != nil {
		return err
	}

	renderList(cmd.OutOrStdout(), items)
	return nil
}

func renderList(out io.Writer, items []container.Item) {
	if len(items) == 0 {
		_, _ = fmt.Fprintln(out, "No toolbox containers.")
		return
	}

	nameW, wsW := len("NAME"), len("WORKSPACE")
	for _, it := range items {
		nameW = max(nameW, len(it.Name))
		wsW = max(wsW, len(it.Workspace))
	}
	_, _ = fmt.Fprintf(out, "%-*s  %-*s  %s\n", nameW, "NAME", wsW, "WORKSPACE", "STATUS")
	for _, it := range items {
		_, _ = fmt.Fprintf(out, "%-*s  %-*s  %s\n", nameW, it.Name, wsW, it.Workspace, it.Status)
	}
}

func init() {
	rootCmd.AddCommand(listCmd)
}
