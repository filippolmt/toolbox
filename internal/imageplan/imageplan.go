// Package imageplan owns the "is the image ready for ContainerCreate?"
// decision tree. Two phases, two seams:
//
//   - Sync: the registry sync, best-effort. Policy in — the Pull policy the
//     Image carries — plus a Reason, the caller's answer to *why this sync is
//     running*; Outcome out. The reason is what decides whether a developer is
//     asked, and it is the only thing about the calling branch this tree
//     learns. There is no second entry point for "the same sync but silent":
//     that was two functions differing by a prompt and a cache check, and a
//     caller choosing between them by name could pick the wrong one.
//   - Ensure: hard guarantee called before ContainerCreate. If the image
//     is already in the local store, done. Otherwise the registry pull
//     already had its chance and we fail fatally.
//
// The image ref defaults to the canonical registry tag but can be relocated
// opt-in (config Image / RegistryMirror; imageref.ResolveImage owns the
// precedence). Ensure never builds — `toolbox build` is the explicit
// user-driven path for a local rebuild. The Pull policy carried on the
// Image steers Sync: "auto" (default) probes or reads the cache depending on
// the reason, "always" forces a pull, "never" skips the registry entirely
// (Ensure still hard-requires the image locally).
package imageplan

import (
	"context"
	"fmt"
	"time"

	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/imageprefetch"
	"github.com/filippolmt/toolbox/internal/imageref"
	"github.com/filippolmt/toolbox/internal/sessionplan"
	"github.com/filippolmt/toolbox/internal/ui"
)

// imageSource is where the image comes from, at the width this package's tree
// actually reaches: the local store it checks for presence, plus the registry
// its two callees ask and pull from. The union of its own call and theirs, and
// no wider — which is what makes an unstubbed endpoint in a test a panic
// naming the method rather than a silent zero value.
// → CONTEXT.md, Declared Docker Surface.
type imageSource interface {
	ImageInspect(ctx context.Context, ref string, opts ...client.ImageInspectOption) (client.ImageInspectResult, error)
	DistributionInspect(ctx context.Context, ref string, opts client.DistributionInspectOptions) (client.DistributionInspectResult, error)
	ImagePull(ctx context.Context, ref string, opts client.ImagePullOptions) (client.ImagePullResponse, error)
}

// promptWindow is how long the start-up prompt waits for an answer. Long
// enough to read the question and reach for a key, short enough that a
// developer who walked away has not lost their morning to it — and visible
// while it runs, because silence of this length is indistinguishable from a
// hang.
const promptWindow = 5 * time.Second

// prompt is the shape of the question: what was answered, and whether the
// developer interrupted the command instead of answering it. The elapsed
// answer rides along because it is per-question here — see Reason.offer.
type prompt func(question string, window time.Duration, elapsed ui.Elapsed) (yes, interrupted bool)

// askable and confirm are the prompt seams: whether there is a developer to
// ask, and what they answered. Package-level vars for the reason Ensure is
// one — a test of the decision tree must not depend on a terminal.
var (
	askable        = ui.Askable
	confirm prompt = ui.ConfirmCountdown
)

// Reason is why a Sync is running — the one thing about the calling branch
// this tree is told, and the whole of it. It decides two things that used to
// be decided by which of two functions the caller named: whether a developer
// is asked at all, and what a yes would cost besides the wait.
type Reason int

// The zero value is ReasonCreate, and that is load-bearing in two directions:
// a reason this package is not given must not silently skip the question the
// way a reload does, and it must not be handed the wording, or the unanswered
// default, of the one that discards a container.
const (
	// ReasonCreate: a shell start with no container yet, so a yes buys the
	// newer image and costs only the download. An elapsed window may answer
	// it, because what it starts is the pull that would otherwise have been
	// unconditional.
	ReasonCreate Reason = iota
	// ReasonStart: a shell start on a stopped container, which a yes replaces
	// — discarding whatever was written inside it outside the bind mounts. No
	// clock may choose that, so the window answers no.
	ReasonStart
	// ReasonReload: a session reload, run before it destroys anything. Asks
	// nothing and confirms nothing — its premise is that the move onto the
	// newer image was asked for, and the same path is what an unattended
	// trigger walks. It is also the one reason that trusts the pull cache: a
	// reload adopts what the store holds, and the background prefetch is what
	// advanced it.
	ReasonReload
)

// offer is one reason's whole side of the conversation: how the question is
// put, what an unanswered window answers, and what a decline says out loud.
// The three travel together because they are one editorial decision — a
// question worded around a container that a clock could accept, or a
// postponement that named the wrong thing, would each be a bug on their own.
type offer struct {
	question  string
	elapsed   ui.Elapsed
	postponed string
}

// offer is what a yes costs besides the wait, made concrete: that decides all
// three fields, and it hangs off the reason because the reason is what implies
// it. The caller knows which branch it is on; that a stopped container makes a
// yes destructive is this package's own conclusion, and one it must not be
// able to be told wrongly. → CONTEXT.md, Prompt Stake.
//
// Each question is kept short enough to share one terminal line with the
// countdown: the prompt owns exactly one line for its whole life — it redraws
// with a carriage return and erases with one clear — so a question that
// wrapped would leave half of itself on screen.
//
// Only ReasonStart discards a container, so only it answers no to an
// unanswered window. Every other reason is worded as the download, the form
// that spends nothing but time — including the zero value, which must never be
// handed the wording, or the default, of the one that destroys something.
func (r Reason) offer() offer {
	if r == ReasonStart {
		return offer{
			question:  "A newer runtime image is available. Recreate this container on it?",
			elapsed:   ui.ElapsedNo,
			postponed: "Starting the container as it is — the newer image downloads in the background.",
		}
	}
	return offer{
		question:  "A newer runtime image is available. Download it now?",
		elapsed:   ui.ElapsedYes,
		postponed: "Starting on the image already in the store — the newer one downloads in the background.",
	}
}

// Outcome is what the start-up refresh settled: one value, because the
// settlements are mutually exclusive and the independent bools that used to
// spell them were not. Those admitted far more combinations than there were
// legal cases, and which combinations were legal was carried in prose above
// the fields; here the illegal ones cannot be written.
//
// Currency is derived rather than stored, for the same reason: a store the
// registry sync proved current is exactly OutcomeCurrent or OutcomeAccepted,
// and a decline or an interrupt downloads nothing. See Synced.
type Outcome int

// The zero value is OutcomeUnsettled: every path that establishes nothing —
// the `never` policy, a probe that did not answer, no tty to ask on — returns
// it, so a caller that returns without choosing a settlement claims nothing
// rather than claiming currency.
const (
	// OutcomeUnsettled: nothing was established. Neither the store's currency
	// nor a developer's answer is known, and nothing may be read into it.
	OutcomeUnsettled Outcome = iota
	// OutcomeCurrent: the local store is current with the registry as of a
	// moment ago — a successful pull, or a probe that proved the store was
	// already current, with nobody asked. The background prefetch takes it as
	// "this shell start already took its turn at the registry" and publishes
	// the banner from the store instead of asking the same question seconds
	// later.
	OutcomeCurrent
	// OutcomeDeclined: the developer postponed the download. A "no" is a
	// postponement rather than a refusal, so the session arms the idle reload
	// that will adopt the image the background prefetch is fetching anyway.
	OutcomeDeclined
	// OutcomeInterrupted: a ctrl+c at the prompt, which is neither an answer
	// nor a postponement — the developer stopped the command. Nothing is
	// stamped and nothing is announced — there is no session left to postpone
	// anything for — and the caller abandons the start.
	OutcomeInterrupted
	// OutcomeAccepted: the developer answered the question with a yes *and*
	// the download it asked for landed. Not the answer alone: a yes the
	// registry could not honour has bought nothing, and the caller reads this
	// on the branch where acting on it destroys a container — which would
	// then be spent for an image that never arrived. A yes whose pull failed
	// settles as OutcomeUnsettled, which is what "the developer is where they
	// already were" means.
	OutcomeAccepted
)

// Synced reports that the local store is current with the registry as of a
// moment ago. It is a question about the store, not about the conversation,
// so both the case nobody was asked about and the honoured yes answer it —
// and only those two, since the other three downloaded nothing.
func (o Outcome) Synced() bool { return o == OutcomeCurrent || o == OutcomeAccepted }

// String names the settlement, so a failure that prints an Outcome reads as
// the case it is rather than as the integer behind it — the one thing the
// struct it replaced gave for free.
func (o Outcome) String() string {
	switch o {
	case OutcomeUnsettled:
		return "unsettled"
	case OutcomeCurrent:
		return "current"
	case OutcomeDeclined:
		return "declined"
	case OutcomeInterrupted:
		return "interrupted"
	case OutcomeAccepted:
		return "accepted"
	}
	// Every settlement is named above, so this is a constant added without a
	// case. It must not fall back to the zero value's name: "unsettled" is the
	// claim that nothing was established, and printing it for a settlement
	// that *was* established is the one lie this type exists to prevent.
	return fmt.Sprintf("Outcome(%d)", int(o))
}

// settleSync maps a best-effort sync's success onto the settlement it
// establishes: a pull or probe that landed proves currency, one that did not
// proves nothing. Every call site that reports a sync nobody was asked about
// shares it, so that "failed" cannot be spelled as anything but the zero
// value.
//
// Named for the settling rather than for currency: Synced() is true of
// OutcomeAccepted too, so a name built on it would read as the wider question
// this answers only half of.
func settleSync(synced bool) Outcome {
	if synced {
		return OutcomeCurrent
	}
	return OutcomeUnsettled
}

// Sync best-effort syncs the image against its registry and, in the one case
// where the answer is not already settled, asks. Policy in, reason in,
// Outcome out. The tree is ADR 0005:
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
// The reason adds exactly one case to that tree — the reload, which skips the
// probe and the question both and reads the TTL cache instead. Past that it
// only supplies the offer: what a yes costs besides the wait, which words the
// question and points the unanswered window. Every settled case above
// stays settled under every reason — in particular `always`, which has said
// yes to downloads and nothing at all about containers, so it pulls without
// asking and reports no acceptance for a caller to act on.
//
// Best-effort throughout: every failure path leaves the caller with the local
// image and Ensure with the last word.
func Sync(ctx context.Context, cli imageSource, image sessionplan.Image, stateDir string, reason Reason) Outcome {
	switch image.PullPolicy {
	case config.PullNever:
		return OutcomeUnsettled
	case config.PullAlways:
		return settleSync(forcePull(ctx, cli, image.Ref, stateDir))
	}

	// The reload's whole branch: it neither probes nor asks, because its
	// premise is that the move onto the newer image was already asked for, and
	// it is the only reason that trusts the TTL cache — a shell start declines
	// that cache deliberately, since a warm one there is what let a released
	// image go unoffered for a whole window. Spelled against the constant
	// rather than behind a predicate: two properties ride on this branch, and
	// a name for either one alone would misdescribe the other.
	if reason == ReasonReload {
		return settleSync(refreshIfStale(ctx, cli, image.Ref, stateDir))
	}

	// Presence, not currency: an image the store does not hold at all leaves
	// nothing to ask about and nothing to start.
	if _, present := imageref.LocalRepoDigest(ctx, cli, image.Ref); !present {
		return settleSync(forcePull(ctx, cli, image.Ref, stateDir))
	}

	// Before the probe, not after: knowing the answer is a registry round-trip
	// too, and off a tty there is nothing that could be done with it.
	if !askable() {
		return OutcomeUnsettled
	}

	store := imageprefetch.AheadOfStore(ctx, cli, image.Ref, stateDir)
	switch {
	case !store.Known:
		// A probe that did not answer, or a locally built image: nothing was
		// established, so nothing is claimed and nobody is asked.
		return OutcomeUnsettled
	case !store.Ahead:
		return settleSync(store.Probed)
	}

	ask := reason.offer()
	yes, interrupted := confirm(ask.question, promptWindow, ask.elapsed)
	switch {
	case interrupted:
		// A ctrl+c is not an answer to this question, it is the end of the
		// command asking it. Saying anything here would announce a session
		// that is being torn down, and stamping a postponement would arm an
		// idle reload for a session that will never idle.
		return OutcomeInterrupted
	case !yes:
		// Nothing new downloads here: the background prefetch already runs an
		// immediate pass when the session opens, and a second fetch of the
		// same ref at the same moment is what that would be. Said out loud,
		// because the question has just erased itself and a postponement the
		// developer cannot see reads like one that was ignored.
		ui.Info(ask.postponed)
		return OutcomeDeclined
	}
	// The pull that landed is what makes the answer honourable; one that
	// failed leaves the developer where they already were rather than
	// spending a container on nothing, so it settles as nothing at all.
	if forcePull(ctx, cli, image.Ref, stateDir) {
		return OutcomeAccepted
	}
	return OutcomeUnsettled
}

// Ensure guarantees the image referenced by `image.Ref` exists in the
// local Docker store.
func Ensure(ctx context.Context, cli imageSource, image sessionplan.Image) error {
	if _, err := cli.ImageInspect(ctx, image.Ref); err == nil {
		return nil
	}
	return fmt.Errorf("image %q not available locally and pull failed — check registry access (run `toolbox build` to build locally)", image.Ref)
}
