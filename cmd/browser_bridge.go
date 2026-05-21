package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/filippolmt/toolbox/internal/browserbridge"
)

var browserBridgeCmd = &cobra.Command{
	Use:   "browser-bridge",
	Short: "Manage the host-side browser bridge (xdg-open forwarder)",
	Long: `Install, uninstall, inspect, or run the per-user daemon that forwards
URLs from inside 'toolbox shell' to the host's real browser. Opt-in: the
daemon only starts after 'toolbox browser-bridge install'.`,
}

var browserBridgeInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Generate token, write service file, start the daemon",
	Args:  usageArgs(cobra.NoArgs),
	RunE: func(_ *cobra.Command, _ []string) error {
		a, err := browserbridge.NewAgent()
		if err != nil {
			if errors.Is(err, browserbridge.ErrUnsupported) {
				return fmt.Errorf("browser-bridge: only macOS and Linux hosts are supported")
			}
			return err
		}
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		if err := browserbridge.Install(a, exe); err != nil {
			return err
		}
		fmt.Println("browser-bridge: installed and running")
		return nil
	},
}

var browserBridgeUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Stop the daemon, remove service file + state",
	Args:  usageArgs(cobra.NoArgs),
	RunE: func(_ *cobra.Command, _ []string) error {
		a, err := browserbridge.NewAgent()
		if err != nil {
			if errors.Is(err, browserbridge.ErrUnsupported) {
				return nil
			}
			return err
		}
		if err := browserbridge.Uninstall(a); err != nil {
			return err
		}
		fmt.Println("browser-bridge: uninstalled")
		return nil
	},
}

var browserBridgeStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Report install state, daemon liveness, port",
	Args:  usageArgs(cobra.NoArgs),
	RunE: func(_ *cobra.Command, _ []string) error {
		a, err := browserbridge.NewAgent()
		if err != nil {
			if errors.Is(err, browserbridge.ErrUnsupported) {
				fmt.Println("unsupported host")
				return nil
			}
			return err
		}
		rep, err := browserbridge.Status(a)
		if err != nil {
			return err
		}
		fmt.Printf("state dir:       %s\n", rep.StateDir)
		fmt.Printf("token present:   %v\n", rep.TokenPresent)
		fmt.Printf("port:            %d\n", rep.Port)
		fmt.Printf("agent installed: %v\n", rep.AgentInstalled)
		fmt.Printf("agent running:   %v\n", rep.AgentRunning)
		fmt.Printf("agent detail:    %s\n", rep.AgentDetail)
		return nil
	},
}

var browserBridgeDaemonCmd = &cobra.Command{
	Use:    "daemon",
	Short:  "Foreground daemon (invoked by LaunchAgent / systemd)",
	Hidden: true,
	Args:   usageArgs(cobra.NoArgs),
	RunE: func(_ *cobra.Command, _ []string) error {
		ctx, stop := signalCtx()
		defer stop()
		return browserbridge.Run(ctx, browserbridge.DaemonOptions{})
	},
}

func init() {
	browserBridgeCmd.AddCommand(browserBridgeInstallCmd)
	browserBridgeCmd.AddCommand(browserBridgeUninstallCmd)
	browserBridgeCmd.AddCommand(browserBridgeStatusCmd)
	browserBridgeCmd.AddCommand(browserBridgeDaemonCmd)
	rootCmd.AddCommand(browserBridgeCmd)
}
