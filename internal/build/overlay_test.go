package build

import (
	"archive/tar"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/moby/moby/client"
)

// overlayBuildClient captures the build context handed to ImageBuild and
// replays a caller-supplied JSON build stream, so BuildOverlay can be
// exercised without a real Docker daemon.
type overlayBuildClient struct {
	client.APIClient
	captured []byte
	stream   string
	buildErr error
}

func (c *overlayBuildClient) ImageBuild(_ context.Context, buildContext io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
	c.captured, _ = io.ReadAll(buildContext)
	if c.buildErr != nil {
		return client.ImageBuildResult{}, c.buildErr
	}
	return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(c.stream))}, nil
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
	c := &overlayBuildClient{stream: `{"stream":"Successfully built abc\n"}`}
	if err := BuildOverlay(context.Background(), c, "sha256:deadbeef", []byte("RUN echo hi\n"), "ghcr.io/example:local"); err != nil {
		t.Fatalf("BuildOverlay: %v", err)
	}
	if got := firstDockerfileLine(t, c.captured); got != "FROM sha256:deadbeef" {
		t.Errorf("first Dockerfile line = %q, want %q", got, "FROM sha256:deadbeef")
	}
}

func TestBuildOverlayPropagatesStreamError(t *testing.T) {
	c := &overlayBuildClient{stream: `{"error":"RUN failed with exit code 1"}`}
	err := BuildOverlay(context.Background(), c, "sha256:deadbeef", []byte("RUN false\n"), "ghcr.io/example:local")
	if err == nil {
		t.Fatal("expected error from failing build stream, got nil")
	}
	if !strings.Contains(err.Error(), "RUN failed") {
		t.Errorf("error should carry the build failure, got: %v", err)
	}
}

func TestBuildOverlayPropagatesStartError(t *testing.T) {
	c := &overlayBuildClient{buildErr: errors.New("daemon unreachable")}
	err := BuildOverlay(context.Background(), c, "sha256:deadbeef", []byte("RUN true\n"), "ghcr.io/example:local")
	if err == nil {
		t.Fatal("expected error when ImageBuild fails to start, got nil")
	}
}
