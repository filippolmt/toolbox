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

func TestNpxExecTmpfsRunsInstalledBinsAsTheSessionUser(t *testing.T) {
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
	name := "toolbox-npx-gate-" + suffix
	volume := "toolbox-npx-gate-cache-" + suffix
	plan := &sessionplan.SessionPlan{
		Image:         sessionplan.Image{Ref: image},
		Binds:         []mountplan.Bind{{Source: volume, Target: "/home/toolbox/.npm", Mode: "rw"}},
		ContainerName: name,
		Hostname:      name,
		Cmd:           []string{"sleep", "infinity"},
	}

	id, err := createAndStart(ctx, cli, plan, plan.Image)
	if err != nil {
		_, _ = cli.VolumeRemove(context.Background(), volume, client.VolumeRemoveOptions{})
		t.Fatalf("createAndStart: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := removeAndWait(cleanupCtx, cli, name, "npx tmpfs gate cleanup"); err != nil {
			t.Errorf("cleanup container: %v", err)
		}
		if _, err := cli.VolumeRemove(cleanupCtx, volume, client.VolumeRemoveOptions{}); err != nil {
			t.Errorf("cleanup volume: %v", err)
		}
	})

	const probe = `
set -eu
dir=/home/toolbox/.npm/_npx
test "$(stat -f -c %T "$dir")" = tmpfs
test "$(stat -c %u:%g "$dir")" = "$(id -u):$(id -g)"
printf '#!/bin/sh\nprintf executable\n' > "$dir/probe"
chmod 700 "$dir/probe"
"$dir/probe"
`
	if out, code := dockerExec(ctx, t, cli, id, "sh", "-c", probe); code != 0 || out != "executable" {
		t.Fatalf("npm _npx tmpfs probe exited %d with %q, want executable", code, out)
	}
}
