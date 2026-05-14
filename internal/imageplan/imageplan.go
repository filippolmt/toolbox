// Package imageplan owns the "is the image ready for ContainerCreate?"
// decision tree. Two phases, two seams:
//
//   - Refresh: best-effort registry sync run on every shell. For
//     non-local images, asks imagepull.RefreshIfStale to revalidate the
//     local cache against the registry; cache + TTL + observability live
//     in imagepull. No-op for local hash-tagged images.
//   - Ensure: hard guarantee called before ContainerCreate. If the image
//     is already in the local store, done. Otherwise, registry tags fail
//     fatally (the upstream pull already had its chance); local hash tags
//     auto-build from the embedded Dockerfile context using the tools
//     map's BuildArgs.
//
// Before this concept was named, the decision was split: the inline
// `imagepull.RefreshIfStale` call at the top of `container.Shell` and a
// package-level `ensureImage` var stub in `internal/container/lifecycle.go`.
// Reading either site alone missed half the policy ("when do we rebuild?"),
// and the build.BuildImage call sat behind a per-package test stub that
// rebuilt the same indirection in every dependent test. Lifting both
// phases here gives the policy a single owner and turns the auto-build
// seam into a single package-level var inside imageplan that lifecycle
// tests can swap without redeclaring the closure each time.
package imageplan

import (
	"context"
	"fmt"

	"github.com/docker/docker/client"

	"github.com/filippolmt/toolbox/internal/build"
	"github.com/filippolmt/toolbox/internal/imagepull"
	"github.com/filippolmt/toolbox/internal/sessionplan"
	"github.com/filippolmt/toolbox/internal/ui"
)

// Refresh best-effort syncs the image against its registry. Returns
// without contacting the daemon for local hash-tagged images. Errors
// are swallowed by imagepull (logged as a warning at most); the
// caller's existing local copy is the fallback.
func Refresh(ctx context.Context, cli client.APIClient, image sessionplan.Image) {
	if image.IsLocal {
		return
	}
	imagepull.RefreshIfStale(ctx, cli, image.Ref)
}

// Ensure guarantees the image referenced by `image.Ref` exists in the
// local Docker store. Exposed as a package-level variable so tests can
// substitute it without redeclaring the auto-build closure at every call
// site (the legacy `ensureImage` stub pattern that previously lived in
// internal/container).
var Ensure = func(ctx context.Context, cli client.APIClient, image sessionplan.Image, buildArgs map[string]*string) error {
	if _, err := cli.ImageInspect(ctx, image.Ref); err == nil {
		return nil
	}
	if !image.IsLocal {
		return fmt.Errorf("image %q not available locally and pull failed — check registry access", image.Ref)
	}

	ui.Info("Image not found locally — building " + image.Ref + " for current tools config...")
	return buildImageFn(ctx, cli, build.Options{
		Tag:       image.Ref,
		BuildArgs: buildArgs,
	})
}

// buildImageFn is the inner seam tests rely on when they want to assert
// the build was triggered without actually running it. Production points
// at build.BuildImage; tests assign a recording stub.
var buildImageFn = build.BuildImage
