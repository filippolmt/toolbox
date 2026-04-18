package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/container"
)

var shellCmd = &cobra.Command{
	Use:   "shell",
	Short: "Start an interactive shell session in the toolbox container",
	Long: `Start the toolbox container and attach an interactive bash session.
The current working directory is mounted at /workspace and the container
name is derived from that path, so each directory gets its own dedicated
container. If the container is already running, a new session is attached
to the existing one. The image is refreshed from the registry on every
invocation; if the pull fails, the locally available image is used.`,
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

	ctx := context.Background()
	return container.Shell(ctx, cli, cfg, workspace)
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
	rootCmd.AddCommand(shellCmd)
}
