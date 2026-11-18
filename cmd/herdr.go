package cmd

import (
	"context"
	"errors"
	"fmt"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/client"
	"github.com/spf13/cobra"

	"github.com/filippolmt/toolbox/internal/container"
	"github.com/filippolmt/toolbox/internal/herdrcore"
	"github.com/filippolmt/toolbox/internal/runplan"
	"github.com/filippolmt/toolbox/internal/sessionplan"
)

var herdrProfile string // persistent on herdr: every subcommand acts on this profile's Herdr Core Shell
var herdrShare []string

// herdrHost is the host's herdr-core the Herdr Core Shell registers with;
// tests swap it.
var herdrHost herdrcore.Host = herdrcore.HostCLI()

var herdrCmd = &cobra.Command{
	Use:   "herdr [dir]",
	Short: "Start herdr-core in a detached Herdr Core Shell and add it to the Herdr desktop app",
	Long: `Start the Herdr Core Shell: a detached toolbox container that hosts
herdr-core for the Herdr desktop app (herdr.app). The command registers it with
the host's herdr-core (hc) as the machine toolbox (toolbox-<profile>), reached
through 'docker exec' rather than ssh; herdr-core installs its own hc in the
container. Spaces on that machine run their terminals and agents in the
container until 'toolbox herdr stop'.

The workspace is the absolute dir argument. One Herdr Core Shell per host and
--profile. The Herdr TUI ('herdr' inside a toolbox shell) is unrelated. See
docs/herdr.md.`,
	Args: usageArgs(cobra.MaximumNArgs(1)),
	RunE: runHerdr,
}

var herdrStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop and remove the Herdr Core Shell; its state volume stays",
	Args:  usageArgs(cobra.NoArgs),
	RunE:  runHerdrStop,
}

// runHerdr opens the Herdr Core Shell through startSession like `shell` does
// (the session plan makes it the Herdr Core Shell), then adds its machine.
func runHerdr(cmd *cobra.Command, args []string) error {
	profile, err := resolveShellProfile(cmd, herdrProfile, herdrShare)
	if err != nil {
		return err
	}
	var ws string // sessionplan checks it
	if len(args) == 1 {
		ws = args[0]
	}
	warnIfNewProfile(hostBestEffort(), profile)
	return startSession(sessionIntent{
		Plan: sessionplan.PlanInput{Cfg: cfg, Workspace: ws, Profile: profile, HerdrCore: true},
		Around: func(ctx context.Context, cli client.APIClient, plan *sessionplan.SessionPlan) (func() error, error) {
			res, err := cli.ContainerInspect(ctx, plan.ContainerName, client.ContainerInspectOptions{})
			created := err != nil || res.Container.State == nil || !res.Container.State.Running || !runplan.IsDetached(res.Container)
			return func() error { return addHerdrMachine(ctx, cli, plan.ContainerName, created, cmd) }, nil
		},
	})
}

// addHerdrMachine registers the Herdr Core Shell called shell with the host's
// herdr-core. A Shell this run created and herdr-core cannot reach is removed:
// left running, it would be a detached container nothing uses.
func addHerdrMachine(ctx context.Context, cli client.APIClient, shell string, created bool, cmd *cobra.Command) error {
	machine := herdrcore.MachineName(shell)
	via, err := herdrcore.Via(shell)
	if err == nil {
		err = herdrHost.Add(machine, via)
	}
	if err != nil {
		err = fmt.Errorf("herdr-core could not add the machine %s: %w", machine, err)
		if created {
			return errors.Join(err, container.StopByName(ctx, cli, shell))
		}
		return err
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "herdr-core reaches the Herdr Core Shell %s as the machine %s: open herdr.app.\n", shell, machine)
	return nil
}

func runHerdrStop(cmd *cobra.Command, _ []string) error {
	profile, err := resolveShellProfile(cmd, herdrProfile, nil)
	if err != nil {
		return err
	}
	name, err := sessionplan.HerdrCoreContainerName(profile)
	if err != nil {
		return err
	}
	return withDocker(func(ctx context.Context, cli client.APIClient) error {
		// No Herdr Core Shell is the goal already reached: quiet, unlike `toolbox stop`.
		if _, err := cli.ContainerInspect(ctx, name, client.ContainerInspectOptions{}); cerrdefs.IsNotFound(err) {
			return nil
		}
		return container.StopByName(ctx, cli, name)
	})
}

func init() {
	herdrCmd.PersistentFlags().StringVar(&herdrProfile, "profile", "",
		"the toolbox profile whose Herdr Core Shell to act on (its own credentials and herdr-core state)")
	herdrCmd.Flags().StringSliceVar(&herdrShare, "share", nil,
		"under --profile, keep the named tools on the host's ~/.toolbox/ root (see toolbox shell --share)")
	herdrCmd.AddCommand(herdrStopCmd)
	rootCmd.AddCommand(herdrCmd)
}
