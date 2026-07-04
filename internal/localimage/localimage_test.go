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
	"github.com/filippolmt/toolbox/internal/sessionplan"
)

const baseRef = "ghcr.io/filippolmt/toolbox:latest"
const baseID = "sha256:baseimageid"

// fakeClient stubs the ImageInspect calls Ensure makes: it always resolves the
// base ref to baseID, and resolves LocalRef only when localPresent is set.
type fakeClient struct {
	client.APIClient
	localPresent bool
}

func (f *fakeClient) ImageInspect(_ context.Context, ref string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
	switch ref {
	case baseRef:
		return client.ImageInspectResult{InspectResponse: image.InspectResponse{ID: baseID}}, nil
	case LocalRef:
		if f.localPresent {
			return client.ImageInspectResult{InspectResponse: image.InspectResponse{ID: "sha256:localid"}}, nil
		}
		return client.ImageInspectResult{}, errors.New("no such image")
	default:
		return client.ImageInspectResult{}, errors.New("unexpected ref " + ref)
	}
}

// withStubBuilder swaps buildOverlay for a counting stub and restores it.
func withStubBuilder(t *testing.T, err error) *int {
	t.Helper()
	calls := 0
	orig := buildOverlay
	buildOverlay = func(_ context.Context, _ client.APIClient, _ string, _ []byte, _ string) error {
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

// seedMarker writes content to the overlay marker location for path, creating
// the (nested) state dir first.
func seedMarker(t *testing.T, path, content string) {
	t.Helper()
	m := markerPath(path)
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

	got, err := Ensure(context.Background(), &fakeClient{}, base, filepath.Join(t.TempDir(), "Dockerfile"))
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
	got, err := Ensure(context.Background(), &fakeClient{}, base, "")
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
	seedMarker(t, path, "stale marker")

	got, err := Ensure(context.Background(), &fakeClient{localPresent: true}, sessionplan.Image{Ref: baseRef}, path)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if got.Ref != LocalRef || got.PullPolicy != config.PullNever {
		t.Errorf("overlay image = %+v, want %s pull=never", got, LocalRef)
	}
	if *calls != 1 {
		t.Errorf("stale marker must rebuild, build calls=%d", *calls)
	}
	if b, _ := os.ReadFile(markerPath(path)); string(b) != markerFor(t, "RUN echo new\n") {
		t.Errorf("marker not refreshed after rebuild: %q", b)
	}
}

func TestEnsureMatchingMarkerSkips(t *testing.T) {
	calls := withStubBuilder(t, nil)
	dockerfile := "RUN echo same\n"
	path := writeOverlay(t, dockerfile)
	seedMarker(t, path, markerFor(t, dockerfile))

	got, err := Ensure(context.Background(), &fakeClient{localPresent: true}, sessionplan.Image{Ref: baseRef}, path)
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
	seedMarker(t, path, markerFor(t, dockerfile))

	// Marker matches but :local is absent from the store → must rebuild.
	if _, err := Ensure(context.Background(), &fakeClient{localPresent: false}, sessionplan.Image{Ref: baseRef}, path); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if *calls != 1 {
		t.Errorf("missing :local must force rebuild despite marker match, calls=%d", *calls)
	}
}

func TestEnsureBuildFailurePropagates(t *testing.T) {
	withStubBuilder(t, errors.New("RUN exited 1"))
	path := writeOverlay(t, "RUN false\n")

	_, err := Ensure(context.Background(), &fakeClient{}, sessionplan.Image{Ref: baseRef}, path)
	if err == nil {
		t.Fatal("expected build failure to propagate, got nil")
	}
	if _, statErr := os.Stat(markerPath(path)); statErr == nil {
		t.Error("marker must not be written when the build fails")
	}
}
