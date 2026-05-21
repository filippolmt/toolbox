package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/filippolmt/toolbox/internal/browserbridge"
	"github.com/filippolmt/toolbox/internal/container"
	"github.com/filippolmt/toolbox/internal/sessionplan"
	"github.com/filippolmt/toolbox/internal/version"
)

var shellPublish []string
var shellCreate bool
var shellPath string

var shellCmd = &cobra.Command{
	Use:   "shell [name|dir]",
	Short: "Start an interactive shell session in the toolbox container",
	Long: `Start the toolbox container and attach an interactive bash session.

Without arguments the current working directory is mounted at /workspace
and the container name is derived from that path, so each directory gets
its own dedicated container. If the container is already running, a new
session is attached to the existing one.

The positional argument is either:
  - a configured shell name from ~/.toolbox.yaml's shells: map, or
  - an absolute path for a one-shot session (e.g. "toolbox shell /tmp") —
    no config is read or written, the container name still derives from
    the path hash so re-running on the same path reattaches.

Use --publish/-p to forward a host port into the container. Accepts the
same formats as "docker run -p" (e.g. "7171", "7171:7171",
"127.0.0.1:7171:7171"). When the host IP is omitted it defaults to
127.0.0.1 — useful for OAuth callbacks from tools like gh/glab that listen
on localhost inside the container.`,
	Args: usageArgs(cobra.MaximumNArgs(1)),
	RunE: runShell,
}

func runShell(cmd *cobra.Command, args []string) error {
	ws, shellName, err := resolveShellWorkspace(args, shellCreate, shellPath)
	if err != nil {
		return err
	}

	if cfg.BrowserBridge != nil && *cfg.BrowserBridge {
		printBrowserBridgeTipIfNeeded()
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
	if shellName != "" {
		plan.ContainerName = sessionplan.NamedContainerNameFromSanitized(shellName)
	}

	// Post-attach Ctrl+C reaches the container as a raw-mode byte; this
	// signal context only fires during pull/build or on external kill.
	ctx, stop := signalCtx()
	defer stop()

	return container.Shell(ctx, cli, plan)
}

// printBrowserBridgeTipIfNeeded prints a one-line install hint when the
// host-side browser bridge is not yet installed. Build tags select an Agent
// that returns ErrUnsupported on non-darwin/linux, which short-circuits here.
// Uses IsInstalled (stat-only) instead of Status to keep the shell-start hot
// path off launchctl/systemctl exec costs.
func printBrowserBridgeTipIfNeeded() {
	a, err := browserbridge.NewAgent()
	if err != nil {
		return
	}
	if a.IsInstalled() {
		return
	}
	fmt.Fprintln(os.Stderr, "toolbox: tip — run 'toolbox browser-bridge install' to forward in-container URLs to your host browser")
}

func init() {
	shellCmd.Flags().StringSliceVarP(&shellPublish, "publish", "p", nil,
		"publish a container port to the host (repeatable). Format: '[host_ip:]host_port:container_port' or 'port'. "+
			"Examples: 7171, 7171:7171, 127.0.0.1:7171:7171, 0.0.0.0:8000:8000. "+
			"Host IP defaults to 127.0.0.1. Bindings apply only at container creation — run 'toolbox stop' to refresh.")
	shellCmd.Flags().BoolVar(&shellCreate, "create", false, "Auto-bootstrap a missing named shell in ~/.toolbox.yaml")
	shellCmd.Flags().StringVar(&shellPath, "path", "", "Path to use with --create (default: $HOME/toolbox-shells/<name>; falls back to /tmp/<name> when home is unresolvable)")
	rootCmd.AddCommand(shellCmd)
}
