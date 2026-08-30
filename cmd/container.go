package cmd

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/moby/moby/client"
	"github.com/spf13/cobra"

	"github.com/filippolmt/toolbox/internal/container"
	"github.com/filippolmt/toolbox/internal/ui"
)

// The `toolbox container` group acts on Orphaned Siblings — the containers a
// test suite starts from inside a shell through the host's Docker socket,
// which no Teardown reaches. Glossary: CONTEXT.md#orphaned-sibling,
// rationale: docs/adr/0004-orphaned-sibling-cleanup.md.
//
// The verbs mirror `toolbox worktree`: `rm <target>` removes what you name,
// `prune` takes no arguments and removes everything matching the criterion.

var (
	containerStopAll   bool
	containerRmVolumes bool
	containerPruneOpts struct {
		volumes bool
		dryRun  bool
	}
)

var containerCmd = &cobra.Command{
	Use:   "container",
	Short: "Act on containers on the host that toolbox did not create",
	Long: `Stop or remove the containers left behind on the host by something running
inside a toolbox shell — typically a 'docker compose up' in a test suite,
which has no reaper and whose containers outlive the shell that started them.

A target is either a Compose project (all of its containers, plus the networks
it created) or a single container carrying no Compose label. Shell completion
lists what is on the host right now; the values it offers are typed
('project:api', 'container:pg') so a project and a container that share a name
stay distinguishable.

Containers toolbox created are never targets — those belong to 'toolbox stop'.`,
}

var containerStopCmd = &cobra.Command{
	Use:   "stop [target...]",
	Short: "Stop containers toolbox did not create",
	Long: `Stop the targets named, leaving them in place so 'docker start' brings them
back. With --all, stop every target on the host except the proximo stack,
which stays reachable by name.`,
	Args:              usageArgs(cobra.ArbitraryArgs),
	ValidArgsFunction: completeSiblings,
	RunE:              runContainerStop,
}

var containerRmCmd = &cobra.Command{
	Use:   "rm <target...>",
	Short: "Stop and remove containers toolbox did not create",
	Long: `Stop the targets named and remove them: their containers, and for a Compose
project the networks it created. Volumes are kept unless --volumes is passed —
a network is free to recreate, a volume is where a test stack keeps data.

Each container is stopped with a SIGTERM grace before removal, never
force-killed.`,
	Args:              usageArgs(cobra.MinimumNArgs(1)),
	ValidArgsFunction: completeSiblings,
	RunE:              runContainerRm,
}

var containerPruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Remove every container toolbox did not create",
	Long: `Remove every target on the host — containers and, for Compose projects, the
networks they created. Takes no arguments: use 'toolbox container rm' to remove
a specific one.

The proximo stack is never removed, and the skip is reported rather than left
silent. Volumes are kept unless --volumes is passed. --dry-run lists what would
be removed without removing it.`,
	Args: usageArgs(cobra.NoArgs),
	RunE: runContainerPrune,
}

// newDockerClient is the Docker-client constructor this group uses, replaced
// in tests. Every verb here reaches the daemon before it can decide anything,
// so without this seam the decisions would only be reachable through a live
// daemon.
var newDockerClient = container.NewClient

// withSiblings opens the Docker client, lists the host's targets and hands
// them to fn. Every verb needs the same three steps.
func withSiblings(fn func(ctx context.Context, cli client.APIClient, sibs []container.Sibling) error) error {
	cli, err := newDockerClient()
	if err != nil {
		return err
	}
	defer cli.Close()

	ctx, stopSig := signalCtx()
	defer stopSig()

	sibs, err := container.Siblings(ctx, cli)
	if err != nil {
		return err
	}
	return fn(ctx, cli, sibs)
}

func runContainerStop(_ *cobra.Command, args []string) error {
	if containerStopAll && len(args) > 0 {
		return &usageError{err: fmt.Errorf("--all cannot be used with a target")}
	}
	if !containerStopAll && len(args) == 0 {
		return &usageError{err: fmt.Errorf("name a target or pass --all")}
	}

	return withSiblings(func(ctx context.Context, cli client.APIClient, sibs []container.Sibling) error {
		targets, err := selectTargets(sibs, args, containerStopAll)
		if err != nil || len(targets) == 0 {
			return err
		}
		return container.StopSiblings(ctx, cli, targets)
	})
}

func runContainerRm(_ *cobra.Command, args []string) error {
	return withSiblings(func(ctx context.Context, cli client.APIClient, sibs []container.Sibling) error {
		targets, err := container.ResolveSiblings(sibs, args)
		if err != nil {
			return err
		}
		return container.RemoveSiblings(ctx, cli, targets, containerRmVolumes)
	})
}

func runContainerPrune(cmd *cobra.Command, _ []string) error {
	return withSiblings(func(ctx context.Context, cli client.APIClient, sibs []container.Sibling) error {
		targets, err := selectTargets(sibs, nil, true)
		if err != nil || len(targets) == 0 {
			return err
		}
		if containerPruneOpts.dryRun {
			renderSiblings(cmd.OutOrStdout(), targets)
			return nil
		}
		return container.RemoveSiblings(ctx, cli, targets, containerPruneOpts.volumes)
	})
}

// selectTargets resolves the arguments, or — for the bulk forms — takes the
// whole criterion minus the proximo stack, announcing the skip. The same
// function serves `stop --all` and `prune` so the two cannot drift apart.
func selectTargets(sibs []container.Sibling, args []string, bulk bool) ([]container.Sibling, error) {
	if !bulk {
		return container.ResolveSiblings(sibs, args)
	}

	targets, skipped := container.BulkSiblings(sibs)
	for _, s := range skipped {
		ui.Warningf("Skipping %s — stop it by name if you mean it", s.Ref)
	}
	if len(targets) == 0 {
		ui.Warning("No containers to act on")
	}
	return targets, nil
}

// completeSiblings is the dynamic completion behind every verb in this group.
// A daemon that cannot be reached yields no suggestions and no error: cobra
// prints whatever a completion function writes straight into the user's
// prompt.
func completeSiblings(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	cli, err := newDockerClient()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	defer cli.Close()

	sibs, err := container.Siblings(context.Background(), cli)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	named := make(map[string]bool, len(args))
	for _, a := range args {
		named[a] = true
	}
	var out []string
	for _, entry := range siblingCompletions(sibs) {
		if ref, _, _ := strings.Cut(entry, "\t"); !named[ref] {
			out = append(out, entry)
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// siblingCompletions renders one completion entry per target: the typed ref as
// the value the shell inserts, everything human after the tab where cobra
// shows it as a description and never puts it on the command line.
func siblingCompletions(sibs []container.Sibling) []string {
	out := make([]string, 0, len(sibs))
	for _, s := range sibs {
		out = append(out, s.Ref+"\t"+siblingDescription(s))
	}
	return out
}

func siblingDescription(s container.Sibling) string {
	if s.Proximo {
		return "proximo stack — never removed by --all or prune"
	}
	if !s.IsProject() {
		return "standalone container"
	}
	return "Compose project, " + strconv.Itoa(len(s.IDs)) + " container(s), " + s.WorkDir
}

// renderSiblings prints the --dry-run listing: exactly the set the criterion
// selected, in the order the commands act on it.
func renderSiblings(out io.Writer, sibs []container.Sibling) {
	if len(sibs) == 0 {
		_, _ = fmt.Fprintln(out, "No containers to act on.")
		return
	}

	refW, cntW := len("TARGET"), len("CONTAINERS")
	for _, s := range sibs {
		refW = max(refW, len(s.Ref))
	}
	_, _ = fmt.Fprintf(out, "%-*s  %-*s  %s\n", refW, "TARGET", cntW, "CONTAINERS", "WORKDIR")
	for _, s := range sibs {
		_, _ = fmt.Fprintf(out, "%-*s  %-*d  %s\n", refW, s.Ref, cntW, len(s.IDs), s.WorkDir)
	}
}

func init() {
	containerStopCmd.Flags().BoolVar(&containerStopAll, "all", false,
		"stop every container toolbox did not create, except the proximo stack")
	containerRmCmd.Flags().BoolVar(&containerRmVolumes, "volumes", false,
		"also remove the volumes each Compose project owns (destroys their data)")
	containerPruneCmd.Flags().BoolVar(&containerPruneOpts.volumes, "volumes", false,
		"also remove the volumes each Compose project owns (destroys their data)")
	containerPruneCmd.Flags().BoolVar(&containerPruneOpts.dryRun, "dry-run", false,
		"list what would be removed without removing it")

	containerCmd.AddCommand(containerStopCmd, containerRmCmd, containerPruneCmd)
	rootCmd.AddCommand(containerCmd)
}
