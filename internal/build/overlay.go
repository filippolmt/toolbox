package build

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/moby/moby/client"
)

// BuildOverlay builds a derived image on top of baseImageID by composing a
// `FROM <baseImageID>` line ahead of the user's append-only Dockerfile
// fragment and building it with an empty context (a single in-memory
// "Dockerfile", no COPY/ADD source). Pinning FROM to the base image ID
// (not a tag) binds the build to the exact base just inspected, immune to a
// `:latest` tag moving mid-build. No build args are injected: the overlay is
// RUN-only over an already-built base and would only emit "unused build arg"
// warnings. Build output is streamed via the shared streamBuildOutput.
// imageBuilder is the daemon's build endpoint — the only thing either build in
// this package needs, BuildImage included. Narrow so the overlay's caller can
// stay narrow too: localimage passes its own interface straight through.
// → CONTEXT.md, Declared Docker Surface.
type imageBuilder interface {
	ImageBuild(ctx context.Context, buildContext io.Reader, opts client.ImageBuildOptions) (client.ImageBuildResult, error)
}

func BuildOverlay(ctx context.Context, cli imageBuilder, baseImageID string, dockerfileBytes []byte, tag string) error {
	composed := "FROM " + baseImageID + "\n" + string(dockerfileBytes)

	buildCtx, err := tarSingleDockerfile([]byte(composed))
	if err != nil {
		return fmt.Errorf("creating overlay build context: %w", err)
	}

	resp, err := cli.ImageBuild(ctx, buildCtx, client.ImageBuildOptions{
		Dockerfile: "Dockerfile",
		Tags:       []string{tag},
		Remove:     true,
	})
	if err != nil {
		return fmt.Errorf("starting overlay build: %w", err)
	}
	defer resp.Body.Close()

	return streamBuildOutput(resp.Body)
}

// tarSingleDockerfile wraps dockerfile bytes in an in-memory tar containing
// only a top-level "Dockerfile" — the empty build context that guarantees no
// host file can leak into the overlay image.
func tarSingleDockerfile(dockerfile []byte) (io.Reader, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := writeTarFile(tw, "Dockerfile", 0o644, dockerfile); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("tar close: %w", err)
	}
	return &buf, nil
}
