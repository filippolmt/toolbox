// Package dockertest provides Docker-client test doubles shared across the
// packages that drive the moby client SDK (container, teardown, runplan,
// imageplan). Each of those packages still defines its own mockClient — the
// fakes differ in which methods they exercise — but the leaf types that were
// byte-identical across packages (the errdefs "not found" error and the
// ImagePullResponse adapter) live here so there is a single copy to update
// when the SDK shifts.
package dockertest

import (
	"context"
	"io"
	"iter"

	"github.com/moby/moby/api/types/jsonstream"
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
