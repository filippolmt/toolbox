// Package imageplan owns the "is the image ready for ContainerCreate?"
// decision tree. Two phases, two seams:
//
//   - Refresh: best-effort registry sync run on every shell, delegating
//     cache + TTL + observability to imagepull.
//   - Ensure: hard guarantee called before ContainerCreate. If the image
//     is already in the local store, done. Otherwise the registry pull
//     already had its chance and we fail fatally.
//
// The image is always the canonical registry tag (one image, no per-tool
// opt-out, no local-hash fallback), so Ensure never builds — `toolbox
// build` is the explicit user-driven path for a local rebuild.
package imageplan

import (
	"context"
	"fmt"

	"github.com/docker/docker/client"

	"github.com/filippolmt/toolbox/internal/imagepull"
	"github.com/filippolmt/toolbox/internal/sessionplan"
)

// Refresh best-effort syncs the image against its registry. Errors are
// swallowed by imagepull (logged as a warning at most); the caller's
// existing local copy is the fallback.
func Refresh(ctx context.Context, cli client.APIClient, image sessionplan.Image) {
	imagepull.RefreshIfStale(ctx, cli, image.Ref)
}

// Ensure guarantees the image referenced by `image.Ref` exists in the
// local Docker store. Exposed as a package-level variable so tests can
// substitute without spinning up a real build context.
var Ensure = func(ctx context.Context, cli client.APIClient, image sessionplan.Image) error {
	if _, err := cli.ImageInspect(ctx, image.Ref); err == nil {
		return nil
	}
	return fmt.Errorf("image %q not available locally and pull failed — check registry access (run `toolbox build` to build locally)", image.Ref)
}
