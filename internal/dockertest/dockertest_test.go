package dockertest

import (
	"context"
	"fmt"
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

// The zero-value Fake must refuse every call it can answer, naming the method
// in the panic. That refusal is the invariant the image tests express as
// assertions of absence — "the registry was asked nothing", "ImageRemove took
// no force flag" — which until now rode on a nil-pointer dereference from an
// embedded client.APIClient.
func TestFakeZeroValueRefusesEveryCall(t *testing.T) {
	for _, tc := range []struct {
		method string
		call   func(*Fake)
	}{
		{"ImageInspect", func(f *Fake) { _, _ = f.ImageInspect(t.Context(), "ref") }},
		{"ImagePull", func(f *Fake) { _, _ = f.ImagePull(t.Context(), "ref", client.ImagePullOptions{}) }},
		{"DistributionInspect", func(f *Fake) {
			_, _ = f.DistributionInspect(t.Context(), "ref", client.DistributionInspectOptions{})
		}},
		{"ImageList", func(f *Fake) { _, _ = f.ImageList(t.Context(), client.ImageListOptions{}) }},
		{"ImageRemove", func(f *Fake) { _, _ = f.ImageRemove(t.Context(), "id", client.ImageRemoveOptions{}) }},
		{"ImageBuild", func(f *Fake) {
			_, _ = f.ImageBuild(t.Context(), strings.NewReader(""), client.ImageBuildOptions{})
		}},
	} {
		t.Run(tc.method, func(t *testing.T) {
			defer func() {
				r := recover()
				if r == nil {
					t.Fatalf("%s on a zero-value Fake did not panic", tc.method)
				}
				if msg := fmt.Sprint(r); !strings.Contains(msg, tc.method) {
					t.Errorf("panic = %q, want it to name %s", msg, tc.method)
				}
			}()
			tc.call(&Fake{})
		})
	}
}

// A stubbed method answers its field and counts the call — the counters are
// what lets a test assert that a probe or a pull happened exactly once without
// hand-rolling a counter per package.
func TestFakeAnswersItsStubsAndCounts(t *testing.T) {
	f := &Fake{
		ImageInspectFn: func(context.Context, string) (client.ImageInspectResult, error) {
			return ImageInspectResult("repo", "sha256:abc"), nil
		},
		ImagePullFn: func(context.Context, string) (client.ImagePullResponse, error) {
			return PullResponse{ReadCloser: io.NopCloser(strings.NewReader(""))}, nil
		},
		DistributionInspectFn: func(context.Context, string) (client.DistributionInspectResult, error) {
			return DistributionResult("sha256:def"), nil
		},
		ImageListFn: func(context.Context) (client.ImageListResult, error) {
			return client.ImageListResult{}, nil
		},
		ImageRemoveFn: func(context.Context, string, client.ImageRemoveOptions) (client.ImageRemoveResult, error) {
			return client.ImageRemoveResult{}, nil
		},
		ImageBuildFn: func(context.Context, io.Reader, client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{}, nil
		},
	}

	res, err := f.ImageInspect(t.Context(), "ref")
	if err != nil || len(res.RepoDigests) != 1 {
		t.Errorf("ImageInspect = %v, %v, want the stubbed result", res.RepoDigests, err)
	}
	if _, err := f.ImagePull(t.Context(), "ref", client.ImagePullOptions{}); err != nil {
		t.Errorf("ImagePull = %v, want nil", err)
	}
	if _, err := f.ImagePull(t.Context(), "ref", client.ImagePullOptions{}); err != nil {
		t.Errorf("ImagePull = %v, want nil", err)
	}
	if _, err := f.DistributionInspect(t.Context(), "ref", client.DistributionInspectOptions{}); err != nil {
		t.Errorf("DistributionInspect = %v, want nil", err)
	}
	if _, err := f.ImageList(t.Context(), client.ImageListOptions{}); err != nil {
		t.Errorf("ImageList = %v, want nil", err)
	}
	if _, err := f.ImageRemove(t.Context(), "id", client.ImageRemoveOptions{}); err != nil {
		t.Errorf("ImageRemove = %v, want nil", err)
	}
	if _, err := f.ImageBuild(t.Context(), strings.NewReader(""), client.ImageBuildOptions{}); err != nil {
		t.Errorf("ImageBuild = %v, want nil", err)
	}

	for _, tc := range []struct {
		method string
		got    int
		want   int
	}{
		{"ImageInspect", f.ImageInspectCalls(), 1},
		{"ImagePull", f.ImagePullCalls(), 2},
		{"DistributionInspect", f.DistributionInspectCalls(), 1},
		{"ImageList", f.ImageListCalls(), 1},
		{"ImageRemove", f.ImageRemoveCalls(), 1},
		{"ImageBuild", f.ImageBuildCalls(), 1},
	} {
		if tc.got != tc.want {
			t.Errorf("%sCalls() = %d, want %d", tc.method, tc.got, tc.want)
		}
	}
}

// Fake must NOT satisfy client.APIClient. Embedding the SDK interface to avoid
// writing a method would make the Fake satisfy every narrow interface in the
// tree by accident, and the narrowing would buy nothing in tests — the whole
// point of it. The assertion is deliberately a negative one: nothing else
// catches an embed added later.
func TestFakeIsNotAnAPIClient(t *testing.T) {
	var v any = &Fake{}
	if _, ok := v.(client.APIClient); ok {
		t.Fatal("Fake satisfies client.APIClient — the SDK interface was embedded, which defeats every narrow interface")
	}
}

// InspectSeq answers one queued result per call and then reports the image
// missing, which is how a test tells the digest before a pull from the digest
// after it.
func TestInspectSeq(t *testing.T) {
	fn := InspectSeq(ImageInspectResult("repo", "sha256:one"), ImageInspectResult("repo", "sha256:two"))

	for _, want := range []string{"repo@sha256:one", "repo@sha256:two"} {
		res, err := fn(t.Context(), "ref")
		if err != nil {
			t.Fatalf("InspectSeq = %v, want a queued result", err)
		}
		if got := res.RepoDigests[0]; got != want {
			t.Errorf("digest = %q, want %q", got, want)
		}
	}
	if _, err := fn(t.Context(), "ref"); err == nil {
		t.Error("InspectSeq answered past the end of the queue, want a not-found error")
	}
}
