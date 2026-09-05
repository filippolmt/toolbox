package imageplan

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/dockertest"
	"github.com/filippolmt/toolbox/internal/fsx"
	"github.com/filippolmt/toolbox/internal/sessionplan"
	"github.com/filippolmt/toolbox/internal/ui"
)

// sourceStub is the data this package's tree is driven with. It implements no
// Docker method of its own: docker() wires the shared fake to these fields,
// stubbing the three endpoints the tree reaches and no others. What the mock
// used to guard by hand — that Ensure never creates a container — the narrow
// imageSource now guards at compile time, since ContainerCreate is not a
// method it has.
type sourceStub struct {
	inspectFn  func(ctx context.Context, id string) (client.ImageInspectResult, error)
	pullFn     func() (io.ReadCloser, error)
	distDigest string
	distErr    error

	fake *dockertest.Fake
}

// docker returns the daemon the tree sees, built once and reused so the call
// counters survive the run and the assertions that read them. Called from the
// test goroutine only. Every stub re-reads its field on each call, so a test
// may still swap one after construction.
func (m *sourceStub) docker() *dockertest.Fake {
	if m.fake != nil {
		return m.fake
	}
	m.fake = &dockertest.Fake{
		ImageInspectFn: func(ctx context.Context, id string) (client.ImageInspectResult, error) {
			if m.inspectFn != nil {
				return m.inspectFn(ctx, id)
			}
			return client.ImageInspectResult{}, errors.New("ImageInspect not mocked")
		},
		// DistributionInspect answers the remote-digest probe, and the fake
		// counts the calls: a question answered from the prefetch's warm cache
		// must reach no registry at all, and the count is how that is asserted.
		DistributionInspectFn: func(context.Context, string) (client.DistributionInspectResult, error) {
			if m.distErr != nil {
				return client.DistributionInspectResult{}, m.distErr
			}
			return dockertest.DistributionResult(m.distDigest), nil
		},
		ImagePullFn: func(context.Context, string) (client.ImagePullResponse, error) {
			if m.pullFn == nil {
				return nil, errors.New("ImagePull must not be called from imageplan.Ensure")
			}
			rc, err := m.pullFn()
			if err != nil {
				return nil, err
			}
			return dockertest.PullResponse{ReadCloser: rc}, nil
		},
	}
	return m.fake
}

func TestEnsureNoOpWhenImagePresent(t *testing.T) {
	mock := &sourceStub{
		inspectFn: func(_ context.Context, _ string) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, nil
		},
	}
	if err := Ensure(context.Background(), mock.docker(), sessionplan.Image{Ref: "ghcr.io/example:latest"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestSyncPullPolicyOnAReload asserts the policy dispatch on the one reason
// that reads the TTL cache: "never" skips the registry round-trip entirely,
// "always" and "auto" both pull (a state dir of its own has an empty cache, so
// "auto" misses and pulls too).
func TestSyncPullPolicyOnAReload(t *testing.T) {
	for _, tt := range []struct {
		policy    string
		wantPulls int
	}{
		{"never", 0},
		{"always", 1},
		{"auto", 1},
		{"", 1}, // empty normalises to auto behaviour
	} {
		t.Run("policy="+tt.policy, func(t *testing.T) {
			mock := &sourceStub{
				pullFn: func() (io.ReadCloser, error) {
					return io.NopCloser(strings.NewReader("")), nil
				},
			}
			Sync(context.Background(), mock.docker(), sessionplan.Image{
				Ref:        "ghcr.io/example:latest",
				PullPolicy: tt.policy,
			}, t.TempDir(), ReasonReload) // a state dir of its own isolates the TTL marker
			if mock.docker().ImagePullCalls() != tt.wantPulls {
				t.Errorf("policy %q: ImagePull called %d times, want %d", tt.policy, mock.docker().ImagePullCalls(), tt.wantPulls)
			}
		})
	}
}

func TestEnsureRegistryMissingErrors(t *testing.T) {
	mock := &sourceStub{
		inspectFn: func(_ context.Context, _ string) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, errors.New("no such image")
		},
	}
	err := Ensure(context.Background(), mock.docker(), sessionplan.Image{Ref: "ghcr.io/example:latest"})
	if err == nil {
		t.Fatal("expected error for missing image")
	}
	if !strings.Contains(err.Error(), "not available locally") {
		t.Errorf("error should mention not-available-locally, got: %v", err)
	}
	if !strings.Contains(err.Error(), "toolbox build") {
		t.Errorf("error should mention `toolbox build`, got: %v", err)
	}
}

// storeWith builds a mock whose local store holds ref at repoDigest ("" for a
// locally built image) and whose registry answers remote.
func storeWith(t *testing.T, repoDigest, remote string) *sourceStub {
	t.Helper()
	return &sourceStub{
		inspectFn: func(context.Context, string) (client.ImageInspectResult, error) {
			return dockertest.ImageInspectResult("ghcr.io/example", repoDigest), nil
		},
		distDigest: remote,
		pullFn:     func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("")), nil },
	}
}

// asked is how the prompt was put: how many times, and which way its
// unattended default pointed — the second being what keeps a window nobody
// answered from destroying a container.
type asked struct {
	times   int
	elapsed ui.Elapsed
}

// answering stands in for the developer at the prompt, recording that the
// question was put at all — which is half of what every case asserts.
func answering(yes, interrupted bool) func(*asked) prompt {
	return func(q *asked) prompt {
		return func(_ string, _ time.Duration, elapsed ui.Elapsed) (bool, bool) {
			q.times++
			q.elapsed = elapsed
			return yes, interrupted
		}
	}
}

var (
	askedYes        = answering(true, false)
	askedNo         = answering(false, false)
	askedAndStopped = answering(false, true)
)

// withPrompt swaps the two prompt seams — is there anyone to ask, and what did
// they answer — for the duration of one test.
func withPrompt(t *testing.T, tty bool, answer prompt) {
	t.Helper()
	oldAskable, oldConfirm := askable, confirm
	askable, confirm = func() bool { return tty }, answer
	t.Cleanup(func() { askable, confirm = oldAskable, oldConfirm })
}

// warmStateDir returns a state mount carrying a published probe result for
// remote and a fresh attempt stamp — the shared answer a sibling session
// leaves behind, and the one this tree reuses instead of re-establishing it.
func warmStateDir(t *testing.T, remote string) string {
	t.Helper()
	dir := t.TempDir()
	body := "image_update=0\nimage_latest=" + remote + "\nimage_state=none\ncli_update=0\ncli_latest=\n"
	if err := os.WriteFile(filepath.Join(dir, "update-check"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := fsx.TouchMarker(filepath.Join(dir, "update-check.stamp")); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestSyncAsksBeforeSpendingTheDevelopersTime is the tree of ADR
// 0005: the prompt fires on exactly one case — an `auto` policy, an image
// already in the store, a registry that is ahead of it, and a tty to ask on.
// Every other case has its answer settled before the question could be put.
//
// The probe count is asserted alongside the outcome, because half the decisions
// here are about not making a registry round-trip.
func TestSyncAsksBeforeSpendingTheDevelopersTime(t *testing.T) {
	const (
		local  = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
		remote = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	)
	for _, tc := range []struct {
		name        string
		policy      string
		reason      Reason
		repoDgst    string
		remote      string
		absent      bool
		pullFails   bool
		warm        string
		tty         bool
		answer      func(*asked) prompt
		wantAsked   int
		wantElapsed ui.Elapsed
		wantPulls   int
		wantProbes  int
		want        Outcome
	}{
		{
			// Not probing is that policy's whole promise, and a probe is a
			// registry round-trip.
			name:   "pull never neither probes nor prompts",
			policy: "never", repoDgst: local, remote: remote,
			tty: true, answer: askedYes,
			want: OutcomeUnsettled,
		},
		{
			// A policy that has already said yes on every shell cannot
			// coherently be asked again.
			name:   "pull always pulls without asking",
			policy: "always", repoDgst: local, remote: remote,
			tty: true, answer: askedYes,
			wantPulls: 1, want: OutcomeCurrent,
		},
		{
			// There is no session to start otherwise, so the block is not a
			// cost but the only honest thing to do.
			name:   "a missing image is pulled without asking",
			policy: "auto", absent: true,
			tty: true, answer: askedNo,
			wantPulls: 1, want: OutcomeCurrent,
		},
		{
			name:   "a store a live probe proves current is not pulled",
			policy: "auto", repoDgst: local, remote: local,
			tty: true, answer: askedYes,
			wantProbes: 1, want: OutcomeCurrent,
		},
		{
			// The same answer read from the shared cache: true when that probe
			// ran, and no claim about now — or the poller's attempt clock would
			// be re-stamped from a cached digest on every shell start.
			name:   "a cached answer claims no sync",
			policy: "auto", repoDgst: local, remote: local, warm: local,
			tty: true, answer: askedYes,
			want: OutcomeUnsettled,
		},
		{
			// The fingerprint of a local `toolbox build`: an automatic pull
			// must never undo an explicit one.
			name:   "a locally built image abstains",
			policy: "auto", repoDgst: "", remote: remote,
			tty: true, answer: askedYes,
			want: OutcomeUnsettled,
		},
		{
			// And the probe is not paid either: knowing the answer is a
			// round-trip too, and off a tty nothing could be done with it.
			name:   "without a tty the session starts now and fetches behind",
			policy: "auto", repoDgst: local, remote: remote,
			tty: false, answer: askedYes,
			want: OutcomeUnsettled,
		},
		{
			name:   "yes pulls synchronously",
			policy: "auto", repoDgst: local, remote: remote,
			tty: true, answer: askedYes,
			wantAsked: 1, wantElapsed: ui.ElapsedYes, wantPulls: 1, wantProbes: 1,
			want: OutcomeAccepted,
		},
		{
			name:   "no starts on the image already in the store",
			policy: "auto", repoDgst: local, remote: remote,
			tty: true, answer: askedNo,
			wantAsked: 1, wantElapsed: ui.ElapsedYes, wantProbes: 1, want: OutcomeDeclined,
		},
		{
			// A ctrl+c is not the "no" it looks like from here: the developer
			// stopped the command, so there is no session left to postpone a
			// download for. It must not settle as OutcomeDeclined or the
			// caller stamps a postponement and arms an idle reload for a
			// session that will never idle.
			name:   "ctrl+c stops the command rather than postponing",
			policy: "auto", repoDgst: local, remote: remote,
			tty: true, answer: askedAndStopped,
			wantAsked: 1, wantElapsed: ui.ElapsedYes, wantProbes: 1, want: OutcomeInterrupted,
		},
		{
			// The same question where a yes also discards the container the
			// developer would otherwise have started: the window may not
			// answer it, and the answer is reported as an answer rather than
			// as a sync, because only a developer's yes may cost a container.
			name:   "a yes where a container is at stake is reported as accepted",
			policy: "auto", reason: ReasonStart, repoDgst: local, remote: remote,
			tty: true, answer: askedYes,
			wantAsked: 1, wantElapsed: ui.ElapsedNo, wantPulls: 1, wantProbes: 1,
			want: OutcomeAccepted,
		},
		{
			// The answer stands, the download did not: acting on it would
			// destroy the container in exchange for an image that never
			// arrived, so nothing is reported for the caller to act on.
			name:   "a yes the registry could not honour is not an acceptance",
			policy: "auto", reason: ReasonStart, repoDgst: local, remote: remote, pullFails: true,
			tty: true, answer: askedYes,
			wantAsked: 1, wantElapsed: ui.ElapsedNo, wantPulls: 1, wantProbes: 1,
			want: OutcomeUnsettled,
		},
		{
			name:   "a no where a container is at stake postpones like any other",
			policy: "auto", reason: ReasonStart, repoDgst: local, remote: remote,
			tty: true, answer: askedNo,
			wantAsked: 1, wantElapsed: ui.ElapsedNo, wantProbes: 1,
			want: OutcomeDeclined,
		},
		{
			// `always` still pulls without asking, and still rebuilds nothing:
			// a policy about downloads has never said anything about
			// destroying a container, so nothing here is accepted on the
			// developer's behalf.
			name:   "pull always spends no container it was not asked about",
			policy: "always", reason: ReasonStart, repoDgst: local, remote: remote,
			tty: true, answer: askedYes,
			wantPulls: 1, want: OutcomeCurrent,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := storeWith(t, tc.repoDgst, tc.remote)
			if tc.pullFails {
				mock.pullFn = func() (io.ReadCloser, error) { return nil, errors.New("unauthorized") }
			}
			if tc.absent {
				mock.inspectFn = func(context.Context, string) (client.ImageInspectResult, error) {
					return client.ImageInspectResult{}, errors.New("no such image")
				}
			}
			var put asked
			withPrompt(t, tc.tty, tc.answer(&put))

			stateDir := t.TempDir()
			if tc.warm != "" {
				stateDir = warmStateDir(t, tc.warm)
			}

			got := Sync(context.Background(), mock.docker(), sessionplan.Image{
				Ref:        "ghcr.io/example:latest",
				PullPolicy: tc.policy,
			}, stateDir, tc.reason)

			if got != tc.want {
				t.Errorf("Sync() = %+v, want %+v", got, tc.want)
			}
			if put.times != tc.wantAsked {
				t.Errorf("the developer was asked %d times, want %d", put.times, tc.wantAsked)
			}
			if tc.wantAsked > 0 && put.elapsed != tc.wantElapsed {
				t.Errorf("the unanswered window would have answered %v, want %v", put.elapsed, tc.wantElapsed)
			}
			if mock.docker().ImagePullCalls() != tc.wantPulls {
				t.Errorf("ImagePull called %d times, want %d", mock.docker().ImagePullCalls(), tc.wantPulls)
			}
			if mock.docker().DistributionInspectCalls() != tc.wantProbes {
				t.Errorf("the registry was probed %d times, want %d", mock.docker().DistributionInspectCalls(), tc.wantProbes)
			}
		})
	}
}

// A probe that does not answer leaves the developer with nothing to be asked
// about: the session starts on what the store holds, and the background
// prefetch is the one that will try the registry again.
func TestSyncStaysSilentWhenTheProbeFails(t *testing.T) {
	mock := storeWith(t, "sha256:1111", "")
	mock.distErr = errors.New("offline")
	var put asked
	withPrompt(t, true, askedYes(&put))

	got := Sync(context.Background(), mock.docker(), sessionplan.Image{
		Ref:        "ghcr.io/example:latest",
		PullPolicy: "auto",
	}, t.TempDir(), ReasonCreate)

	if got != OutcomeUnsettled {
		t.Errorf("Sync() = %+v, want OutcomeUnsettled", got)
	}
	if put.times != 0 || mock.docker().ImagePullCalls() != 0 {
		t.Errorf("asked %d times and pulled %d times, want neither", put.times, mock.docker().ImagePullCalls())
	}
}

// TestSyncOnAReloadNeverAsks pins what the reason now carries on its own. The
// reload used to be guaranteed a silent sync by calling a different function;
// with one entry point, ReasonReload is the whole guarantee — so it is asserted
// against the exact conditions that make every other reason ask: an `auto`
// policy, the image in the store, a registry ahead of it, a tty, and a
// developer who would say yes. It must ask nothing, probe nothing, and sync
// through the TTL cache instead.
func TestSyncOnAReloadNeverAsks(t *testing.T) {
	const (
		local  = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
		remote = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	)
	mock := storeWith(t, local, remote)
	var put asked
	withPrompt(t, true, askedYes(&put))

	got := Sync(context.Background(), mock.docker(), sessionplan.Image{
		Ref:        "ghcr.io/example:latest",
		PullPolicy: "auto",
	}, t.TempDir(), ReasonReload)

	if put.times != 0 {
		t.Errorf("the reload put the question %d times, want 0 — its premise is that the move was already asked for", put.times)
	}
	if n := mock.docker().DistributionInspectCalls(); n != 0 {
		t.Errorf("DistributionInspect called %d times, want 0 — the reload decides from the cache, not a probe", n)
	}
	if n := mock.docker().ImagePullCalls(); n != 1 {
		t.Errorf("ImagePull called %d times, want 1 — a cold TTL cache must still pull", n)
	}
	if got != OutcomeCurrent {
		t.Errorf("Sync() = %+v, want OutcomeCurrent — a reload can neither accept nor decline", got)
	}
}

// TestOutcomeReadsBackAsTheCaseItIs pins the two derivations the settlement
// value replaced fields with. Synced is the load-bearing one — it is what
// Shell hands the prefetch as Input.StartSynced, and the whole point of
// deriving it is that only a landed download may claim it: a decline, an
// interrupt and an unsettled sync each downloaded nothing.
//
// String is pinned in the same table because it is read on a failing test or a
// warning, and the case that must never appear there is a settlement printed
// as the zero value's name — "unsettled" is the claim that nothing happened.
func TestOutcomeReadsBackAsTheCaseItIs(t *testing.T) {
	tests := []struct {
		outcome    Outcome
		wantString string
		wantSynced bool
	}{
		{OutcomeUnsettled, "unsettled", false},
		{OutcomeCurrent, "current", true},
		{OutcomeDeclined, "declined", false},
		{OutcomeInterrupted, "interrupted", false},
		{OutcomeAccepted, "accepted", true},
		// A constant added without a case in String: it must not borrow the
		// zero value's name, which would report a settlement as "nothing was
		// established" — the one claim this type exists to make unspellable.
		{Outcome(99), "Outcome(99)", false},
	}
	for _, tc := range tests {
		t.Run(tc.wantString, func(t *testing.T) {
			if got := tc.outcome.String(); got != tc.wantString {
				t.Errorf("String() = %q, want %q", got, tc.wantString)
			}
			if got := tc.outcome.Synced(); got != tc.wantSynced {
				t.Errorf("Synced() = %v, want %v — only a landed download proves the store current", got, tc.wantSynced)
			}
		})
	}
}
