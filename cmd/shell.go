package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/container"
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
	RunE: runShell,
}

func runShell(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	workspace, err := resolveWorkspace()
	if err != nil {
		return err
	}

	cli, err := container.NewClient()
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %w", err)
	}
	defer cli.Close()

	// Post-attach Ctrl+C reaches the container as a raw-mode byte; this
	// signal context only fires during pull/build or on external kill.
	ctx, stop := signalCtx()
	defer stop()

	return container.Shell(ctx, cli, cfg, workspace, shellPublish)
}

func resolveWorkspace() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current directory: %w", err)
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", fmt.Errorf("failed to resolve absolute path: %w", err)
	}
	return filepath.Clean(abs), nil
}

func init() {
	shellCmd.Flags().StringSliceVarP(&shellPublish, "publish", "p", nil,
		"publish a container port to the host (repeatable, e.g. 7171 or 127.0.0.1:7171:7171)")
	rootCmd.AddCommand(shellCmd)
}
