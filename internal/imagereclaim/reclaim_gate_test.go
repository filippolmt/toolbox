//go:build dockergate

package imagereclaim_test

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/build"
)

// TestDaemonRefusesToRemoveAStoppedContainersImage pins the one claim ADR 0007
// rests on, and it is a claim about Docker rather than about this repo: that
// an unforced ImageRemove fails while a container that is merely *stopped*
// still references the image. Image Reclamation performs no in-use check of
// its own precisely because the daemon performs this one atomically, on the
// far side of the race a ContainerList census would sit on the wrong side of —
// so without this test the ADR asserts something nobody has checked. A fake
// client cannot testify to it: it would only confirm that the fake does what
// we told it.
//
// The subject is a throwaway image built FROM the image under test rather than
// that image itself, so the refusal has exactly one possible cause. The final
// removal, once the container is gone, is what makes that attribution: the same
// unforced call on the same image now succeeds.
func TestDaemonRefusesToRemoveAStoppedContainersImage(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cli, err := client.New(client.FromEnv)
	if err != nil {
		t.Fatalf("docker client: %v", err)
	}
	defer cli.Close()

	subject := buildSubject(ctx, t, cli)

	created, err := cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{Image: subject, Cmd: []string{"/bin/sh", "-c", "exit 0"}},
		Name:   "toolbox-reclaim-gate-" + strconv.FormatInt(time.Now().UnixNano(), 36),
	})
	if err != nil {
		t.Fatalf("ContainerCreate: %v", err)
	}
	holder := created.ID
	removed := false
	t.Cleanup(func() {
		if !removed {
			_, _ = cli.ContainerRemove(context.Background(), holder, client.ContainerRemoveOptions{Force: true})
		}
		_, _ = cli.ImageRemove(context.Background(), subject, client.ImageRemoveOptions{Force: true})
	})

	// Created and never started is the sharpest form of "merely stopped": no
	// process ever ran, and the daemon must still treat the reference as real.
	// It is also the state a toolbox container of another workspace sits in
	// while its developer is away, which is the case the ADR is about.
	if _, err := cli.ImageRemove(ctx, subject, client.ImageRemoveOptions{}); err == nil {
		t.Fatal("the daemon removed an image a stopped container references — Image Reclamation's only in-use check does not hold")
	}

	if _, err := cli.ContainerRemove(ctx, holder, client.ContainerRemoveOptions{}); err != nil {
		t.Fatalf("ContainerRemove: %v", err)
	}
	removed = true

	if _, err := cli.ImageRemove(ctx, subject, client.ImageRemoveOptions{}); err != nil {
		t.Fatalf("the refusal above was not the container after all: unforced ImageRemove still fails with no holder: %v", err)
	}
}

// buildSubject builds a one-instruction image on top of IMAGE_TAG and returns
// its tag. The LABEL is load-bearing: a bare `FROM` adds no layer and would
// resolve to the base image's own ID, which would make the test a statement
// about the image under test instead of about a throwaway.
func buildSubject(ctx context.Context, t *testing.T, cli client.APIClient) string {
	t.Helper()
	base := imageTag(t)
	inspect, err := cli.ImageInspect(ctx, base)
	if err != nil {
		t.Fatalf("ImageInspect(%q): %v", base, err)
	}
	if inspect.ID == "" {
		t.Fatalf("the base image %q reports no ID", base)
	}
	tag := "toolbox-reclaim-gate:" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if err := build.BuildOverlay(ctx, cli, inspect.ID, []byte("LABEL toolbox.gate=reclaim\n"), tag); err != nil {
		t.Fatalf("BuildOverlay: %v", err)
	}
	return tag
}

// imageTag is the image under test, handed to the gate the same way the other
// real-daemon gates receive it, so the canonical ref lives only in
// internal/build.
func imageTag(t *testing.T) string {
	t.Helper()
	if tag := os.Getenv("IMAGE_TAG"); tag != "" {
		return tag
	}
	return build.DefaultRegistryImage
}
