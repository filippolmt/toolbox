package build

import (
	"context"
	"errors"
	"testing"

	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/dockertest"
)

// inspectClient answers ImageInspect and nothing else. The embedded nil
// APIClient turns any other call into a panic, so a helper that grew a second
// daemon round trip fails loudly instead of passing quietly.
type inspectClient struct {
	client.APIClient
	res client.ImageInspectResult
	err error
}

func (c inspectClient) ImageInspect(context.Context, string, ...client.ImageInspectOption) (client.ImageInspectResult, error) {
	return c.res, c.err
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
		cli    inspectClient
		want   string
		wantOk bool
	}{
		{
			name:   "pulled image carries its repo digest",
			cli:    inspectClient{res: dockertest.ImageInspectResult("ghcr.io/filippolmt/toolbox", digest)},
			want:   digest,
			wantOk: true,
		},
		{
			name:   "locally built image answers with no digest",
			cli:    inspectClient{res: client.ImageInspectResult{}},
			wantOk: true,
		},
		{
			name: "absent image does not answer at all",
			cli:  inspectClient{err: errors.New("no such image")},
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
