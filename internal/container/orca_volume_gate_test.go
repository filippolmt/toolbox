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
	"github.com/filippolmt/toolbox/internal/mountplan"
	"github.com/filippolmt/toolbox/internal/sessionplan"
)

// The initialised Orca data volume takes what Orca does in it: bind a Unix
// socket and chmod it, as the session user. The step that fails on a Host
// Share bind (see peerSocketProbe); on a Linux daemon this pins the volume's
// ownership, not the Host Share itself.
func TestOrcaDataVolumeHoldsASocketTheSessionUserChmods(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
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
	suffix := fmt.Sprintf("%x", time.Now().UnixNano())
	name := "toolbox-orca-gate-" + suffix
	volume := "toolbox-orca-gate-data-" + suffix
	plan := &sessionplan.SessionPlan{
		Image:          sessionplan.Image{Ref: image},
		Binds:          []mountplan.Bind{{Source: volume, Target: sessionplan.OrcaDataTarget, Mode: "rw"}},
		ContainerName:  name,
		Hostname:       name,
		Cmd:            []string{"sleep", "infinity"},
		DataVolume:     sessionplan.DataVolume{Name: volume, Init: volume + "_init", Target: sessionplan.OrcaDataTarget, Label: "Orca data volume"},
	}

	id, err := createAndStart(ctx, cli, plan, plan.Image, plan.Detached)
	if err != nil {
		_, _ = cli.VolumeRemove(context.Background(), volume, client.VolumeRemoveOptions{Force: true})
		t.Fatalf("createAndStart: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := removeAndWait(cleanupCtx, cli, name, "orca volume gate cleanup"); err != nil {
			t.Errorf("cleanup container: %v", err)
		}
		if _, err := cli.VolumeRemove(cleanupCtx, volume, client.VolumeRemoveOptions{}); err != nil {
			t.Errorf("cleanup volume: %v", err)
		}
	})

	sock := sessionplan.OrcaDataTarget + "/daemon/probe.sock"
	if out, code := dockerExec(ctx, t, cli, id, "sh", "-c", "mkdir -p "+sessionplan.OrcaDataTarget+"/daemon && python3 -c \"$0\" "+sock, peerSocketProbe); code != 0 {
		t.Fatalf("could not bind and chmod a socket in the Orca data volume (exit %d): %s", code, out)
	}
	if perm, _ := dockerExec(ctx, t, cli, id, "sh", "-c", "stat -c '%a %u:%g' "+sessionplan.OrcaDataTarget+"; echo \" $(id -u):$(id -g)\""); !orcaDataOwned(perm) {
		t.Errorf("%s is %q, want mode 700 owned by the session user", sessionplan.OrcaDataTarget, perm)
	}
}

// orcaDataOwned reads "700 <uid:gid>\n <uid:gid>": mode 700, owner the session user.
func orcaDataOwned(out string) bool {
	var mode, owner, user string
	_, err := fmt.Sscan(out, &mode, &owner, &user)
	return err == nil && mode == "700" && owner == user
}
