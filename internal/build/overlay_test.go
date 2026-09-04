package build

import (
	"archive/tar"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/dockertest"
)

// buildingOverlay returns a builder that replays stream, or fails with
// buildErr — so BuildOverlay can be exercised without a real Docker daemon.
// It ignores the build context; the one test that asserts on it stubs
// ImageBuild itself.
func buildingOverlay(stream string, buildErr error) *dockertest.Fake {
	return &dockertest.Fake{
		ImageBuildFn: func(context.Context, io.Reader, client.ImageBuildOptions) (client.ImageBuildResult, error) {
			if buildErr != nil {
				return client.ImageBuildResult{}, buildErr
			}
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(stream))}, nil
		},
	}
}

// firstDockerfileLine reads the single "Dockerfile" entry out of the captured
// tar build context and returns its first line.
func firstDockerfileLine(t *testing.T, tarball []byte) string {
	t.Helper()
	tr := tar.NewReader(strings.NewReader(string(tarball)))
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar read: %v", err)
		}
		if h.Name == "Dockerfile" {
			data, _ := io.ReadAll(tr)
			return strings.SplitN(string(data), "\n", 2)[0]
		}
	}
	t.Fatal("no Dockerfile entry in build context")
	return ""
}

func TestBuildOverlayComposesFromBaseImageID(t *testing.T) {
	var captured []byte
	c := &dockertest.Fake{
		ImageBuildFn: func(_ context.Context, buildContext io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			captured, _ = io.ReadAll(buildContext)
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(`{"stream":"Successfully built abc\n"}`))}, nil
		},
	}
	if err := BuildOverlay(context.Background(), c, "sha256:deadbeef", []byte("RUN echo hi\n"), "ghcr.io/example:local"); err != nil {
		t.Fatalf("BuildOverlay: %v", err)
	}
	if got := firstDockerfileLine(t, captured); got != "FROM sha256:deadbeef" {
		t.Errorf("first Dockerfile line = %q, want %q", got, "FROM sha256:deadbeef")
	}
}

func TestBuildOverlayPropagatesStreamError(t *testing.T) {
	c := buildingOverlay(`{"error":"RUN failed with exit code 1"}`, nil)
	err := BuildOverlay(context.Background(), c, "sha256:deadbeef", []byte("RUN false\n"), "ghcr.io/example:local")
	if err == nil {
		t.Fatal("expected error from failing build stream, got nil")
	}
	if !strings.Contains(err.Error(), "RUN failed") {
		t.Errorf("error should carry the build failure, got: %v", err)
	}
}

func TestBuildOverlayPropagatesStartError(t *testing.T) {
	c := buildingOverlay("", errors.New("daemon unreachable"))
	err := BuildOverlay(context.Background(), c, "sha256:deadbeef", []byte("RUN true\n"), "ghcr.io/example:local")
	if err == nil {
		t.Fatal("expected error when ImageBuild fails to start, got nil")
	}
}
