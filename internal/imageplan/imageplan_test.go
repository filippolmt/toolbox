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
)

// mockClient implements the subset of client.APIClient used by Ensure.
type mockClient struct {
	client.APIClient
	imgInspFn  func(ctx context.Context, id string) (client.ImageInspectResult, error)
	pullCount  int
	pullFn     func() (io.ReadCloser, error)
	distDigest string
	distErr    error
	distCalls  int
}

func (m *mockClient) ImageInspect(ctx context.Context, id string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
	if m.imgInspFn != nil {
		return m.imgInspFn(ctx, id)
	}
	return client.ImageInspectResult{}, errors.New("ImageInspect not mocked")
}

func (m *mockClient) ContainerCreate(_ context.Context, _ client.ContainerCreateOptions) (client.ContainerCreateResult, error) {
	return client.ContainerCreateResult{}, errors.New("ContainerCreate must not be called from imageplan.Ensure")
}
func (m *mockClient) ImagePull(_ context.Context, _ string, _ client.ImagePullOptions) (client.ImagePullResponse, error) {
	m.pullCount++
	if m.pullFn != nil {
		rc, err := m.pullFn()
		if err != nil {
			return nil, err
		}
		return dockertest.PullResponse{ReadCloser: rc}, nil
	}
	return nil, errors.New("ImagePull must not be called from imageplan.Ensure")
}
func (m *mockClient) Close() error { return nil }

func TestEnsureNoOpWhenImagePresent(t *testing.T) {
	mock := &mockClient{
		imgInspFn: func(_ context.Context, _ string) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, nil
		},
	}
	if err := Ensure(context.Background(), mock, sessionplan.Image{Ref: "ghcr.io/example:latest"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestRefreshPullPolicy asserts the policy dispatch: "never" skips the
// registry round-trip entirely, "always" and "auto" both pull (a fresh HOME
// has an empty TTL cache, so "auto" misses and pulls too).
func TestRefreshPullPolicy(t *testing.T) {
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
			t.Setenv("HOME", t.TempDir()) // isolate the pull-cache marker dir
			mock := &mockClient{
				pullFn: func() (io.ReadCloser, error) {
					return io.NopCloser(strings.NewReader("")), nil
				},
			}
			Refresh(context.Background(), mock, sessionplan.Image{
				Ref:        "ghcr.io/example:latest",
				PullPolicy: tt.policy,
			})
			if mock.pullCount != tt.wantPulls {
				t.Errorf("policy %q: ImagePull called %d times, want %d", tt.policy, mock.pullCount, tt.wantPulls)
			}
		})
	}
}

func TestEnsureRegistryMissingErrors(t *testing.T) {
	mock := &mockClient{
		imgInspFn: func(_ context.Context, _ string) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, errors.New("no such image")
		},
	}
	err := Ensure(context.Background(), mock, sessionplan.Image{Ref: "ghcr.io/example:latest"})
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

// DistributionInspect answers the remote-digest probe, counting the calls: a
// question answered from the prefetch's warm cache must reach no registry at
// all, and the count is how that is asserted.
func (m *mockClient) DistributionInspect(context.Context, string, client.DistributionInspectOptions) (client.DistributionInspectResult, error) {
	m.distCalls++
	if m.distErr != nil {
		return client.DistributionInspectResult{}, m.distErr
	}
	return dockertest.DistributionResult(m.distDigest), nil
}

// storeWith builds a mock whose local store holds ref at repoDigest ("" for a
// locally built image) and whose registry answers remote.
func storeWith(t *testing.T, repoDigest, remote string) *mockClient {
	t.Helper()
	t.Setenv("HOME", t.TempDir()) // isolate imagepull's TTL marker dir
	return &mockClient{
		imgInspFn: func(context.Context, string) (client.ImageInspectResult, error) {
			return dockertest.ImageInspectResult("ghcr.io/example", repoDigest), nil
		},
		distDigest: remote,
		pullFn:     func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("")), nil },
	}
}

// answering stands in for the developer at the prompt, recording that the
// question was put at all — which is half of what every case asserts.
func answering(answer bool) func(*int) func(string, time.Duration) bool {
	return func(asked *int) func(string, time.Duration) bool {
		return func(string, time.Duration) bool { *asked++; return answer }
	}
}

var (
	askedYes = answering(true)
	askedNo  = answering(false)
)

// withPrompt swaps the two prompt seams — is there anyone to ask, and what did
// they answer — for the duration of one test.
func withPrompt(t *testing.T, tty bool, answer func(string, time.Duration) bool) {
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

// TestRefreshAtStartAsksBeforeSpendingTheDevelopersTime is the tree of ADR
// 0005: the prompt fires on exactly one case — an `auto` policy, an image
// already in the store, a registry that is ahead of it, and a tty to ask on.
// Every other case has its answer settled before the question could be put.
//
// The probe count is asserted alongside the outcome, because half the decisions
// here are about not making a registry round-trip.
func TestRefreshAtStartAsksBeforeSpendingTheDevelopersTime(t *testing.T) {
	const (
		local  = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
		remote = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	)
	for _, tc := range []struct {
		name       string
		policy     string
		repoDgst   string
		remote     string
		absent     bool
		warm       string
		tty        bool
		answer     func(*int) func(string, time.Duration) bool
		wantAsked  int
		wantPulls  int
		wantProbes int
		want       Outcome
	}{
		{
			// Not probing is that policy's whole promise, and a probe is a
			// registry round-trip.
			name:   "pull never neither probes nor prompts",
			policy: "never", repoDgst: local, remote: remote,
			tty: true, answer: askedYes,
			want: Outcome{},
		},
		{
			// A policy that has already said yes on every shell cannot
			// coherently be asked again.
			name:   "pull always pulls without asking",
			policy: "always", repoDgst: local, remote: remote,
			tty: true, answer: askedYes,
			wantPulls: 1, want: Outcome{Synced: true},
		},
		{
			// There is no session to start otherwise, so the block is not a
			// cost but the only honest thing to do.
			name:   "a missing image is pulled without asking",
			policy: "auto", absent: true,
			tty: true, answer: askedNo,
			wantPulls: 1, want: Outcome{Synced: true},
		},
		{
			name:   "a store a live probe proves current is not pulled",
			policy: "auto", repoDgst: local, remote: local,
			tty: true, answer: askedYes,
			wantProbes: 1, want: Outcome{Synced: true},
		},
		{
			// The same answer read from the shared cache: true when that probe
			// ran, and no claim about now — or the poller's attempt clock would
			// be re-stamped from a cached digest on every shell start.
			name:   "a cached answer claims no sync",
			policy: "auto", repoDgst: local, remote: local, warm: local,
			tty: true, answer: askedYes,
			want: Outcome{},
		},
		{
			// The fingerprint of a local `toolbox build`: an automatic pull
			// must never undo an explicit one.
			name:   "a locally built image abstains",
			policy: "auto", repoDgst: "", remote: remote,
			tty: true, answer: askedYes,
			want: Outcome{},
		},
		{
			// And the probe is not paid either: knowing the answer is a
			// round-trip too, and off a tty nothing could be done with it.
			name:   "without a tty the session starts now and fetches behind",
			policy: "auto", repoDgst: local, remote: remote,
			tty: false, answer: askedYes,
			want: Outcome{},
		},
		{
			name:   "yes pulls synchronously",
			policy: "auto", repoDgst: local, remote: remote,
			tty: true, answer: askedYes,
			wantAsked: 1, wantPulls: 1, wantProbes: 1, want: Outcome{Synced: true},
		},
		{
			name:   "no starts on the image already in the store",
			policy: "auto", repoDgst: local, remote: remote,
			tty: true, answer: askedNo,
			wantAsked: 1, wantProbes: 1, want: Outcome{Declined: true},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := storeWith(t, tc.repoDgst, tc.remote)
			if tc.absent {
				mock.imgInspFn = func(context.Context, string) (client.ImageInspectResult, error) {
					return client.ImageInspectResult{}, errors.New("no such image")
				}
			}
			asked := 0
			withPrompt(t, tc.tty, tc.answer(&asked))

			stateDir := t.TempDir()
			if tc.warm != "" {
				stateDir = warmStateDir(t, tc.warm)
			}

			got := RefreshAtStart(context.Background(), mock, sessionplan.Image{
				Ref:        "ghcr.io/example:latest",
				PullPolicy: tc.policy,
			}, stateDir)

			if got != tc.want {
				t.Errorf("RefreshAtStart() = %+v, want %+v", got, tc.want)
			}
			if asked != tc.wantAsked {
				t.Errorf("the developer was asked %d times, want %d", asked, tc.wantAsked)
			}
			if mock.pullCount != tc.wantPulls {
				t.Errorf("ImagePull called %d times, want %d", mock.pullCount, tc.wantPulls)
			}
			if mock.distCalls != tc.wantProbes {
				t.Errorf("the registry was probed %d times, want %d", mock.distCalls, tc.wantProbes)
			}
		})
	}
}

// A probe that does not answer leaves the developer with nothing to be asked
// about: the session starts on what the store holds, and the background
// prefetch is the one that will try the registry again.
func TestRefreshAtStartStaysSilentWhenTheProbeFails(t *testing.T) {
	mock := storeWith(t, "sha256:1111", "")
	mock.distErr = errors.New("offline")
	asked := 0
	withPrompt(t, true, askedYes(&asked))

	got := RefreshAtStart(context.Background(), mock, sessionplan.Image{
		Ref:        "ghcr.io/example:latest",
		PullPolicy: "auto",
	}, t.TempDir())

	if got != (Outcome{}) {
		t.Errorf("RefreshAtStart() = %+v, want the zero outcome", got)
	}
	if asked != 0 || mock.pullCount != 0 {
		t.Errorf("asked %d times and pulled %d times, want neither", asked, mock.pullCount)
	}
}
