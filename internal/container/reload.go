package container

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/build"
	"github.com/filippolmt/toolbox/internal/imageplan"
	"github.com/filippolmt/toolbox/internal/imageprefetch"
	"github.com/filippolmt/toolbox/internal/reload"
	"github.com/filippolmt/toolbox/internal/sessionplan"
	"github.com/filippolmt/toolbox/internal/ui"
	"github.com/filippolmt/toolbox/internal/version"
)

// takeReloadRequest reads the marker the exiting shell may have written and
// composes the handover for the next host process. Called exactly where
// execShell returns, which is where the teardown decision is already made.
//
// The "before" half of the summary is read off the plan this process ran with,
// not recomputed: the new binary needs to state what was left, and by the time
// it runs, the container that could have been asked is gone.
func takeReloadRequest(plan *sessionplan.SessionPlan) *reload.From {
	marker := plan.ReloadMarkerPath()
	if marker == "" {
		return nil
	}
	cwd, requested := reload.TakeMarker(marker)
	if !requested {
		return nil
	}
	return &reload.From{
		Container:   plan.ContainerName,
		Cwd:         cwd,
		ImageDigest: sessionplan.EnvValue(plan.Env, sessionplan.ImageDigestEnv),
		CLIVersion:  sessionplan.EnvValue(plan.Env, sessionplan.CLIVersionEnv),
		// The launch mode, not the developer's intent: only a session that
		// auto-launched an agent gets one back, so a plain shell reloads into a
		// plain shell. The re-entry argv is cmd's half of the payload — it is
		// the only field this layer has no business constructing.
		Resume: plan.LaunchesAgent(),
	}
}

// replaceForReload runs the destructive half of a session reload, in the one
// order that makes a failed reload harmless: refresh, prove the image is
// present, only then destroy. A no-op for a session that is not a reload, so
// the caller can name the act unconditionally rather than hide it behind a
// condition.
//
// imageplan.Ensure is a local-presence check that never pulls, so the gate
// needs no new code and gives the contract outright — a reload that finds no
// usable image is not a failed reload, it is a no-op that leaves the session
// alive. Everything after the teardown can still fail (a port conflict, a
// `:local` overlay that will not build), and those failures must print the
// re-entry command, because the shell that would have printed it is gone.
func replaceForReload(ctx context.Context, cli client.APIClient, plan *sessionplan.SessionPlan) error {
	from := plan.ReloadFrom
	if from == nil {
		return nil
	}

	imageplan.Sync(ctx, cli, plan.Image, plan.StateDir, imageplan.ReasonReload)
	if err := imageplan.Ensure(ctx, cli, plan.Image); err != nil {
		return fmt.Errorf("reload aborted, session left as it was: %w", err)
	}

	// Enumerated before the teardown, printed after it: the container is still
	// alive here, and a list printed by a reload that then failed would name
	// casualties that never died.
	casualties := reloadCasualties(ctx, cli, from.Container, plan.Cmd)

	if err := removeAndWait(ctx, cli, from.Container, "reload"); err != nil {
		return err
	}

	// The container is new; the banner's cache still describes the old one.
	imageprefetch.ClearResult(plan.StateDir)

	// A store that cannot be read and one carrying no digest (a local build)
	// collapse to the same "" here on purpose: the digest is summary text, not
	// a gate, and printReloadSummary renders "unknown" for either.
	after, _ := build.LocalRepoDigest(ctx, cli, plan.Image.Ref)
	printReloadSummary(from, after, casualties)
	return nil
}

// removeAndWait destroys the container a create is about to replace and does
// not return until the name is free again. act names the caller in the error,
// which is the only thing that differs between them: a session reload, and a
// start-up refresh whose yes was a yes to rebuilding a stopped container.
//
// Deliberately not teardown.OnShellExit, and the reason is structural rather
// than a preference: that policy declines while a sibling shell is attached,
// and the container name is deterministic per workspace — so a spared old
// container would block the create that follows, turning a considerate refusal
// into a name collision. Another attached terminal dies with the reload; that
// is the rule, and reloadCasualties is what makes the loss visible.
//
// Force-remove rather than kill-and-let-AutoRemove-reap: the AutoRemove path
// returns as soon as the SIGKILL lands, which races the new container's name.
// The wait is subscribed before the removal because the daemon's own worker
// can finish in between, and a wait started after that never fires.
func removeAndWait(ctx context.Context, cli client.APIClient, name, act string) error {
	waitRes := cli.ContainerWait(ctx, name, client.ContainerWaitOptions{Condition: container.WaitConditionRemoved})

	_, err := cli.ContainerRemove(ctx, name, client.ContainerRemoveOptions{Force: true})
	switch {
	case cerrdefs.IsNotFound(err):
		// Already gone — an external `toolbox stop`, or a daemon that reaped it
		// while this process was re-execing. Nothing to wait for.
		return nil
	case err != nil && !cerrdefs.IsConflict(err):
		// Conflict is the daemon's "removal already in progress": redundant,
		// not an error, and the wait below is exactly how we find out it ended.
		return fmt.Errorf("%s: failed to remove container %s: %w", act, name, err)
	}

	select {
	case <-waitRes.Result:
		return nil
	case werr := <-waitRes.Error:
		if werr != nil && !cerrdefs.IsNotFound(werr) {
			return fmt.Errorf("%s: waiting for container %s to be removed: %w", act, name, werr)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// reloadCasualties lists the container processes the teardown is about to end,
// baseline removed. The reload gates on nothing here: a dev server, a watcher
// or a `tail -f` is the normal state of a working shell, so a prompt would
// fire on nearly every reload. What earns the look is the one thing the
// developer has forgotten — a Ctrl+Z-suspended agent, a detached job, invisible
// at the prompt where `toolbox-reload` was typed.
//
// ContainerTop rather than a process list from inside the container: under
// peer messaging the anchor's PID namespace is the whole process table, so an
// in-container `ps` would list every sibling session. ContainerTop stays
// cgroup-scoped even then. Best-effort — it answers 409 on a container that is
// no longer running, and a missing list costs evidence, never the reload.
func reloadCasualties(ctx context.Context, cli client.APIClient, name string, sessionCmd []string) []string {
	top, err := cli.ContainerTop(ctx, name, client.ContainerTopOptions{})
	if err != nil {
		return nil
	}
	return filterCasualties(top.Titles, top.Processes, sessionCmd)
}

// reloadBaseline is the small known set of processes a healthy idle container
// always carries: tini as PID 1, one socat per published port under -B, the
// proximo hosts watcher and the `docker events` child it reconnects, and the
// `sleep infinity` a bare image CMD would leave.
//
// Keyed by baselineKey, which is why the watcher's child can sit in the same
// map as everything else despite needing two words to name.
//
// It does not need to be right, only honest. Because the list is informational
// a stale entry costs one noisy line and never a wrong decision — the same
// drift a gate could not have tolerated.
var reloadBaseline = map[string]bool{
	"tini":          true,
	"socat":         true,
	"proximo-hosts": true,
	"sleep":         true,
	"docker events": true,
}

// scriptInterpreters are the shells a `#!` line puts in front of a script, so
// that ContainerTop reports the interpreter where the developer would name the
// script. Keyed by basename, the same reduction baselineKey applies.
var scriptInterpreters = map[string]bool{"sh": true, "bash": true, "zsh": true}

// baselineKey reduces a command line to the shape reloadBaseline is keyed on:
// the first field's basename, plus the subcommand when the binary is one whose
// name alone says nothing (`docker` runs the watcher's event stream, and it
// also runs whatever the developer typed).
//
// A shebang script is named by its interpreter in the process table
// (`/bin/sh /usr/local/bin/proximo-hosts`), so the key comes off the script
// instead — unless the next field is a flag, where the interpreter is running
// something of its own (`sh -c ...`) and is itself the honest name.
func baselineKey(fields []string) string {
	i := 0
	if scriptInterpreters[filepath.Base(fields[0])] && len(fields) > 1 && !strings.HasPrefix(fields[1], "-") {
		i = 1
	}
	base := filepath.Base(fields[i])
	if base == "docker" && len(fields) > i+1 {
		return base + " " + fields[i+1]
	}
	return base
}

// filterCasualties reduces a ContainerTop result to the command lines worth
// showing. Pure, so the deny-list is testable without a daemon.
//
// The session's own shell command is dropped exactly once: the container's
// idle main process and a sibling attached pane run the identical command, and
// the sibling is the loud loss the developer should see. Dropping every match
// would hide it; dropping none would report the idle shell on every reload.
func filterCasualties(titles []string, processes [][]string, sessionCmd []string) []string {
	col := commandColumn(titles)
	if col < 0 {
		return nil
	}
	mainShell := strings.Join(sessionCmd, " ")
	mainShellSeen := false

	// A tab per pane and a watcher per project make identical command lines
	// the common case, and the same line eight times says nothing eight times.
	// Counted on the cut line rather than the full one, because the cut line
	// is what the developer reads: two watchdogs whose argv diverges past the
	// cut would otherwise print as two identical lines carrying no count.
	counts := map[string]int{}
	var out []string
	for _, p := range processes {
		if col >= len(p) {
			continue
		}
		cmd := strings.TrimSpace(p[col])
		if cmd == "" {
			continue
		}
		if cmd == mainShell && !mainShellSeen {
			mainShellSeen = true
			continue
		}
		if reloadBaseline[baselineKey(strings.Fields(cmd))] {
			continue
		}
		line := cutTo(cmd, casualtyLineMax)
		if counts[line] == 0 {
			out = append(out, line)
		}
		counts[line]++
	}
	sort.Strings(out)
	return countCasualties(out, counts)
}

// countCasualties appends each line's count to it, in place. The suffix is
// part of the line, so it comes out of the same budget: a cap the count is
// then appended past would not be a cap.
func countCasualties(lines []string, counts map[string]int) []string {
	for i, line := range lines {
		n := counts[line]
		if n == 1 {
			continue
		}
		suffix := fmt.Sprintf(" (×%d)", n)
		lines[i] = cutTo(line, casualtyLineMax-utf8.RuneCountInString(suffix)) + suffix
	}
	return lines
}

// casualtyLineMax is where a rendered casualty line ends, count suffix
// included. The list is evidence a developer scans, not a process dump: a
// watchdog started with `node -e` carries kilobytes of argv, and two of them
// would be the entire summary.
const casualtyLineMax = 120

// cutTo shortens s to max runes, ellipsis included, and counts runes rather
// than bytes so a cut never lands inside a multi-byte character.
func cutTo(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

// commandColumn locates the CMD column in a ContainerTop result. The daemon's
// default `ps -ef` yields UID PID PPID C STIME TTY TIME CMD, but the title set
// is the daemon's to choose, so it is read rather than assumed; an unrecognised
// header yields no list rather than a column of timestamps.
func commandColumn(titles []string) int {
	for i, t := range titles {
		switch strings.ToUpper(strings.TrimSpace(t)) {
		case "CMD", "COMMAND":
			return i
		}
	}
	return -1
}

// printReloadSummary is the only evidence the reload did anything: the command
// always reloads, including when nothing is newer, so a run that changed
// nothing and a run that failed silently would otherwise look identical.
// Printed on host stdout before the attach, never preceded by a screen clear.
func printReloadSummary(from *reload.From, imageDigest string, casualties []string) {
	ui.Infof("Reload: image %s", transition(from.ImageDigest, imageDigest))
	ui.Infof("Reload: CLI %s", transition(from.CLIVersion, version.Version))
	if len(casualties) == 0 {
		// An empty list prints nothing: the two lines above already carry the
		// proof that the reload happened.
		return
	}
	ui.Info("Reload: ended with the old container:")
	for _, c := range casualties {
		ui.Info("  " + c)
	}
}

// transition renders one before/after pair, saying `unchanged` outright when
// the two match — the reload always fires, so "same digest" is a result, not
// an absence of one.
func transition(before, after string) string {
	switch {
	case before == "" && after == "":
		return "unknown"
	case before == "":
		return after
	case after == "" || before == after:
		return before + " (unchanged)"
	default:
		return before + " → " + after
	}
}
