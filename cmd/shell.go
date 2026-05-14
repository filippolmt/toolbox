package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/filippolmt/toolbox/internal/container"
	"github.com/filippolmt/toolbox/internal/sessionplan"
	"github.com/filippolmt/toolbox/internal/version"
	"github.com/filippolmt/toolbox/internal/workspace"
)

var shellPublish []string

var shellCmd = &cobra.Command{
	Use:   "shell",
	Short: "Start an interactive shell session in the toolbox container",
	Long: `Start the toolbox container and attach an interactive bash session.
The current working directory is mounted at /workspace and the container
name is derived from that path, so each directory gets its own dedicated
container. If the container is already running, a new session is attached
to the existing one.

Use --publish/-p to forward a host port into the container. Accepts the
same formats as "docker run -p" (e.g. "7171", "7171:7171",
"127.0.0.1:7171:7171"). When the host IP is omitted it defaults to
127.0.0.1 — useful for OAuth callbacks from tools like gh/glab that listen
on localhost inside the container.`,
	Args: usageArgs(cobra.NoArgs),
	RunE: runShell,
}

func runShell(cmd *cobra.Command, args []string) error {
	ws, err := workspace.Resolve()
	if err != nil {
		return err
	}

	cli, err := container.NewClient()
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %w", err)
	}
	defer cli.Close()

	// Plan after the Docker client is constructed so a failed client init
	// (env parse / socket misconfig) does not leave behind mountplan.Plan
	// fs side effects under ~/.toolbox and the workspace.
	plan, err := sessionplan.Plan(cfg, ws, shellPublish, version.Version)
	if err != nil {
		return err
	}

	// Post-attach Ctrl+C reaches the container as a raw-mode byte; this
	// signal context only fires during pull/build or on external kill.
	ctx, stop := signalCtx()
	defer stop()

	return container.Shell(ctx, cli, plan)
}

func init() {
	shellCmd.Flags().StringSliceVarP(&shellPublish, "publish", "p", nil,
		"publish a container port to the host (repeatable). Format: '[host_ip:]host_port:container_port' or 'port'. "+
			"Examples: 7171, 7171:7171, 127.0.0.1:7171:7171, 0.0.0.0:8000:8000. "+
			"Host IP defaults to 127.0.0.1. Bindings apply only at container creation — run 'toolbox stop' to refresh.")
	rootCmd.AddCommand(shellCmd)
}
