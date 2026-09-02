// Package imageplan owns the "is the image ready for ContainerCreate?"
// decision tree. Two phases, three seams:
//
//   - RefreshAtStart: the registry sync a shell start runs, which on the one
//     case that is not already settled — an `auto` policy, the image in the
//     store, the registry ahead of it, a tty to ask on — *asks*, because that
//     is the case where the cost lands on a developer who has an opinion
//     about it. Reports what it established, and whether a "no" postponed it.
//   - Refresh: the same sync with nothing to ask, run by a session reload
//     before it destroys anything. A reload's premise is that the move onto
//     the newer image was asked for, and its own rule is that it gates on
//     nothing and confirms nothing.
//   - Ensure: hard guarantee called before ContainerCreate. If the image
//     is already in the local store, done. Otherwise the registry pull
//     already had its chance and we fail fatally.
//
// The image ref defaults to the canonical registry tag but can be relocated
// opt-in (config Image / RegistryMirror; build.ResolveImage owns the
// precedence). Ensure never builds — `toolbox build` is the explicit
// user-driven path for a local rebuild. The Pull policy carried on the
// Image steers Refresh: "auto" (default) is cache-aware, "always" forces a
// pull, "never" skips the registry entirely (Ensure still hard-requires the
// image locally).
package imageplan

import (
	"context"
	"fmt"
	"time"

	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/build"
	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/imageprefetch"
	"github.com/filippolmt/toolbox/internal/imagepull"
	"github.com/filippolmt/toolbox/internal/sessionplan"
	"github.com/filippolmt/toolbox/internal/ui"
)

// Refresh best-effort syncs the image against its registry without asking
// anything, steered by the Image's pull policy: "never" skips the registry
// round-trip entirely (the local copy is authoritative — Ensure still guards
// presence), "always" forces a pull bypassing the TTL cache, "auto"/"" uses
// the cache-aware default. Errors are swallowed by imagepull (logged as a
// warning at most); the caller's existing local copy is the fallback.
//
// This is the session reload's form. A shell start calls RefreshAtStart
// instead: the TTL cache is the very unevenness that decision removes, and a
// reload keeps it because a reload adopts what the store holds and the
// background prefetch is what advanced it.
//
// Reports whether the local store was actually synced against the registry
// here and now.
func Refresh(ctx context.Context, cli client.APIClient, image sessionplan.Image) bool {
	switch image.PullPolicy {
	case config.PullNever:
		return false
	case config.PullAlways:
		return imagepull.ForcePull(ctx, cli, image.Ref)
	default: // config.PullAuto and the unset zero value
		return imagepull.RefreshIfStale(ctx, cli, image.Ref)
	}
}

// promptWindow is how long the start-up prompt waits for an answer. Long
// enough to read the question and reach for a key, short enough that a
// developer who walked away has not lost their morning to it — and visible
// while it runs, because silence of this length is indistinguishable from a
// hang.
const promptWindow = 5 * time.Second

// prompt is the shape of the question: what was answered, and whether the
// developer interrupted the command instead of answering it.
type prompt func(question string, window time.Duration) (yes, interrupted bool)

// askable and confirm are the prompt seams: whether there is a developer to
// ask, and what they answered. Package-level vars for the reason Ensure is
// one — a test of the decision tree must not depend on a terminal.
var (
	askable        = ui.Askable
	confirm prompt = ui.ConfirmCountdown
)

// Outcome is what the start-up refresh established, and each field is read by
// a different consumer.
type Outcome struct {
	// Synced records that the local store is current with the registry as of
	// a moment ago — a successful pull, or a probe that proved the store was
	// already current. The background prefetch takes it as "this shell start
	// already took its turn at the registry" and publishes the banner from
	// the store instead of asking the same question seconds later.
	Synced bool
	// Declined records that the developer postponed the download. A "no" is a
	// postponement rather than a refusal, so the session arms the idle reload
	// that will adopt the image the background prefetch is fetching anyway.
	Declined bool
	// Interrupted records a ctrl+c at the prompt, which is neither an answer
	// nor a postponement: the developer stopped the command. Nothing is
	// stamped and nothing is announced — there is no session left to postpone
	// anything for — and the caller abandons the start.
	Interrupted bool
}

// RefreshAtStart runs the shell-start refresh and, in the one case where the
// answer is not already settled, asks. The tree is ADR 0005:
//
//   - `never` neither probes nor prompts — not probing is that policy's whole
//     promise, and a probe is a registry round-trip.
//   - `always` pulls without asking: a policy that has already said yes on
//     every shell cannot coherently be asked again.
//   - an image missing from the store is pulled without asking, because there
//     is no session to start otherwise.
//   - without a tty nothing is asked and nothing is probed, because the
//     default already inverts to start-now-fetch-behind: the interactive
//     default is justified by the work that follows the wait, and a script has
//     no work that follows, so the same wait is pure latency multiplied by
//     every invocation in a pipeline.
//   - a store a *live* probe proves current needs nothing and says so, which
//     is worth as much to the prefetch as a pull would have been. The same
//     answer read from the shared cache claims nothing: it was true when that
//     probe ran, and re-stating it here would let the poller's clock be
//     re-stamped from a cached digest on every shell start.
//   - what is left — a registry ahead of the store — is the prompt.
//
// Best-effort throughout, like Refresh: every failure path leaves the caller
// with the local image and Ensure with the last word.
func RefreshAtStart(ctx context.Context, cli client.APIClient, image sessionplan.Image, stateDir string) Outcome {
	switch image.PullPolicy {
	case config.PullNever:
		return Outcome{}
	case config.PullAlways:
		return Outcome{Synced: imagepull.ForcePull(ctx, cli, image.Ref)}
	}

	// Presence, not currency: an image the store does not hold at all leaves
	// nothing to ask about and nothing to start.
	if _, present := build.LocalRepoDigest(ctx, cli, image.Ref); !present {
		return Outcome{Synced: imagepull.ForcePull(ctx, cli, image.Ref)}
	}

	// Before the probe, not after: knowing the answer is a registry round-trip
	// too, and off a tty there is nothing that could be done with it.
	if !askable() {
		return Outcome{}
	}

	store := imageprefetch.AheadOfStore(ctx, cli, image.Ref, stateDir)
	switch {
	case !store.Known:
		// A probe that did not answer, or a locally built image: nothing was
		// established, so nothing is claimed and nobody is asked.
		return Outcome{}
	case !store.Ahead:
		return Outcome{Synced: store.Probed}
	}

	yes, interrupted := confirm("A newer runtime image is available. Download it now?", promptWindow)
	switch {
	case interrupted:
		// A ctrl+c is not an answer to this question, it is the end of the
		// command asking it. Saying anything here would announce a session
		// that is being torn down, and stamping a postponement would arm an
		// idle reload for a session that will never idle.
		return Outcome{Interrupted: true}
	case !yes:
		// Nothing new downloads here: the background prefetch already runs an
		// immediate pass when the session opens, and a second fetch of the
		// same ref at the same moment is what that would be. Said out loud,
		// because the question has just erased itself and a postponement the
		// developer cannot see reads like one that was ignored.
		ui.Info("Starting on the image already in the store — the newer one downloads in the background.")
		return Outcome{Declined: true}
	}
	return Outcome{Synced: imagepull.ForcePull(ctx, cli, image.Ref)}
}

// Ensure guarantees the image referenced by `image.Ref` exists in the
// local Docker store. Exposed as a package-level variable so tests can
// substitute without spinning up a real build context.
var Ensure = func(ctx context.Context, cli client.APIClient, image sessionplan.Image) error {
	if _, err := cli.ImageInspect(ctx, image.Ref); err == nil {
		return nil
	}
	return fmt.Errorf("image %q not available locally and pull failed — check registry access (run `toolbox build` to build locally)", image.Ref)
}
