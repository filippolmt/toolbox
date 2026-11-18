package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/moby/moby/client"
	"github.com/spf13/cobra"

	"github.com/filippolmt/toolbox/internal/container"
	"github.com/filippolmt/toolbox/internal/orca"
	"github.com/filippolmt/toolbox/internal/runplan"
	"github.com/filippolmt/toolbox/internal/sessionplan"
	"github.com/filippolmt/toolbox/internal/ui"
	"github.com/filippolmt/toolbox/internal/workspace"
)

var orcaVersion string
var orcaProfile string
var orcaShare []string
var orcaStopProfile string

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
// will land, and returns the wait for it. A running Orca Shell is joined, and
// the last record already in the log is its server's; a new one appends past
// what the log holds now, which may end in an earlier container's record.
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
		if errors.Is(err, orca.ErrNotStarted) {
			// Nothing will ever serve in it: remove it rather than leave it running empty.
			return errors.Join(err, container.StopByName(ctx, cli, plan.ContainerName))
		}
		if err != nil {
			return fmt.Errorf("%w (the Orca Shell %s keeps running; toolbox orca stop removes it)", err, plan.ContainerName)
		}
		if warnings, err := orca.Warnings(logPath, offset); err == nil {
			for _, w := range warnings {
				ui.Warning(w)
			}
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Orca is serving on 127.0.0.1:%s. Paste this link in Orca (Settings → Remote Orca Servers → Add Server):\n%s\n", sessionplan.OrcaPort, url)
		return nil
	}, nil
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
	orcaCmd.AddCommand(orcaStopCmd)
	rootCmd.AddCommand(orcaCmd)
}
