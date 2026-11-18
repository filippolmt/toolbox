package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/client"
	"github.com/spf13/cobra"

	"github.com/filippolmt/toolbox/internal/container"
	"github.com/filippolmt/toolbox/internal/mountplan"
	"github.com/filippolmt/toolbox/internal/orca"
	"github.com/filippolmt/toolbox/internal/runplan"
	"github.com/filippolmt/toolbox/internal/sessionplan"
	"github.com/filippolmt/toolbox/internal/ui"
)

var orcaVersion string
var orcaProfile string // persistent on orca: every subcommand acts on this profile's Orca Shell
var orcaShare []string
var orcaShellShare []string

// orcaOffer asks the running Orca Shell for a pairing link; tests swap it.
var orcaOffer = func(ctx context.Context, cli client.APIClient, container string) (string, error) {
	return orca.Offer(ctx, cli, container)
}

// orcaDesktop is the Orca desktop app runOrca takes the server version from
// when --version is unset, and registers the runtime with; tests swap it.
var orcaDesktop = orca.HostDesktop()

// orcaReadyTimeout bounds the wait for the pairing link: a first start
// downloads the server before it runs.
const orcaReadyTimeout = 3 * time.Minute

var orcaCmd = &cobra.Command{
	Use:   "orca [dir]",
	Short: "Start Orca's runtime in a detached Orca Shell and pair the desktop app with it",
	Long: `Start the Orca Shell: a detached toolbox container that runs the Orca
server (stablyai/orca) at the Orca desktop app's version (--version
overrides it), published on host 127.0.0.1:` + sessionplan.OrcaPort + `. The command returns once the server is ready and
the desktop app lists it as the environment toolbox (toolbox-<profile>);
'toolbox orca pair' pairs it again. The runtime keeps running until
'toolbox orca stop'.

The workspace is the absolute dir argument, or the orca_workspace config key,
or else the running Orca Shell's.
One Orca Shell per host and --profile. On a running one the command creates
nothing and reports its state; a different server version needs 'toolbox
orca stop' first. 'toolbox orca shell' opens a terminal in it. See
docs/orca.md.`,
	Args: usageArgs(cobra.MaximumNArgs(1)),
	RunE: runOrca,
}

var orcaStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop and remove the Orca Shell",
	Args:  usageArgs(cobra.NoArgs),
	RunE:  runOrcaStop,
}

var orcaPairCmd = &cobra.Command{
	Use:   "pair",
	Short: "Pair the Orca desktop app with the running Orca Shell again",
	Long: `Remove the Orca Shell's environment (toolbox, or toolbox-<profile>) from
the Orca desktop app and add it again from a fresh pairing offer: the way back
after the server's identity changed. The Orca Shell must be running.`,
	Args: usageArgs(cobra.NoArgs),
	RunE: runOrcaPair,
}

// runOrca validates the flags before any container work (only an inspect for
// a running Orca Shell when no version or no workspace resolves), then opens the Orca Shell
// through startSession like `shell` does; the session plan makes it the Orca
// Shell.
func runOrca(cmd *cobra.Command, args []string) error {
	profile, err := resolveShellProfile(cmd, orcaProfile, orcaShare)
	if err != nil {
		return err
	}
	var ws string // sessionplan checks it, the same way as orca_workspace
	if len(args) == 1 {
		ws = args[0]
	}
	version, err := orca.ResolveVersion(orcaVersion, orcaDesktop)
	noVersion := err != nil && orcaVersion == ""
	noWorkspace := ws == "" && cfg.OrcaWorkspace == ""
	if noVersion || noWorkspace {
		// What the command cannot resolve, a running Orca Shell supplies: it
		// is joined and reported at its own version and workspace.
		if env, runErr := runningOrcaShellOf(profile); runErr == nil {
			if noVersion {
				version, err = sessionplan.EnvValue(env, sessionplan.OrcaVersionEnv), nil
			}
			if noWorkspace {
				ws = sessionplan.EnvValue(env, sessionplan.HostWorkspaceEnv)
			}
		}
	}
	if err != nil {
		return &usageError{err: err}
	}
	warnIfNewProfile(hostBestEffort(), profile)

	return startSession(sessionIntent{
		Plan: sessionplan.PlanInput{
			Cfg:         cfg,
			Workspace:   ws,
			Profile:     profile,
			OrcaVersion: version,
		},
		Around: func(ctx context.Context, cli client.APIClient, plan *sessionplan.SessionPlan) (func() error, error) {
			return awaitPairing(ctx, cli, plan, cmd)
		},
	})
}

// awaitPairing notes where in the server log this start's readiness record
// will land, and returns the wait for it. A running Orca Shell is joined: the
// log as of its latest server start says whether that server still serves,
// and the report says it was already running. A new one appends past what the
// log holds now, which may end in an earlier container's record.
func awaitPairing(ctx context.Context, cli client.APIClient, plan *sessionplan.SessionPlan, cmd *cobra.Command) (func() error, error) {
	logPath := plan.OrcaLogPath()
	var offset int64
	res, err := cli.ContainerInspect(ctx, plan.ContainerName, client.ContainerInspectOptions{})
	joining := err == nil && res.Container.State != nil && res.Container.State.Running && runplan.IsDetached(res.Container)
	if info, statErr := os.Stat(logPath); statErr == nil && !joining {
		offset = info.Size()
	}
	return func() error {
		_, err := orca.WaitPairing(logPath, offset, orcaReadyTimeout)
		if errors.Is(err, orca.ErrNotStarted) {
			// Nothing will ever serve in it: remove it rather than leave it running empty.
			return errors.Join(err, container.StopByName(ctx, cli, plan.ContainerName))
		}
		if err != nil {
			return fmt.Errorf("%w (the Orca Shell %s keeps running; toolbox orca stop removes it)", err, plan.ContainerName)
		}
		if joining && res.Container.Config != nil {
			reportJoin(cmd.OutOrStdout(), plan, res.Container.Config.Env)
		}
		if warnings, err := orca.Warnings(logPath, offset); err == nil {
			for _, w := range warnings {
				ui.Warning(w)
			}
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Orca is serving on 127.0.0.1:%s.\n", sessionplan.OrcaPort)
		return pairOrcaShell(ctx, cli, plan.ContainerName, false, cmd)
	}, nil
}

var orcaShellCmd = &cobra.Command{
	Use:   "shell",
	Short: "Open a terminal in the running Orca Shell; exiting leaves Orca running",
	Args:  usageArgs(cobra.NoArgs),
	RunE:  runOrcaShell,
}

// runOrcaShell attaches a terminal to the running Orca Shell. The plan takes
// the server version and workspace off that container, so a recreate on the
// way in (refresh, Session Reload) brings the server back; the re-entry form
// re-runs this command, not `toolbox shell`.
func runOrcaShell(cmd *cobra.Command, _ []string) error {
	profile, err := resolveShellProfile(cmd, orcaProfile, orcaShellShare)
	if err != nil {
		return err
	}
	env, err := runningOrcaShellOf(profile)
	if err != nil {
		return err
	}
	return startSession(sessionIntent{
		Plan: sessionplan.PlanInput{
			Cfg:         cfg,
			Workspace:   sessionplan.EnvValue(env, sessionplan.HostWorkspaceEnv),
			Profile:     profile,
			OrcaVersion: sessionplan.EnvValue(env, sessionplan.OrcaVersionEnv),
			OrcaAttach:  true,
		},
		Reentry: append([]string{"orca", "shell"}, reentryFlags(cmd.Flags(), nil)...),
	})
}

// runningOrcaShellOf returns the env of the running Orca Shell of profile.
func runningOrcaShellOf(profile *mountplan.Profile) ([]string, error) {
	name, err := sessionplan.OrcaContainerName(profile)
	if err != nil {
		return nil, err
	}
	return runningOrcaShellEnv(name)
}

// runningOrcaShellEnv returns the env of the running Orca Shell called name,
// or an error naming the command that starts one.
func runningOrcaShellEnv(name string) ([]string, error) {
	var env []string
	err := withDocker(func(ctx context.Context, cli client.APIClient) error {
		res, err := cli.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
		if err != nil && !cerrdefs.IsNotFound(err) {
			return err
		}
		c := res.Container
		if err != nil || c.State == nil || !c.State.Running || !runplan.IsDetached(c) || c.Config == nil ||
			sessionplan.EnvValue(c.Config.Env, sessionplan.OrcaVersionEnv) == "" {
			return fmt.Errorf("no Orca Shell %s is running: start it with toolbox orca", name)
		}
		env = c.Config.Env
		return nil
	})
	return env, err
}

// withDocker runs fn with the Docker client every orca subcommand uses and a
// context cancelled on SIGINT/SIGTERM.
func withDocker(fn func(context.Context, client.APIClient) error) error {
	cli, err := newDockerClient()
	if err != nil {
		return dockerClientErr(err)
	}
	defer cli.Close()
	ctx, stop := signalCtx()
	defer stop()
	return fn(ctx, cli)
}

// reportJoin says the Orca Shell was already running, at which version, and
// that a different requested version or workspace needs a stop first.
func reportJoin(w io.Writer, plan *sessionplan.SessionPlan, runningEnv []string) {
	running := sessionplan.EnvValue(runningEnv, sessionplan.OrcaVersionEnv)
	_, _ = fmt.Fprintf(w, "The Orca Shell %s is already running Orca %s", plan.ContainerName, running)
	if want := sessionplan.EnvValue(plan.Env, sessionplan.OrcaVersionEnv); want != running {
		_, _ = fmt.Fprintf(w, "; to run %s, toolbox orca stop first", want)
	}
	served := sessionplan.EnvValue(runningEnv, sessionplan.HostWorkspaceEnv)
	if want := sessionplan.EnvValue(plan.Env, sessionplan.HostWorkspaceEnv); want != served {
		_, _ = fmt.Fprintf(w, "; it keeps serving %s; to serve %s, toolbox orca stop first", served, want)
	}
	_, _ = fmt.Fprintln(w, ".")
}

// pairOrcaShell registers the Orca Shell named shell with the desktop app.
func pairOrcaShell(ctx context.Context, cli client.APIClient, shell string, force bool, cmd *cobra.Command) error {
	offer := func() (string, error) { return orcaOffer(ctx, cli, shell) }
	return orca.Pair(orcaDesktop, orca.EnvironmentName(shell), force, offer, cmd.OutOrStdout())
}

func runOrcaPair(cmd *cobra.Command, _ []string) error {
	name, err := orcaShellName(cmd)
	if err != nil {
		return err
	}
	return withDocker(func(ctx context.Context, cli client.APIClient) error {
		return pairOrcaShell(ctx, cli, name, true, cmd)
	})
}

func runOrcaStop(cmd *cobra.Command, _ []string) error {
	name, err := orcaShellName(cmd)
	if err != nil {
		return err
	}
	return withDocker(func(ctx context.Context, cli client.APIClient) error {
		// No Orca Shell is the goal already reached: quiet, unlike `toolbox stop`.
		if _, err := cli.ContainerInspect(ctx, name, client.ContainerInspectOptions{}); cerrdefs.IsNotFound(err) {
			return nil
		}
		return container.StopByName(ctx, cli, name)
	})
}

// orcaShellName is the container name of the --profile's Orca Shell.
func orcaShellName(cmd *cobra.Command) (string, error) {
	profile, err := resolveShellProfile(cmd, orcaProfile, nil)
	if err != nil {
		return "", err
	}
	return sessionplan.OrcaContainerName(profile)
}

func init() {
	orcaCmd.PersistentFlags().StringVar(&orcaProfile, "profile", "",
		"the toolbox profile whose Orca Shell to act on (its own credentials and Orca data)")
	orcaCmd.Flags().StringVar(&orcaVersion, "version", "",
		"Orca server release to run (e.g. 1.4.262); default: the installed Orca desktop app's version")
	orcaCmd.Flags().StringSliceVar(&orcaShare, "share", nil,
		"under --profile, keep the named tools on the host's ~/.toolbox/ root (see toolbox shell --share)")
	orcaShellCmd.Flags().StringSliceVar(&orcaShellShare, "share", nil,
		"under --profile, the --share the Orca Shell was started with")
	orcaCmd.AddCommand(orcaStopCmd, orcaPairCmd, orcaShellCmd)
	rootCmd.AddCommand(orcaCmd)
}
