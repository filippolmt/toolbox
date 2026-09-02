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

// markerPath stability + HOME-based rooting is load-bearing for the TTL
// cache: drift here silently relocates the cache to a different path on
// every CLI version, defeating the cross-invocation persistence the seam
// is built to provide.
func TestMarkerPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	p1, err := markerPath("ghcr.io/filippolmt/toolbox:latest")
	if err != nil {
		t.Fatalf("markerPath: unexpected error: %v", err)
	}

	want := filepath.Join(home, ".toolbox", "toolbox", "state", "pull-cache")
	if !strings.HasPrefix(p1, want) {
		t.Errorf("markerPath root = %q, want prefix %q", p1, want)
	}

	p2, _ := markerPath("ghcr.io/filippolmt/toolbox:latest")
	if p1 != p2 {
		t.Errorf("markerPath not stable: %q != %q", p1, p2)
	}

	p3, _ := markerPath("ghcr.io/filippolmt/toolbox:edge")
	if p1 == p3 {
		t.Errorf("distinct refs collided to same marker path: %q", p1)
	}
}

func TestCachedMissingMarker(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if cached("ghcr.io/foo/bar:latest") {
		t.Error("cached returned true for missing marker")
	}
}

func TestCachedFreshMarker(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ref := "ghcr.io/foo/bar:latest"
	record(ref)
	if !cached(ref) {
		t.Error("cached returned false for freshly recorded marker")
	}
}

// Marker older than TTL must NOT be treated as a cache hit: skipping the
// manifest check past the trust window is the exact bug the TTL guard is
// meant to prevent.
func TestCachedStaleMarker(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ref := "ghcr.io/foo/bar:latest"
	record(ref)

	p, _ := markerPath(ref)
	old := time.Now().Add(-2 * TTL)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	if cached(ref) {
		t.Error("cached returned true for marker older than TTL")
	}
}

func TestRecordCreatesMissingDirs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	record("ghcr.io/foo/bar:latest")

	dir := filepath.Join(home, ".toolbox", "toolbox", "state", "pull-cache")
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

// pullMock is a daemon that answers ImagePull and nothing else: the refresh
// seam touches no other endpoint, and an embedded nil APIClient turns any
// other call into a panic that names the drift.
type pullMock struct {
	client.APIClient
	err   error
	calls int
}

func (m *pullMock) ImagePull(context.Context, string, client.ImagePullOptions) (client.ImagePullResponse, error) {
	m.calls++
	if m.err != nil {
		return nil, m.err
	}
	return dockertest.PullResponse{ReadCloser: io.NopCloser(strings.NewReader(""))}, nil
}

// TestRefreshIfStaleReportsTheRegistryRoundTrip pins the fact the update
// prefetch reads: whether this shell start actually synced against the
// registry. Only a successful round trip counts — a cache hit did no work,
// and a failed pull leaves the local store possibly behind, so neither may
// let the background poller skip its own probe.
func TestRefreshIfStaleReportsTheRegistryRoundTrip(t *testing.T) {
	ref := "ghcr.io/foo/bar:latest"

	t.Run("successful pull", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		if !RefreshIfStale(t.Context(), &pullMock{}, ref) {
			t.Error("RefreshIfStale = false after a successful pull, want true")
		}
	})

	t.Run("cache hit does no round trip", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		record(ref)
		m := &pullMock{}
		if RefreshIfStale(t.Context(), m, ref) {
			t.Error("RefreshIfStale = true on a cache hit, want false")
		}
		if m.calls != 0 {
			t.Errorf("ImagePull calls = %d on a cache hit, want 0", m.calls)
		}
	})

	t.Run("failed pull is not a sync", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		if RefreshIfStale(t.Context(), &pullMock{err: errors.New("boom")}, ref) {
			t.Error("RefreshIfStale = true after a failed pull, want false")
		}
	})
}

// TestForcePullReportsTheRegistryRoundTrip covers the `pull: always` path,
// which bypasses the cache but owes the caller the same fact.
func TestForcePullReportsTheRegistryRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ref := "ghcr.io/foo/bar:latest"
	record(ref) // a fresh marker ForcePull must ignore

	m := &pullMock{}
	if !ForcePull(t.Context(), m, ref) {
		t.Error("ForcePull = false after a successful pull, want true")
	}
	if m.calls != 1 {
		t.Errorf("ImagePull calls = %d, want 1", m.calls)
	}
}
