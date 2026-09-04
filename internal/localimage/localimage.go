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
//
// stateDir is the session's resolved state dir, where the marker lives. It is
// a declared input rather than something derived here: the root-resolution
// rule belongs to the Mount Plan, which is the only place a `--share state`
// carve-out and a user `mounts:` patch — the two cases where the state mount
// and the overlay Dockerfile stop sharing a root — are both accounted for.
// See markerPath for the one session shape that resolves no state dir at all.
func Ensure(ctx context.Context, cli overlayBuilder, base sessionplan.Image, dockerfilePath, stateDir string) (sessionplan.Image, error) {
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
	markerFile := markerPath(markerDir(stateDir, filepath.Dir(dockerfilePath)))

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

// markerPath returns the overlay marker's location inside dir — beside the
// pull cache when dir is the session's state dir, and never beside the user's
// own Dockerfile, so toolbox-managed state does not litter the user-facing
// config dir.
func markerPath(dir string) string {
	return filepath.Join(dir, "local-overlay.marker")
}

// markerDir answers where the marker lives: the state dir the session
// resolved, or — for the one session shape that resolved no state mount — the
// default state location under the overlay Dockerfile's own directory. That
// fallback is the single path this package derives, and it is deliberate: the
// marker is host-side only, nothing in the container reads it, and dropping it
// would not cost one extra check per shell the way a missing pull cache does.
// It would rebuild the overlay image on every single shell for the life of the
// setting. The pull cache can afford to be absent; this cannot.
//
// Kept in step with mountplan's own default by
// TestFallbackTracksTheDefaultStateMount.
func markerDir(stateDir, overlayDir string) string {
	if stateDir != "" {
		return stateDir
	}
	return filepath.Join(overlayDir, "toolbox", "state")
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
