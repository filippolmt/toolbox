// Package dockertest provides the Docker-client test doubles shared across the
// packages that drive the moby client SDK.
//
// Fake is the double for every module that declares its own Docker surface —
// the image family, internal/build, internal/imageref and the teardown (see
// CONTEXT.md, Declared Docker Surface). One function field per method those
// interfaces hold, and structural satisfaction is what lets it stand in for an
// interface it cannot name because that interface is unexported in its own
// package.
//
// Two packages still hold the whole client and keep their own hand-rolled
// adapter, and both are structural rather than pending: internal/container is
// the Docker edge, and internal/worktree hands its client to container.Stop.
// Their functions take client.APIClient, which this Fake deliberately does not
// satisfy, so it could not stand in there even if it grew the methods. The
// leaf types that were byte-identical across packages (the errdefs "not found"
// error, the ImagePullResponse adapter, the two result builders) live here
// either way, so there is a single copy to update when the SDK shifts.
package dockertest

import (
	"context"
	"io"
	"iter"

	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/jsonstream"
	"github.com/moby/moby/client"
	godigest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// NotFoundError implements the errdefs "not found" interface, so
// cerrdefs.IsNotFound reports true for it — the shape the daemon returns when
// a container or image is absent.
type NotFoundError struct{ Msg string }

func (e *NotFoundError) Error() string { return e.Msg }
func (e *NotFoundError) Unwrap() error { return nil }

func (e *NotFoundError) NotFound() {
	// Marker method: its presence alone is what satisfies the errdefs
	// interface, so there is no behaviour to implement.
}

// PullResponse adapts a plain io.ReadCloser to client.ImagePullResponse: the
// SDK's pull result is an interface with no public constructor, so a fake has
// to supply the Wait/JSONMessages methods alongside the embedded reader.
type PullResponse struct {
	io.ReadCloser
}

func (PullResponse) Wait(context.Context) error { return nil }
func (PullResponse) JSONMessages(context.Context) iter.Seq2[jsonstream.Message, error] {
	return func(func(jsonstream.Message, error) bool) {
		// An empty iterator: a fake pull streams no progress messages, so the
		// sequence yields nothing and never calls yield.
	}
}

// DistributionResult builds a DistributionInspect result carrying digest —
// the shape the daemon returns for a registry probe. The OCI descriptor and
// its digest type live here rather than in each caller's own mock: the probe
// is stubbed by every package that drives the update prefetch, and a
// hand-rolled descriptor per package is the duplication this package exists
// to hold. The digest is not validated, matching the daemon's own decode.
func DistributionResult(digest string) client.DistributionInspectResult {
	res := client.DistributionInspectResult{}
	res.Descriptor = ocispec.Descriptor{Digest: godigest.Digest(digest)}
	return res
}

// ImageInspectResult builds a local-store inspect carrying one repo digest
// for repo, or none at all — the fingerprint of an image produced by
// `docker build` and never pushed, which is what makes the prefetch abstain.
func ImageInspectResult(repo, digest string) client.ImageInspectResult {
	res := client.ImageInspectResult{}
	if digest != "" {
		res.InspectResponse = image.InspectResponse{RepoDigests: []string{repo + "@" + digest}}
	}
	return res
}
