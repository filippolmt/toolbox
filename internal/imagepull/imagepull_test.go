package imagepull

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/jsonstream"
	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/dockertest"
)

// markerPath stability + state-dir rooting is load-bearing for the TTL cache:
// drift here silently relocates the cache to a different path on every CLI
// version, defeating the cross-invocation persistence the seam is built to
// provide.
func TestMarkerPath(t *testing.T) {
	stateDir := t.TempDir()

	p1, err := markerPath("ghcr.io/filippolmt/toolbox:latest", stateDir)
	if err != nil {
		t.Fatalf("markerPath: unexpected error: %v", err)
	}

	want := filepath.Join(stateDir, "pull-cache")
	if !strings.HasPrefix(p1, want) {
		t.Errorf("markerPath root = %q, want prefix %q", p1, want)
	}

	p2, _ := markerPath("ghcr.io/filippolmt/toolbox:latest", stateDir)
	if p1 != p2 {
		t.Errorf("markerPath not stable: %q != %q", p1, p2)
	}

	p3, _ := markerPath("ghcr.io/filippolmt/toolbox:edge", stateDir)
	if p1 == p3 {
		t.Errorf("distinct refs collided to same marker path: %q", p1)
	}
}

// A session with no state mount has nowhere to keep the cache. markerPath must
// say so rather than invent the default path: the callers read the error as
// "no cache" and pay one round-trip per invocation, which is the honest cost
// of having disabled the mount.
func TestMarkerPathWithoutAStateDir(t *testing.T) {
	got, err := markerPath("ghcr.io/foo/bar:latest", "")
	if err == nil {
		t.Fatalf("markerPath returned %q for an unresolved state dir, want an error", got)
	}
	if got != "" {
		t.Errorf("markerPath returned a path %q alongside its error", got)
	}
}

func TestCachedMissingMarker(t *testing.T) {
	if cached("ghcr.io/foo/bar:latest", t.TempDir()) {
		t.Error("cached returned true for missing marker")
	}
}

func TestCachedFreshMarker(t *testing.T) {
	stateDir := t.TempDir()
	ref := "ghcr.io/foo/bar:latest"
	record(ref, stateDir)
	if !cached(ref, stateDir) {
		t.Error("cached returned false for freshly recorded marker")
	}
}

// Marker older than TTL must NOT be treated as a cache hit: skipping the
// manifest check past the trust window is the exact bug the TTL guard is
// meant to prevent.
func TestCachedStaleMarker(t *testing.T) {
	stateDir := t.TempDir()
	ref := "ghcr.io/foo/bar:latest"
	record(ref, stateDir)

	p, _ := markerPath(ref, stateDir)
	old := time.Now().Add(-2 * TTL)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	if cached(ref, stateDir) {
		t.Error("cached returned true for marker older than TTL")
	}
}

func TestRecordCreatesMissingDirs(t *testing.T) {
	// Nested, because a retargeted state dir need not exist yet.
	stateDir := filepath.Join(t.TempDir(), "custom-root", "toolbox", "state")

	record("ghcr.io/foo/bar:latest", stateDir)

	dir := filepath.Join(stateDir, "pull-cache")
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("pull-cache dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("pull-cache path exists but is not a directory")
	}
}

func TestIsAuthError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"jsonstream 401", &jsonstream.Error{Code: 401, Message: "unauthorized"}, true},
		{"jsonstream 403", &jsonstream.Error{Code: 403, Message: "forbidden"}, true},
		{"jsonstream 500", &jsonstream.Error{Code: 500, Message: "server error"}, false},
		{"plain unauthorized", errors.New("Error response from daemon: unauthorized: token expired"), true},
		{"plain denied", errors.New("denied: requested access to the resource is denied"), true},
		{"plain auth required", errors.New("authentication required"), true},
		{"plain 403 code", errors.New("received unexpected HTTP status: 403 Forbidden"), true},
		{"network timeout", errors.New("dial tcp: i/o timeout"), false},
		{"image not found", errors.New("manifest unknown: manifest unknown"), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isAuthError(tc.err); got != tc.want {
				t.Errorf("isAuthError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestRegistryOf(t *testing.T) {
	cases := map[string]string{
		"ghcr.io/filippolmt/toolbox:latest": "ghcr.io",
		"debian:bookworm-slim":              "docker.io",
		"library/debian":                    "docker.io",
		"localhost:5000/private/img":        "localhost:5000",
		"registry.example.com/x/y:1":        "registry.example.com",
		"":                                  "docker.io",
	}
	for ref, want := range cases {
		if got := registryOf(ref); got != want {
			t.Errorf("registryOf(%q) = %q, want %q", ref, got, want)
		}
	}
}

// pulling builds a daemon that answers ImagePull and nothing else: the refresh
// seam touches no other endpoint, so the shared fake panics on any other call
// and names the drift.
func pulling(err error) *dockertest.Fake {
	return &dockertest.Fake{
		ImagePullFn: func(context.Context, string) (client.ImagePullResponse, error) {
			if err != nil {
				return nil, err
			}
			return dockertest.PullResponse{ReadCloser: io.NopCloser(strings.NewReader(""))}, nil
		},
	}
}

// TestRefreshIfStaleReportsTheRegistryRoundTrip pins the fact the update
// prefetch reads: whether this shell start actually synced against the
// registry. Only a successful round trip counts — a cache hit did no work,
// and a failed pull leaves the local store possibly behind, so neither may
// let the background poller skip its own probe.
func TestRefreshIfStaleReportsTheRegistryRoundTrip(t *testing.T) {
	ref := "ghcr.io/foo/bar:latest"

	t.Run("successful pull", func(t *testing.T) {
		if !RefreshIfStale(t.Context(), pulling(nil), ref, t.TempDir()) {
			t.Error("RefreshIfStale = false after a successful pull, want true")
		}
	})

	t.Run("cache hit does no round trip", func(t *testing.T) {
		stateDir := t.TempDir()
		record(ref, stateDir)
		m := pulling(nil)
		if RefreshIfStale(t.Context(), m, ref, stateDir) {
			t.Error("RefreshIfStale = true on a cache hit, want false")
		}
		if m.ImagePullCalls() != 0 {
			t.Errorf("ImagePull calls = %d on a cache hit, want 0", m.ImagePullCalls())
		}
	})

	t.Run("failed pull is not a sync", func(t *testing.T) {
		if RefreshIfStale(t.Context(), pulling(errors.New("boom")), ref, t.TempDir()) {
			t.Error("RefreshIfStale = true after a failed pull, want false")
		}
	})
}

// TestForcePullReportsTheRegistryRoundTrip covers the `pull: always` path,
// which bypasses the cache but owes the caller the same fact.
func TestForcePullReportsTheRegistryRoundTrip(t *testing.T) {
	ref := "ghcr.io/foo/bar:latest"
	stateDir := t.TempDir()
	record(ref, stateDir) // a fresh marker ForcePull must ignore

	m := pulling(nil)
	if !ForcePull(t.Context(), m, ref, stateDir) {
		t.Error("ForcePull = false after a successful pull, want true")
	}
	if m.ImagePullCalls() != 1 {
		t.Errorf("ImagePull calls = %d, want 1", m.ImagePullCalls())
	}
}

// TestRefreshIfStaleCachesUnderTheGivenStateDir is the contract
// docs/configuration.md already claims for this cache: it sits under the
// *resolved* toolbox state dir, "mounts_root-aware — alongside the image-pull
// cache". Its two siblings hold to that — localimage derives the overlay
// marker from the overlay Dockerfile's root, imageprefetch takes StateDir as a
// declared input — and this one hardcoded the default location instead, so a
// mounts_root retarget or a --profile left the pull cache outside the tree
// every other marker moved into.
//
// A retargeted state dir is the case that separates the two: a marker stamped
// there must suppress the next refresh. Nothing here touches $HOME, because
// nothing in this package reads one any more.
func TestRefreshIfStaleCachesUnderTheGivenStateDir(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "custom-root", "toolbox", "state")

	ref := "ghcr.io/foo/bar:latest"
	pulls := 0
	cli := &dockertest.Fake{ImagePullFn: func(context.Context, string) (client.ImagePullResponse, error) {
		pulls++
		return dockertest.PullResponse{ReadCloser: io.NopCloser(strings.NewReader(""))}, nil
	}}

	if !RefreshIfStale(context.Background(), cli, ref, stateDir) {
		t.Fatal("first refresh did not pull")
	}
	if pulls != 1 {
		t.Fatalf("pulls = %d after the first refresh, want 1", pulls)
	}

	// The marker must be under the state dir it was handed, not under $HOME.
	if _, err := os.Stat(filepath.Join(stateDir, "pull-cache")); err != nil {
		t.Fatalf("no pull-cache under the given state dir: %v", err)
	}

	if RefreshIfStale(context.Background(), cli, ref, stateDir) {
		t.Error("second refresh pulled again; the marker under the state dir did not register")
	}
	if pulls != 1 {
		t.Errorf("pulls = %d, want the cache to suppress the second round-trip", pulls)
	}
}

// TestRecordWithoutAStateDirIsSilent: a session that resolved no state mount
// has nowhere to keep the cache, and that is a configuration the user chose —
// `mountplan.StateDirPath` documents "" as a supported answer. record's
// warning exists for a cache that *should* work and does not (ENOSPC, EROFS,
// permissions), where the advice "fix the underlying issue and the warning
// stops" holds. Here there is no issue to fix, so warning on every successful
// pull would be noise for the life of the setting. cached is already silent on
// the same input; imageprefetch.Start returns early on it too.
func TestRecordWithoutAStateDirIsSilent(t *testing.T) {
	got := captureStderr(t, func() { record("ghcr.io/foo/bar:latest", "") })
	if got != "" {
		t.Errorf("record wrote %q for a session with no state dir, want silence", got)
	}
}

// The warning must survive for the failure it was written for: a state dir
// that resolves but cannot be written.
func TestRecordWarnsWhenTheCacheIsUnwritable(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	got := captureStderr(t, func() { record("ghcr.io/foo/bar:latest", blocked) })
	if !strings.Contains(got, "pull cache") {
		t.Errorf("record stayed quiet about an unwritable cache; stderr = %q", got)
	}
}

// captureStderr collects what fn writes to os.Stderr — where ui.Warning goes.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stderr
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		buf := make([]byte, 4096)
		for {
			n, readErr := r.Read(buf)
			b.Write(buf[:n])
			if readErr != nil {
				break
			}
		}
		done <- b.String()
	}()
	fn()
	os.Stderr = orig
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}
