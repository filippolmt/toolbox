package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/client"
	"github.com/spf13/cobra"

	"github.com/filippolmt/toolbox/internal/container"
	"github.com/filippolmt/toolbox/internal/orca"
	"github.com/filippolmt/toolbox/internal/runplan"
	"github.com/filippolmt/toolbox/internal/sessionplan"
	"github.com/filippolmt/toolbox/internal/workspace"
)

var orcaVersion string
var orcaProfile string
var orcaShare []string
var orcaStopProfile string
var orcaShellProfile string
var orcaShellShare []string

// orcaReadyTimeout bounds the wait for the pairing link: a first start
// downloads the server before it runs.
const orcaReadyTimeout = 3 * time.Minute

var orcaCmd = &cobra.Command{
	Use:   "orca [dir]",
	Short: "Start Orca's runtime in a detached Orca Shell and print the pairing link",
	Long: `Start the Orca Shell: a detached toolbox container that runs the Orca
server (stablyai/orca) at the version --version names, published on host
127.0.0.1:` + sessionplan.OrcaPort + `. The command returns once the server is ready and
prints the link to paste into the Orca desktop app (Settings → Remote Orca
Servers → Add Server). The runtime keeps running until 'toolbox orca stop'.

The workspace is the absolute dir argument, or the orca_workspace config key.
One Orca Shell per host and --profile. A different --version needs
'toolbox orca stop' first. See docs/orca.md.`,
	Args: usageArgs(cobra.MaximumNArgs(1)),
	RunE: runOrca,
}

var orcaStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop and remove the Orca Shell",
	Args:  usageArgs(cobra.NoArgs),
	RunE:  runOrcaStop,
}

// runOrca validates the flags before any container work, then opens the Orca
// Shell through startSession like `shell` does; the session plan makes it the
// Orca Shell.
func runOrca(cmd *cobra.Command, args []string) error {
	version, err := orca.NormalizeVersion(orcaVersion)
	if err != nil {
		return &usageError{err: err}
	}
	profile, err := resolveShellProfile(cmd, orcaProfile, orcaShare)
	if err != nil {
		return err
	}
	var ws string
	if len(args) == 1 {
		if ws, err = workspace.ResolveExplicit(args[0]); err != nil {
			return err
		}
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
		url, err := orca.WaitPairing(logPath, offset, orcaReadyTimeout)
		if err != nil {
			return fmt.Errorf("%w (the Orca Shell %s keeps running; toolbox orca stop removes it)", err, plan.ContainerName)
		}
		if joining && res.Container.Config != nil {
			running := sessionplan.EnvValue(res.Container.Config.Env, sessionplan.OrcaVersionEnv)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "The Orca Shell %s is already running Orca %s", plan.ContainerName, running)
			if want := sessionplan.EnvValue(plan.Env, sessionplan.OrcaVersionEnv); want != running {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "; to run %s, toolbox orca stop first", want)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), ".")
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Orca is serving on 127.0.0.1:%s. Paste this link in Orca (Settings → Remote Orca Servers → Add Server):\n%s\n", sessionplan.OrcaPort, url)
		return nil
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
	profile, err := resolveShellProfile(cmd, orcaShellProfile, orcaShellShare)
	if err != nil {
		return err
	}
	name, err := sessionplan.OrcaContainerName(profile)
	if err != nil {
		return err
	}
	env, err := runningOrcaShellEnv(name)
	if err != nil {
		return err
	}
	return startSession(sessionIntent{
		Plan: sessionplan.PlanInput{
			Cfg:         cfg,
			Workspace:   sessionplan.EnvValue(env, "TOOLBOX_HOST_WORKSPACE"),
			Profile:     profile,
			OrcaVersion: sessionplan.EnvValue(env, sessionplan.OrcaVersionEnv),
			OrcaAttach:  true,
		},
		Reentry: append([]string{"orca", "shell"}, reentryFlags(cmd.Flags(), nil)...),
	})
}

// runningOrcaShellEnv returns the env of the running Orca Shell called name,
// or an error naming the command that starts one.
func runningOrcaShellEnv(name string) ([]string, error) {
	cli, err := newDockerClient()
	if err != nil {
		return nil, dockerClientErr(err)
	}
	defer cli.Close()
	ctx, stop := signalCtx()
	defer stop()
	res, err := cli.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
	if err != nil && !cerrdefs.IsNotFound(err) {
		return nil, err
	}
	c := res.Container
	if err != nil || c.State == nil || !c.State.Running || !runplan.IsDetached(c) || c.Config == nil ||
		sessionplan.EnvValue(c.Config.Env, sessionplan.OrcaVersionEnv) == "" {
		return nil, fmt.Errorf("no Orca Shell %s is running: start it with toolbox orca --version <version>", name)
	}
	return c.Config.Env, nil
}

func runOrcaStop(cmd *cobra.Command, _ []string) error {
	profile, err := resolveShellProfile(cmd, orcaStopProfile, nil)
	if err != nil {
		return err
	}
	name, err := sessionplan.OrcaContainerName(profile)
	if err != nil {
		return err
	}
	cli, err := container.NewClient()
	if err != nil {
		return err
	}
	defer cli.Close()
	ctx, stopSig := signalCtx()
	defer stopSig()
	return container.StopByName(ctx, cli, name)
}

func init() {
	orcaCmd.Flags().StringVar(&orcaVersion, "version", "",
		"Orca server release to run (e.g. 1.4.262); match your desktop app's version")
	_ = orcaCmd.MarkFlagRequired("version")
	orcaCmd.Flags().StringVar(&orcaProfile, "profile", "",
		"run the Orca Shell under this toolbox profile (its own credentials and Orca data)")
	orcaCmd.Flags().StringSliceVar(&orcaShare, "share", nil,
		"under --profile, keep the named tools on the host's ~/.toolbox/ root (see toolbox shell --share)")
	orcaStopCmd.Flags().StringVar(&orcaStopProfile, "profile", "", "stop the Orca Shell of this toolbox profile")
	orcaShellCmd.Flags().StringVar(&orcaShellProfile, "profile", "", "open a terminal in the Orca Shell of this toolbox profile")
	orcaShellCmd.Flags().StringSliceVar(&orcaShellShare, "share", nil,
		"under --profile, the --share the Orca Shell was started with")
	orcaCmd.AddCommand(orcaStopCmd, orcaShellCmd)
	rootCmd.AddCommand(orcaCmd)
}
