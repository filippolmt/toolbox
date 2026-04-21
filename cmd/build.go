package cmd

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/filippolmt/toolbox/internal/build"
	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/container"
	"github.com/filippolmt/toolbox/internal/ui"
	"github.com/filippolmt/toolbox/internal/version"
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

The resulting image tag mirrors what 'toolbox shell' would look for:
- When tools match the defaults, the canonical ghcr.io tag.
- When any tool is opted out, a content-hashed local tag
  (toolbox:local-<hash>) so different configs don't clobber each other.`,
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

	ref, isLocal := build.ResolveImage(cfg, version.Version)
	if !isLocal {
		ui.Info("Config matches defaults — rebuilding the canonical image locally as " + ref)
	}

	ctx, stopSig := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSig()

	return build.BuildImage(ctx, cli, build.Options{
		Tag:       ref,
		BuildArgs: build.BuildArgsFromTools(cfg.Tools),
		NoCache:   buildNoCache,
	})
}

func init() {
	buildCmd.Flags().BoolVar(&buildNoCache, "no-cache", false,
		"force a full rebuild, ignoring Docker's layer cache")
	rootCmd.AddCommand(buildCmd)
}
