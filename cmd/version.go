package cmd

import (
	"fmt"

	"github.com/filippolmt/toolbox/internal/version"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(versionCmd)
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the toolbox version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("toolbox %s (commit: %s, built: %s)\n",
			version.Version, version.Commit, version.Date)
	},
}
