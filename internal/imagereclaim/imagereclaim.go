// Package imagereclaim owns Image Reclamation: the opportunistic removal of
// the runtime images this CLI pulled and a later move of `latest` left
// nameless. Glossary: CONTEXT.md's Superseded Image (what counts as a
// candidate) and Image Reclamation (how one is removed).
//
// The contract is that the daemon is the arbiter of use. This package runs no
// container census of its own: it calls ImageRemove with neither `force` nor
// `PruneChildren` and treats the refusal as the answer to the question it did
// not ask. ADR 0007 carries the reasoning and the two consequences.
package imagereclaim

import (
	"context"

	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/build"
	"github.com/filippolmt/toolbox/internal/ui"
)

// announce writes the sweep's one summary line. A package-level var so a test
// can read what was said: the real writer is the attached tty, which is also
// why there is only ever one line and why a refusal produces none.
var announce = ui.Infof

// Input is everything one sweep needs, resolved by the caller at the
// container edge.
type Input struct {
	// Repo is the toolbox image reference the config resolves to. Any tag or
	// digest suffix is ignored — only the registry path is compared, which is
	// what keeps the sweep inside its own perimeter.
	Repo string
	// KeepDigest is the repo digest the current session runs. Excluded by
	// name rather than left to inference: a config pinning `image:` to a
	// digest produces a running image with no tags at all, so the predicate
	// on its own would nominate the very image the shell just started from.
	KeepDigest string
}

// Start runs one Image Reclamation sweep beside the attached session and
// returns immediately. Background because a store holding a generation per
// merge would otherwise delay the prompt by however long the daemon takes to
// unlink their layers, and the developer is waiting on a shell rather than on
// disk space.
//
// The caller cancels it with the session, and that is safe rather than merely
// tolerated: the act is idempotent, so a candidate this sweep did not reach is
// still a candidate at the next shell.
func Start(ctx context.Context, cli client.APIClient, in Input) {
	go sweep(ctx, cli, in)
}

// sweep removes every Superseded Image the local store holds for in.Repo. A
// store the daemon will not enumerate costs nothing: the act is opportunistic,
// and the next shell asks again.
//
// No filter is passed, and `dangling=true` in particular is not: it does not
// match these images at all (losing a tag leaves the repo digest behind, and
// an image is dangling only when it has neither), and it would match images
// belonging to every other project on the machine. The default listing is
// heads-only, which is what keeps intermediate layers out of the candidate set.
func sweep(ctx context.Context, cli client.APIClient, in Input) {
	res, err := cli.ImageList(ctx, client.ImageListOptions{})
	if err != nil {
		return
	}
	removed := 0
	for _, img := range res.Items {
		// Cancelled with the session, and asked once per candidate rather
		// than once: every removal left in the list would otherwise cost a
		// doomed daemon round-trip whose error is indistinguishable from the
		// refusal that means "some container still needs this".
		if ctx.Err() != nil {
			return
		}
		// The three clauses of the predicate, in the order they cost least:
		// no digest for this repo means this project never pulled the image
		// (the perimeter); the session's own digest is excluded by name; and a
		// surviving tag means `latest` has not moved past it yet.
		//
		// The digest guard is inert when KeepDigest is empty, which is what a
		// local `toolbox build` looks like — the image carries no repo digest,
		// so nothing was resolvable to keep. Deliberately not a reason to
		// abstain: a locally built image is not a candidate either (no repo
		// digest, first clause), and this session's container already exists
		// and references whatever it runs, so the daemon refuses it for us.
		digest := build.RepoDigest(in.Repo, img.RepoDigests)
		if digest == "" || digest == in.KeepDigest || len(img.RepoTags) > 0 {
			continue
		}
		// The daemon is the arbiter of use: an error here is its refusal —
		// some container, running or merely stopped, still references the
		// image — and a refusal is an answer rather than a failure, so the
		// sweep moves on to the next candidate without a word. ADR 0007.
		if _, err := cli.ImageRemove(ctx, img.ID, client.ImageRemoveOptions{}); err != nil {
			continue
		}
		removed++
	}
	if removed > 0 {
		// CR-terminated on purpose: by the time a removal finishes, the
		// attached shell has put the tty in raw mode (term.MakeRaw clears
		// ONLCR), where the bare LF ui writes drops a line without returning
		// the carriage and staircases everything after it. Harmless on a
		// cooked tty, which is the only other case.
		announce("reclaimed %d superseded runtime image(s)\r", removed)
	}
}
