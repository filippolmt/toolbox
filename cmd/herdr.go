package cmd

import (
	"context"
	"errors"
	"fmt"
	"slices"

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
var herdrReregister bool

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
container until 'toolbox herdr stop'. A re-run leaves a known machine alone,
or updates it when the run created the container (a new container has no hc);
--reregister removes and adds it again. Without hc on the host, the Shell
starts and the hc machine add command to run later is printed.

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
// herdr-core, or prints how to once the host has hc. A Shell this run created
// and herdr-core cannot register is removed: left running, it would be a
// detached container nothing uses.
func addHerdrMachine(ctx context.Context, cli client.APIClient, shell string, created bool, cmd *cobra.Command) error {
	machine := herdrcore.MachineName(shell)
	via, err := herdrcore.Via(shell)
	if err == nil {
		err = registerHerdrMachine(machine, via, created)
	}
	if errors.Is(err, herdrcore.ErrNoHC) {
		// via is a plain container name and fixed words: single quotes suffice.
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "The Herdr Core Shell %s is up, but this host has no hc (herdr-core) on its PATH. Once herdr-core is installed, register it with:\n  hc machine add --name %s --install --via '%s'\n", shell, machine, via)
		return nil
	}
	if err != nil {
		err = fmt.Errorf("herdr-core could not register the machine %s: %w", machine, err)
		if created {
			return errors.Join(err, container.StopByName(ctx, cli, shell))
		}
		return err
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "herdr-core reaches the Herdr Core Shell %s as the machine %s: open herdr.app.\n", shell, machine)
	return nil
}

// registerHerdrMachine adds machine reached through via unless the host's
// herdr-core already knows it. A known machine on a container this run created
// is updated: the hc herdr-core installed lived in the old container's layer.
// --reregister removes a known machine and adds it again.
func registerHerdrMachine(machine, via string, created bool) error {
	machines, err := herdrHost.Machines()
	if err != nil {
		return err
	}
	if slices.ContainsFunc(machines, func(m herdrcore.Machine) bool { return m.Name == machine }) {
		switch {
		case herdrReregister:
			if err := herdrHost.Remove(machine); err != nil {
				return err
			}
		case created:
			return herdrHost.Update(machine)
		default:
			return nil
		}
	}
	return herdrHost.Add(machine, via)
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
	herdrCmd.Flags().BoolVar(&herdrReregister, "reregister", false,
		"remove the machine from the host's herdr-core and add it again, reinstalling hc in the Shell")
	herdrCmd.AddCommand(herdrStopCmd)
	rootCmd.AddCommand(herdrCmd)
}
