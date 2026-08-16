package dockertest

import (
	"io"
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
