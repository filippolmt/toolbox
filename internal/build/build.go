package build

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"runtime"

	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/client"
	"github.com/filippolmt/toolbox/internal/ui"
)

// buildMessage is a single JSON message from the Docker build output stream.
type buildMessage struct {
	Stream string `json:"stream"`
	Error  string `json:"error"`
}

// Options tunes an image build.
type Options struct {
	// Tag applied to the built image (e.g. "toolbox:local-<hash>").
	Tag string
	// BuildArgs passed to docker build (e.g. INSTALL_GCLOUD=false).
	BuildArgs map[string]*string
	// NoCache forces a clean build ignoring Docker's layer cache.
	NoCache bool
}

// BuildImage builds the Docker image from the embedded build context.
// The context (Dockerfile + scripts) is shipped inside the Go binary (see
// embed.go), so the CLI does not depend on the user having a repo checkout.
func BuildImage(ctx context.Context, cli client.APIClient, opts Options) error {
	ui.Info("Building image " + opts.Tag + "...")

	buildCtx, err := tarEmbeddedContext()
	if err != nil {
		return fmt.Errorf("creating build context: %w", err)
	}

	resp, err := cli.ImageBuild(ctx, buildCtx, build.ImageBuildOptions{
		Dockerfile: "Dockerfile",
		Tags:       []string{opts.Tag},
		BuildArgs:  mergeBuildArgs(opts.BuildArgs),
		NoCache:    opts.NoCache,
		Remove:     true,
	})
	if err != nil {
		return fmt.Errorf("starting image build: %w", err)
	}
	defer resp.Body.Close()

	if err := streamBuildOutput(resp.Body); err != nil {
		return err
	}

	ui.Success("Image " + opts.Tag + " built successfully")
	return nil
}

// streamBuildOutput reads Docker's JSON build stream and prints output in real time.
func streamBuildOutput(reader io.Reader) error {
	scanner := bufio.NewScanner(reader)
	// Buildx produces long lines with multi-line stream fragments; bump the
	// per-line limit so we don't bail out on a large log message.
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		var msg buildMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			// Ignore non-JSON lines (possible keep-alives).
			continue
		}
		if msg.Error != "" {
			return fmt.Errorf("build error: %s", msg.Error)
		}
		if msg.Stream != "" {
			fmt.Print(msg.Stream)
		}
	}
	return scanner.Err()
}

// mergeBuildArgs injects the host-derived build args required by the
// Dockerfile on top of the caller-provided map. BuildKit auto-populates
// TARGETARCH for multi-arch builds, but the classic Docker builder used by
// the Go SDK's ImageBuild API does not — we must supply it ourselves.
// Caller-provided values take precedence.
func mergeBuildArgs(args map[string]*string) map[string]*string {
	out := map[string]*string{}
	arch := runtime.GOARCH // "amd64" or "arm64" — matches Docker's TARGETARCH naming.
	out["TARGETARCH"] = &arch
	for k, v := range args {
		out[k] = v
	}
	return out
}

// tarEmbeddedContext serialises the embedded assets into an in-memory tar the
// Docker daemon can consume as a build context. Filenames inside the tar are
// the basenames of the assets — the Dockerfile's `COPY bashrc.sh …` resolves
// to the tarred file of the same name.
func tarEmbeddedContext() (io.Reader, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	entries, err := fs.ReadDir(Assets, AssetDir)
	if err != nil {
		return nil, fmt.Errorf("read embedded assets: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := fs.ReadFile(Assets, AssetDir+"/"+e.Name())
		if err != nil {
			return nil, fmt.Errorf("read embedded %s: %w", e.Name(), err)
		}
		info, err := e.Info()
		if err != nil {
			return nil, fmt.Errorf("stat embedded %s: %w", e.Name(), err)
		}
		mode := int64(0o644)
		if info.Mode()&0o111 != 0 {
			mode = 0o755
		}
		hdr := &tar.Header{
			Name: e.Name(),
			Mode: mode,
			Size: int64(len(data)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, fmt.Errorf("tar header %s: %w", e.Name(), err)
		}
		if _, err := tw.Write(data); err != nil {
			return nil, fmt.Errorf("tar write %s: %w", e.Name(), err)
		}
	}
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("tar close: %w", err)
	}
	return &buf, nil
}
