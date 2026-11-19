//go:build dockergate

package container

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/imageref"
	"github.com/filippolmt/toolbox/internal/sessionplan"
)

// The initialiser's exit status is read only once it has run: a wait that
// answers before the start reports success for an init that never ran, and
// the session then binds a root-owned volume. The failing init leans on
// initOwnedVolume leaving target unquoted in its sh line: a target with a
// space makes the chown exit 1. Quote it there, and this needs another init
// that fails once it runs.
func TestOwnedVolumeInitReportsAnInitThatFails(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cli, err := NewClient()
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { _ = cli.Close() })

	image := os.Getenv("IMAGE_TAG")
	if image == "" {
		image = imageref.DefaultRegistryImage
	}
	volume := fmt.Sprintf("toolbox-owned-gate-%x", time.Now().UnixNano())
	v := ownedVolume{name: volume, target: "/owned gate", initName: volume + "_init", label: "owned-volume gate"}
	t.Cleanup(func() {
		_, _ = cli.VolumeRemove(context.Background(), volume, client.VolumeRemoveOptions{Force: true})
	})

	err = ensureOwnedVolume(ctx, cli, sessionplan.Image{Ref: image}, v)
	if err == nil || !strings.Contains(err.Error(), "initialiser exited") {
		t.Fatalf("ensureOwnedVolume = %v, want the initialiser's failed chown reported: a nil error means the exit status was read before the init ran", err)
	}
	if _, err := cli.VolumeInspect(ctx, volume, client.VolumeInspectOptions{}); !cerrdefs.IsNotFound(err) {
		t.Errorf("VolumeInspect after a failed init = %v, want the volume rolled back", err)
	}
}
