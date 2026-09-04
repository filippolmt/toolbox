package localimage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/dockertest"
	"github.com/filippolmt/toolbox/internal/fsx"
	"github.com/filippolmt/toolbox/internal/mountplan"
	"github.com/filippolmt/toolbox/internal/sessionplan"
)

const baseRef = "ghcr.io/filippolmt/toolbox:latest"
const baseID = "sha256:baseimageid"

// store stubs the ImageInspect calls Ensure makes: it always resolves the base
// ref to baseID, and resolves LocalRef only when localPresent is set. The
// build endpoint is left unstubbed on purpose — every test here goes through
// the buildOverlay seam, so a build that reached the daemon panics naming it.
func store(localPresent bool) *dockertest.Fake {
	return &dockertest.Fake{
		ImageInspectFn: func(_ context.Context, ref string) (client.ImageInspectResult, error) {
			switch ref {
			case baseRef:
				return client.ImageInspectResult{InspectResponse: image.InspectResponse{ID: baseID}}, nil
			case LocalRef:
				if localPresent {
					return client.ImageInspectResult{InspectResponse: image.InspectResponse{ID: "sha256:localid"}}, nil
				}
				return client.ImageInspectResult{}, errors.New("no such image")
			default:
				return client.ImageInspectResult{}, errors.New("unexpected ref " + ref)
			}
		},
	}
}

// withStubBuilder swaps buildOverlay for a counting stub and restores it.
func withStubBuilder(t *testing.T, err error) *int {
	t.Helper()
	calls := 0
	orig := buildOverlay
	buildOverlay = func(_ context.Context, _ overlayBuilder, _ string, _ []byte, _ string) error {
		calls++
		return err
	}
	t.Cleanup(func() { buildOverlay = orig })
	return &calls
}

func markerFor(t *testing.T, dockerfile string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(dockerfile))
	return baseID + "\n" + hex.EncodeToString(sum[:])
}

func writeOverlay(t *testing.T, dockerfile string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "Dockerfile")
	if err := os.WriteFile(path, []byte(dockerfile), 0o644); err != nil {
		t.Fatalf("write overlay: %v", err)
	}
	return path
}

// seedMarker writes content to the overlay marker location under stateDir,
// creating the (nested) dir first — a retargeted state dir need not exist yet.
func seedMarker(t *testing.T, stateDir, content string) {
	t.Helper()
	m := markerPath(stateDir)
	if err := os.MkdirAll(filepath.Dir(m), 0o755); err != nil {
		t.Fatalf("mkdir marker dir: %v", err)
	}
	if err := os.WriteFile(m, []byte(content), 0o644); err != nil {
		t.Fatalf("seed marker: %v", err)
	}
}

func TestEnsureNoOverlayPassthrough(t *testing.T) {
	calls := withStubBuilder(t, nil)
	base := sessionplan.Image{Ref: baseRef, PullPolicy: config.PullAuto}

	got, err := Ensure(context.Background(), store(false), base, filepath.Join(t.TempDir(), "Dockerfile"), t.TempDir())
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if got != base {
		t.Errorf("expected base image passthrough, got %+v", got)
	}
	if *calls != 0 {
		t.Errorf("no build expected when overlay absent, got %d", *calls)
	}
}

func TestEnsureEmptyPathPassthrough(t *testing.T) {
	calls := withStubBuilder(t, nil)
	base := sessionplan.Image{Ref: baseRef}
	got, err := Ensure(context.Background(), store(false), base, "", t.TempDir())
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if got != base || *calls != 0 {
		t.Errorf("empty path must passthrough with no build; got %+v calls=%d", got, *calls)
	}
}

func TestEnsureStaleMarkerRebuilds(t *testing.T) {
	calls := withStubBuilder(t, nil)
	path := writeOverlay(t, "RUN echo new\n")
	stateDir := t.TempDir()
	seedMarker(t, stateDir, "stale marker")

	got, err := Ensure(context.Background(), store(true), sessionplan.Image{Ref: baseRef}, path, stateDir)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if got.Ref != LocalRef || got.PullPolicy != config.PullNever {
		t.Errorf("overlay image = %+v, want %s pull=never", got, LocalRef)
	}
	if *calls != 1 {
		t.Errorf("stale marker must rebuild, build calls=%d", *calls)
	}
	if b, _ := os.ReadFile(markerPath(stateDir)); string(b) != markerFor(t, "RUN echo new\n") {
		t.Errorf("marker not refreshed after rebuild: %q", b)
	}
}

func TestEnsureMatchingMarkerSkips(t *testing.T) {
	calls := withStubBuilder(t, nil)
	dockerfile := "RUN echo same\n"
	path := writeOverlay(t, dockerfile)
	stateDir := t.TempDir()
	seedMarker(t, stateDir, markerFor(t, dockerfile))

	got, err := Ensure(context.Background(), store(true), sessionplan.Image{Ref: baseRef}, path, stateDir)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if got.Ref != LocalRef {
		t.Errorf("want %s, got %s", LocalRef, got.Ref)
	}
	if *calls != 0 {
		t.Errorf("matching marker + present :local must skip build, calls=%d", *calls)
	}
}

func TestEnsureMissingLocalImageRebuilds(t *testing.T) {
	calls := withStubBuilder(t, nil)
	dockerfile := "RUN echo same\n"
	path := writeOverlay(t, dockerfile)
	stateDir := t.TempDir()
	seedMarker(t, stateDir, markerFor(t, dockerfile))

	// Marker matches but :local is absent from the store → must rebuild.
	if _, err := Ensure(context.Background(), store(false), sessionplan.Image{Ref: baseRef}, path, stateDir); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if *calls != 1 {
		t.Errorf("missing :local must force rebuild despite marker match, calls=%d", *calls)
	}
}

func TestEnsureBuildFailurePropagates(t *testing.T) {
	withStubBuilder(t, errors.New("RUN exited 1"))
	path := writeOverlay(t, "RUN false\n")
	stateDir := t.TempDir()

	_, err := Ensure(context.Background(), store(false), sessionplan.Image{Ref: baseRef}, path, stateDir)
	if err == nil {
		t.Fatal("expected build failure to propagate, got nil")
	}
	if _, statErr := os.Stat(markerPath(stateDir)); statErr == nil {
		t.Error("marker must not be written when the build fails")
	}
}

// TestEnsureKeepsTheMarkerUnderTheGivenStateDir pins the marker to the state
// dir the session resolved. It used to be re-derived from the overlay
// Dockerfile's own root, which tracked a plain mounts_root or profile root by
// construction and parted company with the real state mount exactly where the
// two roots do: `--share state` keeps the mount on ~/.toolbox while the
// Dockerfile follows the profile, and a user `mounts:` patch can move the
// source anywhere. See TestEnsureRebuildsForASiblingsOverlay for what the
// divergence cost.
func TestEnsureKeepsTheMarkerUnderTheGivenStateDir(t *testing.T) {
	calls := withStubBuilder(t, nil)
	dockerfile := "RUN echo retargeted\n"
	path := writeOverlay(t, dockerfile)
	stateDir := filepath.Join(t.TempDir(), "profile-root", "toolbox", "state")

	if _, err := Ensure(context.Background(), store(true), sessionplan.Image{Ref: baseRef}, path, stateDir); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if *calls != 1 {
		t.Fatalf("first Ensure did not build, calls=%d", *calls)
	}
	if b, err := os.ReadFile(filepath.Join(stateDir, "local-overlay.marker")); err != nil {
		t.Fatalf("no marker under the given state dir: %v", err)
	} else if string(b) != markerFor(t, dockerfile) {
		t.Errorf("marker = %q, want %q", b, markerFor(t, dockerfile))
	}

	// The marker it just wrote is the one it must read back: a second Ensure
	// with the same state dir skips the build.
	if _, err := Ensure(context.Background(), store(true), sessionplan.Image{Ref: baseRef}, path, stateDir); err != nil {
		t.Fatalf("second Ensure: %v", err)
	}
	if *calls != 1 {
		t.Errorf("second Ensure rebuilt, calls=%d — the marker under the state dir did not register", *calls)
	}
}

// TestEnsureStillGatesRebuildsWithNoStateDir pins the fallback for the one
// session shape that resolves no state mount: the marker lands under the
// default state location for the overlay Dockerfile's own root, so the rebuild
// stays gated. Why that fallback exists at all is on markerDir.
func TestEnsureStillGatesRebuildsWithNoStateDir(t *testing.T) {
	calls := withStubBuilder(t, nil)
	path := writeOverlay(t, "RUN echo unmounted\n")

	if _, err := Ensure(context.Background(), store(true), sessionplan.Image{Ref: baseRef}, path, ""); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if *calls != 1 {
		t.Fatalf("first Ensure did not build, calls=%d", *calls)
	}
	// Named, not merely present: an unjoined empty state dir would leave a
	// *relative* marker in whatever directory the CLI was invoked from, which
	// gates a rebuild just as well from inside one working tree and litters it.
	want := filepath.Join(filepath.Dir(path), "toolbox", "state", "local-overlay.marker")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("no marker at the fallback location %s: %v", want, err)
	}
	if _, err := Ensure(context.Background(), store(true), sessionplan.Image{Ref: baseRef}, path, ""); err != nil {
		t.Fatalf("second Ensure: %v", err)
	}
	if *calls != 1 {
		t.Errorf("second Ensure rebuilt, calls=%d — a session with no state mount lost its rebuild gate", *calls)
	}
}

// TestEnsureRebuildsForASiblingsOverlay is the safety property the shared
// state dir buys, and the reason the re-derived path was worth removing:
// `:local` is one global tag, so two sessions that share a state dir must
// share one marker, or both can read "marker matches, :local present" for an
// image the other overwrote and start from the wrong overlay.
func TestEnsureRebuildsForASiblingsOverlay(t *testing.T) {
	calls := withStubBuilder(t, nil)
	stateDir := t.TempDir()
	mine := writeOverlay(t, "RUN echo mine\n")
	sibling := writeOverlay(t, "RUN echo sibling\n")

	// The sibling built last, so it owns :local and stamped the shared marker.
	if _, err := Ensure(context.Background(), store(true), sessionplan.Image{Ref: baseRef}, sibling, stateDir); err != nil {
		t.Fatalf("sibling Ensure: %v", err)
	}
	if *calls != 1 {
		t.Fatalf("sibling Ensure did not build, calls=%d", *calls)
	}

	// :local is present and a marker exists — but it is not this overlay's.
	if _, err := Ensure(context.Background(), store(true), sessionplan.Image{Ref: baseRef}, mine, stateDir); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if *calls != 2 {
		t.Errorf("calls=%d — started from the sibling's :local instead of rebuilding", *calls)
	}
	if b, _ := os.ReadFile(markerPath(stateDir)); string(b) != markerFor(t, "RUN echo mine\n") {
		t.Errorf("marker = %q, want this overlay's %q", b, markerFor(t, "RUN echo mine\n"))
	}
}

// TestFallbackTracksTheDefaultStateMount is the drift guard on the one path
// this package derives itself. markerDir's fallback reproduces mountplan's
// default state-mount source, and nothing but this test ties the two
// together: if the default moves — a renamed source, a different sub-path —
// the fallback would keep pointing at a directory that no longer means
// anything, silently, on the only session shape that uses it.
//
// The assertion is the equality, not either path's spelling: the default is
// mountplan's to change, and this only insists the copy follows.
func TestFallbackTracksTheDefaultStateMount(t *testing.T) {
	host := fsx.Host{Home: filepath.Join(t.TempDir(), "home")}

	resolved, err := mountplan.StateDirPath(host, &config.Config{}, nil)
	if err != nil {
		t.Fatalf("StateDirPath: %v", err)
	}
	if resolved == "" {
		t.Fatal("the default config resolves no state mount — this guard has nothing to compare against")
	}

	overlay, err := mountplan.OverlayDockerfilePath(host, &config.Config{}, nil)
	if err != nil {
		t.Fatalf("OverlayDockerfilePath: %v", err)
	}

	// "" is the session that resolved no state mount: the case the fallback
	// exists for, and the only one where these two may not diverge.
	if got := markerDir("", filepath.Dir(overlay)); got != resolved {
		t.Errorf("fallback marker dir = %q, but mountplan resolves the default state mount to %q — the derived copy has drifted", got, resolved)
	}
}
