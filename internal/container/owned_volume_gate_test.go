//go:build dockergate

package container

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/imageref"
	"github.com/filippolmt/toolbox/internal/sessionplan"
)

// The initialiser's exit status is read only once it has run: a wait that
// answers before the start reports success for an init that never ran, and
// the session then binds a root-owned volume. A target with a space makes the
// unquoted chown fail, so only an init that really ran can report it.
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

	if err := ensureOwnedVolume(ctx, cli, sessionplan.Image{Ref: image}, v); err == nil {
		t.Fatal("ensureOwnedVolume succeeded, but its initialiser's chown cannot: the exit status was read before the init ran")
	}
}
