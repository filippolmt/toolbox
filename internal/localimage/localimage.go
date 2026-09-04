// Package localimage owns the opt-in local image overlay: when a user-supplied
// overlay Dockerfile is present, it builds a derived image on top of the
// resolved base and returns it for the shell to run from. It mirrors the
// imageplan seam — a single Ensure entry point that passes the base through
// unchanged when the overlay file is absent, and otherwise builds a
// marker-gated `:local` image.
//
// The overlay is append-only and RUN-only: build.BuildOverlay injects
// `FROM <base image ID>` and builds with an empty context, so entrypoint,
// init.d/, and host-UID mapping are inherited unchanged and no host file can
// leak into the image. Rebuilds are gated on a marker (base image ID +
// sha256 of the Dockerfile) so an unchanged base + Dockerfile skips the build
// entirely. Build failures are fatal — the caller aborts the shell rather
// than silently falling back to the base.
package localimage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/build"
	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/fsx"
	"github.com/filippolmt/toolbox/internal/sessionplan"
	"github.com/filippolmt/toolbox/internal/ui"
)

// LocalRef is the tag applied to the derived overlay image. Carries pull
// policy "never" so imageplan's refresh and Ensure never reach a registry for
// it — and so the start-up prompt never offers a download of a built image.
const LocalRef = "ghcr.io/filippolmt/toolbox:local"

// overlayBuilder is the daemon this package needs: the base image's ID to pin
// the overlay's FROM, and the build itself — which build.BuildOverlay performs
// with this very value, since its own interface is a subset of this one.
// → CONTEXT.md, Declared Docker Surface.
type overlayBuilder interface {
	ImageInspect(ctx context.Context, ref string, opts ...client.ImageInspectOption) (client.ImageInspectResult, error)
	ImageBuild(ctx context.Context, buildContext io.Reader, opts client.ImageBuildOptions) (client.ImageBuildResult, error)
}

// buildOverlay is the injection seam: a package-level var overridden in tests
// so they exercise the real marker/inspect/skip logic while stubbing only the
// Docker build (the one part that needs a daemon). Wrapped rather than
// assigned so the seam is spelled in this package's own interface — build's is
// unexported there, and a test could not name it to write a stub.
var buildOverlay = func(ctx context.Context, cli overlayBuilder, baseImageID string, dockerfileBytes []byte, tag string) error {
	return build.BuildOverlay(ctx, cli, baseImageID, dockerfileBytes, tag)
}

// Ensure returns the image the shell should run from. When dockerfilePath is
// absent it returns base unchanged (no build attempted). When present it
// inspects the base image ID, and builds the derived `:local` image unless a
// stored marker (base image ID + sha256 of the Dockerfile) matches the current
// state AND `:local` is already in the local store — in which case the build
// is skipped. On the overlay path it returns the `:local` image with pull
// policy "never". A build or marker-write failure is returned as an error so
// the caller can fail loud.
func Ensure(ctx context.Context, cli overlayBuilder, base sessionplan.Image, dockerfilePath string) (sessionplan.Image, error) {
	if dockerfilePath == "" {
		return base, nil
	}
	dockerfileBytes, err := os.ReadFile(dockerfilePath)
	if errors.Is(err, fs.ErrNotExist) {
		return base, nil
	}
	if err != nil {
		return base, fmt.Errorf("reading overlay Dockerfile %s: %w", dockerfilePath, err)
	}

	inspect, err := cli.ImageInspect(ctx, base.Ref)
	if err != nil {
		// The overlay needs the base image ID locally to pin its FROM. A missing
		// base (e.g. pull: never on a fresh host) surfaces here before the create
		// path's friendlier imageplan.Ensure message, so name the remedy.
		return base, fmt.Errorf("base image %q not available locally to build the overlay — pull it or run `toolbox build`: %w", base.Ref, err)
	}

	sum := sha256.Sum256(dockerfileBytes)
	marker := inspect.ID + "\n" + hex.EncodeToString(sum[:])
	markerFile := markerPath(dockerfilePath)

	local := sessionplan.Image{Ref: LocalRef, PullPolicy: config.PullNever}

	if storedMarker(markerFile) == marker && localImagePresent(ctx, cli) {
		return local, nil
	}

	ui.Info("Building local overlay image…")
	if err := buildOverlay(ctx, cli, inspect.ID, dockerfileBytes, LocalRef); err != nil {
		return base, fmt.Errorf("building local overlay image: %w", err)
	}
	// AtomicWriteFile does not create the destination dir; ensure the state
	// dir exists (normally already materialised by the "state" mount).
	if err := os.MkdirAll(filepath.Dir(markerFile), 0o755); err != nil {
		return base, fmt.Errorf("creating overlay marker dir: %w", err)
	}
	if err := fsx.AtomicWriteFile(markerFile, []byte(marker), 0o644); err != nil {
		return base, fmt.Errorf("writing overlay marker: %w", err)
	}
	return local, nil
}

// markerPath returns the overlay marker location under the toolbox state dir,
// derived from the overlay Dockerfile's root (so mounts_root is honoured
// automatically). Kept alongside the imagepull cache convention
// (~/.toolbox/toolbox/state/…) rather than beside the user's Dockerfile, so
// toolbox-managed state never litters the user-facing config dir.
func markerPath(dockerfilePath string) string {
	root := filepath.Dir(dockerfilePath)
	return filepath.Join(root, "toolbox", "state", "local-overlay.marker")
}

// localImagePresent reports whether the derived `:local` image is in the local
// store (an inspect that succeeds). A rebuild is forced when it is absent even
// if the marker matches.
func localImagePresent(ctx context.Context, cli overlayBuilder) bool {
	_, err := cli.ImageInspect(ctx, LocalRef)
	return err == nil
}

// storedMarker reads the persisted overlay marker, returning "" when absent or
// unreadable so the caller treats it as a mismatch and rebuilds rather than
// silently skipping on uncertainty.
func storedMarker(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}
