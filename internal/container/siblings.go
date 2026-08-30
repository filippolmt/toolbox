package container

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/proximo"
	"github.com/filippolmt/toolbox/internal/sessionplan"
	"github.com/filippolmt/toolbox/internal/ui"
)

// Orphaned Sibling cleanup: the containers a test suite starts from inside a
// toolbox shell through the host's Docker socket, which no Teardown reaches.
// Glossary: CONTEXT.md#orphaned-sibling. Rationale, verbs and blast radius:
// docs/adr/0004-orphaned-sibling-cleanup.md.

// Compose stamps these on every container, network and volume it creates.
// Project is the grouping key; WorkingDir is both the provenance shown in
// completion and the only thing that separates two projects whose names
// collide — the default project name is the basename of the directory, so two
// unrelated repos called "api" produce two projects called "api".
const (
	composeProjectLabel = "com.docker.compose.project"
	composeWorkDirLabel = "com.docker.compose.project.working_dir"
)

// Ref prefixes. A target is addressed by a typed handle rather than a bare
// name so a project and a container that share a name stay distinguishable,
// and so completion can hand the shell values that are unique by
// construction.
const (
	projectRefPrefix   = "project:"
	containerRefPrefix = "container:"
)

// Sibling is one cleanup target: a Compose project with all its containers,
// or a single container carrying no Compose label.
type Sibling struct {
	// Ref is the unique typed handle: "project:<name>", "container:<name>",
	// or "project:<name>@<workdir>" when two projects share a name.
	Ref string
	// Name is the Compose project name, or the container name for a
	// standalone target. It is the label value the network and volume
	// lookups filter on.
	Name string
	// WorkDir is the Compose working directory; empty for a standalone
	// container.
	WorkDir string
	// IDs are the container IDs the target covers.
	IDs []string
	// Proximo marks the proximo stack: offered by name, never swept in bulk.
	Proximo bool
}

// IsProject reports whether the target is a Compose project rather than a
// standalone container. Networks and volumes only exist for the former.
func (s Sibling) IsProject() bool { return strings.HasPrefix(s.Ref, projectRefPrefix) }

// Siblings returns every Orphaned Sibling on the host, sorted by Ref. It is
// the single criterion behind all three consumers — shell completion,
// `container stop --all` and `container prune` — so that what the shell
// offers is what a bulk sweep acts on.
//
// Containers toolbox created are never targets: they are the business of
// `toolbox stop`, and letting this command reach them would give it the one
// property that makes `toolbox stop` safe to run blind.
func Siblings(ctx context.Context, cli client.APIClient) ([]Sibling, error) {
	list, err := cli.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	// Keyed by project name plus working directory: same name, different
	// directory means two unrelated stacks that must not be merged.
	groups := make(map[string]*Sibling)
	var order []string
	for _, c := range list.Items {
		if len(c.Names) == 0 {
			continue
		}
		name := containerName(c)
		if sessionplan.IsToolboxContainerName(name) {
			continue
		}

		key, sib := siblingOf(c, name)
		if existing, ok := groups[key]; ok {
			existing.IDs = append(existing.IDs, c.ID)
			existing.Proximo = existing.Proximo || sib.Proximo
			continue
		}
		sib.IDs = []string{c.ID}
		groups[key] = &sib
		order = append(order, key)
	}

	out := make([]Sibling, 0, len(order))
	for _, key := range order {
		out = append(out, *groups[key])
	}
	assignRefs(out)
	sort.Slice(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return out, nil
}

// siblingOf maps one container summary to its target and the key that groups
// containers of the same target together.
func siblingOf(c container.Summary, name string) (string, Sibling) {
	project := c.Labels[composeProjectLabel]
	if project == "" {
		ref := containerRefPrefix + name
		return ref, Sibling{Ref: ref, Name: name}
	}
	workDir := c.Labels[composeWorkDirLabel]
	_, isProximo := c.Labels[proximo.RoleLabel]
	// The Ref set here is provisional — assignRefs appends the working-
	// directory qualifier once it knows whether the name collides. The prefix
	// is final from the start so IsProject answers correctly throughout, and
	// a project whose working_dir label is missing still reads as a project.
	return projectRefPrefix + project + "@" + workDir, Sibling{
		Ref:     projectRefPrefix + project,
		Name:    project,
		WorkDir: workDir,
		Proximo: isProximo,
	}
}

// assignRefs qualifies a project's Ref with its working directory, and only
// when another project shares its name. Unqualified refs are what a user sees
// and types in the common case; the qualifier appears exactly where it is
// load-bearing. A project and a container that share a name never collide —
// their prefixes already differ.
func assignRefs(sibs []Sibling) {
	count := make(map[string]int, len(sibs))
	for _, s := range sibs {
		count[s.Ref]++
	}
	for i := range sibs {
		if sibs[i].IsProject() && count[sibs[i].Ref] > 1 {
			sibs[i].Ref += "@" + sibs[i].WorkDir
		}
	}
}

// ResolveSiblings maps command-line arguments onto targets. An argument is
// either a typed ref as completion offers it, or a bare name — accepted only
// while it identifies exactly one target, because the working-directory
// qualifier is something a user has no way to guess.
//
// Naming a toolbox container is refused rather than reported as unknown: it
// exists, it is simply not this command's business, and the error says which
// command owns it.
func ResolveSiblings(sibs []Sibling, args []string) ([]Sibling, error) {
	out := make([]Sibling, 0, len(args))
	for _, arg := range args {
		sib, err := resolveOne(sibs, arg)
		if err != nil {
			return nil, err
		}
		out = append(out, sib)
	}
	return out, nil
}

func resolveOne(sibs []Sibling, arg string) (Sibling, error) {
	var matches []Sibling
	for _, s := range sibs {
		if s.Ref == arg {
			return s, nil
		}
		if s.Name == arg {
			matches = append(matches, s)
		}
	}

	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		if sessionplan.IsToolboxContainerName(arg) {
			return Sibling{}, fmt.Errorf("%s is a toolbox container: use `toolbox stop %s`", arg, arg)
		}
		return Sibling{}, fmt.Errorf("no such project or container: %s", arg)
	default:
		refs := make([]string, 0, len(matches))
		for _, m := range matches {
			refs = append(refs, m.Ref)
		}
		return Sibling{}, fmt.Errorf("%s is ambiguous, pick one of: %s", arg, strings.Join(refs, ", "))
	}
}

// BulkSiblings splits the targets of a blind sweep (`stop --all`, `prune`)
// from the ones it declines to touch. Only the proximo stack is declined: it
// is host infrastructure whose removal takes every `.test` name down with it,
// and it stays addressable by name — typing it is a choice, sweeping it is
// not. The skipped set is returned so the caller can say so out loud, because
// a target completion offered and the sweep passed over must not just be
// absent from the output.
func BulkSiblings(sibs []Sibling) (targets, skipped []Sibling) {
	for _, s := range sibs {
		if s.Proximo {
			skipped = append(skipped, s)
			continue
		}
		targets = append(targets, s)
	}
	return targets, skipped
}

// siblingStopGrace is the SIGTERM grace (seconds) for a sibling container.
// Deliberately not teardown.DefaultStopGrace: that 2s is measured on the idle
// shell of a toolbox container, which has nothing to flush because its state
// lives on host bind mounts. A sibling is usually a test stack with a database
// in it, and SIGKILL two seconds in is how a data directory gets corrupted.
const siblingStopGrace = 10

// StopSiblings stops every container of every target. A failure on one target
// does not short-circuit the rest — partial cleanup beats fail-fast when the
// caller asked for a sweep — and the errors are joined at the end.
func StopSiblings(ctx context.Context, cli client.APIClient, sibs []Sibling) error {
	var errs []error
	for _, s := range sibs {
		ui.Info("Stopping " + s.Ref + "...")
		if err := stopSibling(ctx, cli, s); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func stopSibling(ctx context.Context, cli client.APIClient, s Sibling) error {
	grace := siblingStopGrace
	var errs []error
	for _, id := range s.IDs {
		_, err := cli.ContainerStop(ctx, id, client.ContainerStopOptions{Timeout: &grace})
		if err != nil && !cerrdefs.IsNotFound(err) {
			errs = append(errs, fmt.Errorf("failed to stop container %s of %s: %w", id, s.Ref, err))
		}
	}
	return errors.Join(errs...)
}

// RemoveSiblings stops each target and removes it: the containers, and for a
// Compose project the networks it created. Volumes are removed only when
// volumes is true — everything else here is free to rebuild, a volume is the
// one place a test stack keeps something you may not want back.
//
// Deliberately not teardown.StopOne: Teardown is the policy for a container
// toolbox owns, where stop+remove is lossless because the state lives on host
// bind mounts. This is a different population with a different policy, and
// sharing the function would collapse the distinction.
func RemoveSiblings(ctx context.Context, cli client.APIClient, sibs []Sibling, volumes bool) error {
	var errs []error
	for _, s := range sibs {
		ui.Info("Removing " + s.Ref + "...")
		if err := stopSibling(ctx, cli, s); err != nil {
			errs = append(errs, err)
		}
		for _, id := range s.IDs {
			// No Force: the stop above already sent SIGTERM with a real
			// grace, and Force is an immediate SIGKILL.
			_, err := cli.ContainerRemove(ctx, id, client.ContainerRemoveOptions{})
			if err != nil && !cerrdefs.IsNotFound(err) {
				errs = append(errs, fmt.Errorf("failed to remove container %s of %s: %w", id, s.Ref, err))
			}
		}
		if !s.IsProject() {
			// A standalone container owns no Compose network or volume, and an
			// unscoped lookup here would reach every network on the host.
			continue
		}
		errs = append(errs, removeProjectNetworks(ctx, cli, s))
		if volumes {
			errs = append(errs, removeProjectVolumes(ctx, cli, s))
		}
	}
	return errors.Join(errs...)
}

// projectFilter scopes a network or volume lookup to one Compose project.
func projectFilter(s Sibling) client.Filters {
	return make(client.Filters).Add("label", composeProjectLabel+"="+s.Name)
}

func removeProjectNetworks(ctx context.Context, cli client.APIClient, s Sibling) error {
	list, err := cli.NetworkList(ctx, client.NetworkListOptions{Filters: projectFilter(s)})
	if err != nil {
		return fmt.Errorf("failed to list networks of %s: %w", s.Ref, err)
	}
	var errs []error
	for _, n := range list.Items {
		_, err := cli.NetworkRemove(ctx, n.ID, client.NetworkRemoveOptions{})
		if err != nil && !cerrdefs.IsNotFound(err) {
			errs = append(errs, fmt.Errorf("failed to remove network %s of %s: %w", n.Name, s.Ref, err))
		}
	}
	return errors.Join(errs...)
}

func removeProjectVolumes(ctx context.Context, cli client.APIClient, s Sibling) error {
	list, err := cli.VolumeList(ctx, client.VolumeListOptions{Filters: projectFilter(s)})
	if err != nil {
		return fmt.Errorf("failed to list volumes of %s: %w", s.Ref, err)
	}
	var errs []error
	for _, v := range list.Items {
		_, err := cli.VolumeRemove(ctx, v.Name, client.VolumeRemoveOptions{})
		if err != nil && !cerrdefs.IsNotFound(err) {
			errs = append(errs, fmt.Errorf("failed to remove volume %s of %s: %w", v.Name, s.Ref, err))
		}
	}
	return errors.Join(errs...)
}
