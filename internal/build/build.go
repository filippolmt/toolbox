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
	"maps"
	"os"
	"runtime"
	"strings"

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
	// Tag applied to the built image. Always the canonical registry tag —
	// `toolbox build` overwrites the local cache so the next `toolbox shell`
	// picks up the freshly built image.
	Tag string
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
		BuildArgs:  mergeBuildArgs(nil),
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
			// Build log is diagnostic output; keep stdout clean for program output.
			fmt.Fprint(os.Stderr, msg.Stream)
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
	maps.Copy(out, args)
	return out
}

// tarEmbeddedContext serialises the embedded assets into an in-memory tar the
// Docker daemon can consume as a build context. Filenames inside the tar are
// the path of each asset relative to AssetDir — top-level files keep their
// basename (so `COPY bashrc.sh …` resolves) and nested entries (e.g.
// `init.d/10-rtk.sh`) keep their subdirectory prefix so `COPY init.d/ …`
// resolves too.
//
// Files under init.d/ get tar mode 0755 unconditionally because embed.FS
// strips executable bits to 0444 — the in-tar mode is the belt half of the
// belt-and-braces guarantee with the Dockerfile chmod.
func tarEmbeddedContext() (io.Reader, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	walkErr := fs.WalkDir(Assets, AssetDir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel := strings.TrimPrefix(p, AssetDir+"/")
		data, err := fs.ReadFile(Assets, p)
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", p, err)
		}
		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("stat embedded %s: %w", p, err)
		}
		mode := int64(0o644)
		switch {
		case strings.HasPrefix(rel, "init.d/"):
			mode = 0o755
		case info.Mode()&0o111 != 0:
			mode = 0o755
		}
		hdr := &tar.Header{
			Name: rel,
			Mode: mode,
			Size: int64(len(data)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return fmt.Errorf("tar header %s: %w", rel, err)
		}
		if _, err := tw.Write(data); err != nil {
			return fmt.Errorf("tar write %s: %w", rel, err)
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("tar close: %w", err)
	}
	return &buf, nil
}
