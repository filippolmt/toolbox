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

	"github.com/filippolmt/toolbox/internal/imageref"
	"github.com/filippolmt/toolbox/internal/ui"
)

// announce writes the sweep's one summary line. A package-level var so a test
// can read what was said: the real writer is the attached tty, which is also
// why there is only ever one line and why a refusal produces none. The async
// writer, not Infof — ui owns why (the shell holds the tty in raw mode by the
// time a removal finishes).
var announce = ui.InfoAsyncf

// Input is everything one sweep needs, resolved by the caller at the
// container edge.
type Input struct {
	// Ref is the resolved base image reference (config `image` override or
	// `registry_mirror` host swap already applied) — the same value
	// imageprefetch.Input.Ref carries, spelled the same way on purpose, since
	// both are populated from the one base ref at the one call site. Only its
	// registry path is compared, which is what keeps the sweep inside its own
	// perimeter; any tag or digest suffix is ignored. Never the `:local`
	// overlay tag: that one is built, not pulled.
	Ref string
	// KeepDigest is the repo digest the current session runs. Excluded by
	// name rather than left to inference: a config pinning `image:` to a
	// digest produces a running image with no tags at all, so the predicate
	// on its own would nominate the very image the shell just started from.
	KeepDigest string
}

// imageStore is the local image store a sweep works over: it enumerates the
// heads and asks for a removal, and the daemon's refusal of the second is the
// arbitration this package leans on. → CONTEXT.md, Declared Docker Surface.
type imageStore interface {
	ImageList(ctx context.Context, opts client.ImageListOptions) (client.ImageListResult, error)
	ImageRemove(ctx context.Context, id string, opts client.ImageRemoveOptions) (client.ImageRemoveResult, error)
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
func Start(ctx context.Context, cli imageStore, in Input) {
	go sweep(ctx, cli, in)
}

// abstains reports whether there is no sweep to run. An empty ref is not a
// wildcard but worse than one: imageref.RepoDigest compares the bare registry
// path, and the empty path matches a malformed `@sha256:…` entry, which would
// nominate an image belonging to a project that is not this one.
func (in Input) abstains() bool { return in.Ref == "" }

// sweep removes every Superseded Image the local store holds for in.Ref. A
// store the daemon will not enumerate costs nothing: the act is opportunistic,
// and the next shell asks again.
//
// No filter is passed, and `dangling=true` in particular is not: it does not
// match these images at all (losing a tag leaves the repo digest behind, and
// an image is dangling only when it has neither), and it would match images
// belonging to every other project on the machine. The default listing is
// heads-only, which is what keeps intermediate layers out of the candidate set.
func sweep(ctx context.Context, cli imageStore, in Input) {
	if in.abstains() {
		return
	}
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
			break
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
		digest := imageref.RepoDigest(in.Ref, img.RepoDigests)
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
	// Reached on the cancellation path too, deliberately: a session that exits
	// mid-sweep has already freed whatever it freed, and the summary is the
	// developer's only sign that gigabytes went away.
	if removed > 0 {
		announce("reclaimed %d superseded runtime image(s)", removed)
	}
}
