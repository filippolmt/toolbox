package cmd

import (
	"github.com/filippolmt/toolbox/internal/build"
	"github.com/filippolmt/toolbox/internal/container"
	"github.com/filippolmt/toolbox/internal/ui"
	"github.com/spf13/cobra"
)

var (
	buildNoCache bool
)

var buildCmd = &cobra.Command{
	Use:   "build",
	Short: "Build the toolbox Docker image from the embedded context",
	Long: `Build the toolbox Docker image locally.

The Dockerfile and companion scripts are embedded into the toolbox binary,
so this works from any directory — a repo checkout is not required.

The build produces the canonical image tag (` + build.DefaultRegistryImage + `),
overwriting the local cache. The next 'toolbox shell' picks up the freshly
built image; run 'docker pull ` + build.DefaultRegistryImage + `' to restore
the upstream one.`,
	Args: usageArgs(cobra.NoArgs),
	RunE: runBuild,
}

func runBuild(cmd *cobra.Command, args []string) error {
	cli, err := container.NewClient()
	if err != nil {
		return err
	}
	defer cli.Close()

	tag := build.DefaultRegistryImage
	ui.Info("Building " + tag + " locally (overwrites the cached copy of the registry image)")

	ctx, stopSig := signalCtx()
	defer stopSig()

	return build.BuildImage(ctx, cli, build.Options{
		Tag:     tag,
		NoCache: buildNoCache,
	})
}

func init() {
	buildCmd.Flags().BoolVar(&buildNoCache, "no-cache", false,
		"force a full rebuild, ignoring Docker's layer cache")
	rootCmd.AddCommand(buildCmd)
}
