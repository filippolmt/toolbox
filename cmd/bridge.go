package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/filippolmt/toolbox/internal/bridge"
)

var bridgeCmd = &cobra.Command{
	Use: "bridge",
	// Deprecated spelling. Installed LaunchAgents/systemd units keep invoking
	// `browser-bridge daemon` until the user reruns `toolbox bridge install`,
	// so the alias is load-bearing — remove only in a major release.
	Aliases: []string{"browser-bridge"},
	Short:   "Manage the host-side bridge (browser, editor, proximo forwarder)",
	Long: `Install, uninstall, inspect, or run the per-user daemon that forwards
URLs, editor opens, and proximo lifecycle commands from inside 'toolbox shell'
to the host. Opt-in: the daemon only starts after 'toolbox bridge install'.
'browser-bridge' is a deprecated alias for this command.`,
}

var bridgeInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Generate token, write service file, start the daemon",
	Args:  usageArgs(cobra.NoArgs),
	RunE: func(_ *cobra.Command, _ []string) error {
		a, err := bridge.NewAgent()
		if err != nil {
			if errors.Is(err, bridge.ErrUnsupported) {
				return fmt.Errorf("bridge: only macOS and Linux hosts are supported")
			}
			return err
		}
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		if err := bridge.Install(a, exe); err != nil {
			return err
		}
		fmt.Println("bridge: installed and running")
		if ok, advice := bridge.CheckHostCredentialHelper(); !ok {
			fmt.Fprintln(os.Stderr, "warning: "+advice)
		}
		return nil
	},
}

var bridgeUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Stop the daemon, remove service file + state",
	Args:  usageArgs(cobra.NoArgs),
	RunE: func(_ *cobra.Command, _ []string) error {
		a, err := bridge.NewAgent()
		if err != nil {
			if errors.Is(err, bridge.ErrUnsupported) {
				return nil
			}
			return err
		}
		if err := bridge.Uninstall(a); err != nil {
			return err
		}
		fmt.Println("bridge: uninstalled")
		return nil
	},
}

var bridgeStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Report install state, daemon liveness, port",
	Args:  usageArgs(cobra.NoArgs),
	RunE: func(_ *cobra.Command, _ []string) error {
		a, err := bridge.NewAgent()
		if err != nil {
			if errors.Is(err, bridge.ErrUnsupported) {
				fmt.Println("unsupported host")
				return nil
			}
			return err
		}
		rep, err := bridge.Status(a)
		if err != nil {
			return err
		}
		fmt.Printf("state dir:       %s\n", rep.StateDir)
		fmt.Printf("token present:   %v\n", rep.TokenPresent)
		fmt.Printf("port:            %d\n", rep.Port)
		if rep.SocketPath != "" {
			fmt.Printf("socket:          %s (present=%v)\n", rep.SocketPath, rep.SocketPresent)
		}
		fmt.Printf("agent installed: %v\n", rep.AgentInstalled)
		fmt.Printf("agent running:   %v\n", rep.AgentRunning)
		fmt.Printf("agent detail:    %s\n", rep.AgentDetail)
		return nil
	},
}

var bridgeDaemonCmd = &cobra.Command{
	Use:    "daemon",
	Short:  "Foreground daemon (invoked by LaunchAgent / systemd)",
	Hidden: true,
	Args:   usageArgs(cobra.NoArgs),
	RunE: func(_ *cobra.Command, _ []string) error {
		ctx, stop := signalCtx()
		defer stop()
		return bridge.Run(ctx, bridge.DaemonOptions{})
	},
}

func init() {
	bridgeCmd.AddCommand(bridgeInstallCmd)
	bridgeCmd.AddCommand(bridgeUninstallCmd)
	bridgeCmd.AddCommand(bridgeStatusCmd)
	bridgeCmd.AddCommand(bridgeDaemonCmd)
	rootCmd.AddCommand(bridgeCmd)
}
