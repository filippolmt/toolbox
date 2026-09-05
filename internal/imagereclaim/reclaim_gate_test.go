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
	"github.com/filippolmt/toolbox/internal/imageref"
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
		// The image's own ENTRYPOINT is a full shell start that never exits, so
		// it is overridden with something that returns immediately — this
		// container exists to hold a reference, not to run a session.
		Config: &container.Config{Image: subject, Entrypoint: []string{"/bin/true"}},
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

	// Run to completion first, so the container is genuinely `exited` rather
	// than `created`. The distinction is the whole point: `created` would only
	// show that an unstarted record counts, while the consequence the ADR is
	// written about — "A stopped container pins its image indefinitely" — is
	// about a container whose developer ran a shell in it and walked away.
	runToExit(ctx, t, cli, holder)

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

// runToExit starts the container and waits for it to stop, then asserts the
// daemon agrees it is in `exited` — the state under test, established rather
// than assumed.
func runToExit(ctx context.Context, t *testing.T, cli client.APIClient, id string) {
	t.Helper()
	// Start first, then wait — the reverse of the usual "subscribe before the
	// event" rule, and for a reason: `created` already satisfies
	// WaitConditionNotRunning, so a wait registered beforehand is answered
	// instantly and proves nothing. Registering it after the start is safe
	// here because the daemon answers a wait on an already-exited container
	// from its record, so a process that has finished by now is not missed.
	if _, err := cli.ContainerStart(ctx, id, client.ContainerStartOptions{}); err != nil {
		t.Fatalf("ContainerStart: %v", err)
	}
	wait := cli.ContainerWait(ctx, id, client.ContainerWaitOptions{Condition: container.WaitConditionNotRunning})
	select {
	case err := <-wait.Error:
		t.Fatalf("ContainerWait: %v", err)
	case <-wait.Result:
	case <-ctx.Done():
		t.Fatalf("the container never stopped: %v", ctx.Err())
	}

	res, err := cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatalf("ContainerInspect: %v", err)
	}
	if state := res.Container.State; state == nil || state.Status != "exited" {
		t.Fatalf("container status = %+v, want exited — the gate would pin the wrong state", state)
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
// internal/imageref.
func imageTag(t *testing.T) string {
	t.Helper()
	if tag := os.Getenv("IMAGE_TAG"); tag != "" {
		return tag
	}
	return imageref.DefaultRegistryImage
}
