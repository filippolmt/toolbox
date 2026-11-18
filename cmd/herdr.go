package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/client"
	"github.com/spf13/cobra"

	"github.com/filippolmt/toolbox/internal/container"
	"github.com/filippolmt/toolbox/internal/herdrcore"
	"github.com/filippolmt/toolbox/internal/mountplan"
	"github.com/filippolmt/toolbox/internal/runplan"
	"github.com/filippolmt/toolbox/internal/sessionplan"
)

var herdrProfile string // persistent on herdr: every subcommand acts on this profile's Herdr Core Shell
var herdrShare []string
var herdrShellShare []string
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

The workspace is the absolute dir argument, or the herdr_workspace config key,
or else the running Herdr Core Shell's. One Herdr Core Shell per host and
--profile. On a running one the command creates nothing and reports its
workspace. 'toolbox herdr shell' opens a terminal in it. The Herdr TUI
('herdr' inside a toolbox shell) is unrelated. See docs/herdr.md.`,
	Args: usageArgs(cobra.MaximumNArgs(1)),
	RunE: runHerdr,
}

var herdrStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop and remove the Herdr Core Shell; its state volume stays",
	Args:  usageArgs(cobra.NoArgs),
	RunE:  runHerdrStop,
}

var herdrShellCmd = &cobra.Command{
	Use:   "shell",
	Short: "Open a terminal in the running Herdr Core Shell; exiting leaves herdr-core running",
	Args:  usageArgs(cobra.NoArgs),
	RunE:  runHerdrShell,
}

// runHerdrShell attaches a terminal to the running Herdr Core Shell, planned
// off its workspace, so a recreate on the way in (refresh, Session Reload)
// keeps it the Herdr Core Shell; the re-entry form re-runs this command.
func runHerdrShell(cmd *cobra.Command, _ []string) error {
	profile, err := resolveShellProfile(cmd, herdrProfile, herdrShellShare)
	if err != nil {
		return err
	}
	env, err := runningHerdrCoreShellOf(profile)
	if err != nil {
		return err
	}
	return startSession(sessionIntent{
		Plan: sessionplan.PlanInput{
			Cfg:         cfg,
			Workspace:   sessionplan.EnvValue(env, sessionplan.HostWorkspaceEnv),
			Profile:     profile,
			HerdrCore:   true,
			HerdrAttach: true,
		},
		Reentry: append([]string{"herdr", "shell"}, reentryFlags(cmd.Flags(), nil)...),
	})
}

// runHerdr opens the Herdr Core Shell through startSession like `shell` does
// (the session plan makes it the Herdr Core Shell), then adds its machine.
func runHerdr(cmd *cobra.Command, args []string) error {
	profile, err := resolveShellProfile(cmd, herdrProfile, herdrShare)
	if err != nil {
		return err
	}
	var ws string // sessionplan checks it, the same way as herdr_workspace
	if len(args) == 1 {
		ws = args[0]
	}
	if ws == "" && cfg.HerdrWorkspace == "" {
		// A running Herdr Core Shell supplies the workspace: it is joined.
		if env, err := runningHerdrCoreShellOf(profile); err == nil {
			ws = sessionplan.EnvValue(env, sessionplan.HostWorkspaceEnv)
		}
	}
	warnIfNewProfile(hostBestEffort(), profile)
	return startSession(sessionIntent{
		Plan: sessionplan.PlanInput{Cfg: cfg, Workspace: ws, Profile: profile, HerdrCore: true},
		Around: func(ctx context.Context, cli client.APIClient, plan *sessionplan.SessionPlan) (func() error, error) {
			res, err := cli.ContainerInspect(ctx, plan.ContainerName, client.ContainerInspectOptions{})
			created := err != nil || res.Container.State == nil || !res.Container.State.Running || !runplan.IsDetached(res.Container)
			return func() error {
				if !created && res.Container.Config != nil {
					reportHerdrJoin(cmd.OutOrStdout(), plan, res.Container.Config.Env)
				}
				return addHerdrMachine(ctx, cli, plan.ContainerName, created, cmd)
			}, nil
		},
	})
}

// reportHerdrJoin says the Herdr Core Shell was already running and on which
// workspace, and that a different requested workspace needs a stop first.
func reportHerdrJoin(w io.Writer, plan *sessionplan.SessionPlan, runningEnv []string) {
	served := sessionplan.EnvValue(runningEnv, sessionplan.HostWorkspaceEnv)
	_, _ = fmt.Fprintf(w, "The Herdr Core Shell %s is already running on %s", plan.ContainerName, served)
	if want := sessionplan.EnvValue(plan.Env, sessionplan.HostWorkspaceEnv); want != served {
		_, _ = fmt.Fprintf(w, "; it keeps %s; to mount %s, toolbox herdr stop first", served, want)
	}
	_, _ = fmt.Fprintln(w, ".")
}

// runningHerdrCoreShellOf returns the env of the running Herdr Core Shell of
// profile, or an error naming the command that starts one.
func runningHerdrCoreShellOf(profile *mountplan.Profile) ([]string, error) {
	name, err := sessionplan.HerdrCoreContainerName(profile)
	if err != nil {
		return nil, err
	}
	return runningSingletonEnv(name, "", "no Herdr Core Shell "+name+" is running: start it with toolbox herdr")
}

// addHerdrMachine registers the Herdr Core Shell called shell with the host's
// herdr-core, or prints how to once the host has hc. A Shell this run created
// and herdr-core cannot register is removed: left running, it would be a
// detached container nothing uses.
func addHerdrMachine(ctx context.Context, cli client.APIClient, shell string, created bool, cmd *cobra.Command) error {
	machine := herdrcore.MachineName(shell)
	via, err := herdrcore.Via(shell)
	if err == nil {
		// A running Shell was maybe recreated without toolbox herdr (Session
		// Reload, start-up refresh): its layer may have lost herdr-core.
		fresh := func() (bool, error) {
			if created {
				return true, nil
			}
			ok, err := herdrcore.Installed(ctx, cli, shell)
			return !ok, err
		}
		err = registerHerdrMachine(machine, via, fresh)
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
// herdr-core already knows it on that connection. A known machine is updated
// when fresh reports a container without herdr-core (one this run created, or
// a recreate that lost the old container's layer); update reinstalls it. --reregister, or a machine of that name on
// another connection, removes it and adds it again.
func registerHerdrMachine(machine, via string, fresh func() (bool, error)) error {
	machines, err := herdrHost.Machines()
	if err != nil {
		return err
	}
	if i := slices.IndexFunc(machines, func(m herdrcore.Machine) bool { return m.Name == machine }); i >= 0 {
		// hc lists a --via machine's target as the --via command verbatim.
		same := machines[i].Via && machines[i].Target == via
		switch {
		case herdrReregister || !same:
			if err := herdrHost.Remove(machine); err != nil {
				return err
			}
		default:
			if missing, err := fresh(); err != nil || !missing {
				return err
			}
			return herdrHost.Update(machine)
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
	herdrShellCmd.Flags().StringSliceVar(&herdrShellShare, "share", nil,
		"under --profile, the --share the Herdr Core Shell was started with")
	herdrCmd.AddCommand(herdrStopCmd, herdrShellCmd)
	rootCmd.AddCommand(herdrCmd)
}
