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

// PullResponse must satisfy client.ImagePullResponse — the interface mocks
// return from ImagePull. The assignment is the assertion.
func TestPullResponseSatisfiesInterface(t *testing.T) {
	var _ client.ImagePullResponse = PullResponse{ReadCloser: io.NopCloser(strings.NewReader(""))}
}
