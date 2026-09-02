package dockertest

import (
	"io"
	"slices"
	"strings"
	"testing"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/client"
)

// NotFoundError must satisfy the errdefs "not found" interface, otherwise the
// packages that use it to drive cerrdefs.IsNotFound branches would test the
// wrong path.
func TestNotFoundErrorSatisfiesErrdefs(t *testing.T) {
	if !cerrdefs.IsNotFound(&NotFoundError{Msg: "test"}) {
		t.Fatal("NotFoundError should satisfy cerrdefs.IsNotFound")
	}
}

// The marker method and Unwrap round out the errdefs contract: Error carries
// the message, Unwrap terminates the chain rather than looping.
func TestNotFoundErrorShape(t *testing.T) {
	err := &NotFoundError{Msg: "no such container"}
	if err.Error() != "no such container" {
		t.Errorf("Error() = %q, want %q", err.Error(), "no such container")
	}
	if err.Unwrap() != nil {
		t.Errorf("Unwrap() = %v, want nil", err.Unwrap())
	}
	err.NotFound()
}

// PullResponse must satisfy client.ImagePullResponse — the interface mocks
// return from ImagePull. The assignment is the assertion.
func TestPullResponseSatisfiesInterface(t *testing.T) {
	var _ client.ImagePullResponse = PullResponse{ReadCloser: io.NopCloser(strings.NewReader(""))}
}

// A fake pull reports no progress and no error: Wait returns nil and the
// message sequence yields nothing, so a consumer's range loop runs zero times
// instead of blocking or panicking.
func TestPullResponseStreamsNothing(t *testing.T) {
	resp := PullResponse{ReadCloser: io.NopCloser(strings.NewReader(""))}
	if err := resp.Wait(t.Context()); err != nil {
		t.Errorf("Wait() = %v, want nil", err)
	}
	count := 0
	for range resp.JSONMessages(t.Context()) {
		count++
	}
	if count != 0 {
		t.Errorf("JSONMessages yielded %d messages, want 0", count)
	}
}

// The two result builders exist so no caller hand-rolls an OCI descriptor or
// a RepoDigests entry. Their shape is the assertion: a probe fake that put
// the digest in the wrong field would make every prefetch test pass against
// nothing.
func TestResultBuilders(t *testing.T) {
	const digest = "sha256:abc"

	if got := DistributionResult(digest).Descriptor.Digest.String(); got != digest {
		t.Errorf("DistributionResult digest = %q, want %q", got, digest)
	}

	got := ImageInspectResult("ghcr.io/filippolmt/toolbox", digest).RepoDigests
	want := []string{"ghcr.io/filippolmt/toolbox@" + digest}
	if !slices.Equal(got, want) {
		t.Errorf("ImageInspectResult RepoDigests = %v, want %v", got, want)
	}

	// No digest means no entry at all — the fingerprint of an image built
	// locally and never pushed, which is what makes the prefetch abstain.
	if got := ImageInspectResult("ghcr.io/filippolmt/toolbox", "").RepoDigests; len(got) != 0 {
		t.Errorf("RepoDigests = %v, want none for a locally built image", got)
	}
}
