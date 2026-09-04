package build

import (
	"context"
	"errors"
	"testing"

	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/dockertest"
)

// inspecting builds a store answering every inspect with res and err. The
// shared fake stubs nothing else, so a helper that grew a second daemon round
// trip panics on the method it reached for instead of passing quietly.
func inspecting(res client.ImageInspectResult, err error) *dockertest.Fake {
	return &dockertest.Fake{
		ImageInspectFn: func(context.Context, string) (client.ImageInspectResult, error) {
			return res, err
		},
	}
}

// TestLocalRepoDigest pins the distinction four call sites depend on: a store
// that did not answer is not the same as a store carrying no digest. The
// update prefetch abstains on the second, while the container's own digest
// record is rewritten to whatever the store says — empty included — so
// collapsing the two would either silence the prefetch or stamp a stale
// digest into a fresh container.
func TestLocalRepoDigest(t *testing.T) {
	const ref = "ghcr.io/filippolmt/toolbox:latest"
	const digest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"

	tests := []struct {
		name   string
		cli    *dockertest.Fake
		want   string
		wantOk bool
	}{
		{
			name:   "pulled image carries its repo digest",
			cli:    inspecting(dockertest.ImageInspectResult("ghcr.io/filippolmt/toolbox", digest), nil),
			want:   digest,
			wantOk: true,
		},
		{
			name:   "locally built image answers with no digest",
			cli:    inspecting(client.ImageInspectResult{}, nil),
			wantOk: true,
		},
		{
			name: "absent image does not answer at all",
			cli:  inspecting(client.ImageInspectResult{}, errors.New("no such image")),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := LocalRepoDigest(t.Context(), tc.cli, ref)
			if got != tc.want || ok != tc.wantOk {
				t.Errorf("LocalRepoDigest = (%q, %v), want (%q, %v)", got, ok, tc.want, tc.wantOk)
			}
		})
	}
}
