package imagefreshness_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/dockertest"
	"github.com/filippolmt/toolbox/internal/imagefreshness"
	"github.com/filippolmt/toolbox/internal/sessionplan"
)

const (
	testRepo   = "ghcr.io/example/toolbox"
	testRef    = testRepo + ":latest"
	testDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

func TestReplacedInvalidatesPublishedFreshness(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"update-check", "update-check.shown", "update-check.stamp"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("stale"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	imagefreshness.New(&dockertest.Fake{}, imagefreshness.Input{StateDir: dir}).Replaced()

	for _, name := range []string{"update-check", "update-check.shown", "update-check.stamp"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("%s survived Replaced(): %v", name, err)
		}
	}
}

func TestPrepareStartDoesNotRepeatAReloadPreparation(t *testing.T) {
	fake := &dockertest.Fake{
		ImagePullFn: func(context.Context, string) (client.ImagePullResponse, error) {
			return dockertest.PullResponse{ReadCloser: io.NopCloser(strings.NewReader(""))}, nil
		},
		ImageInspectFn: func(context.Context, string) (client.ImageInspectResult, error) {
			return dockertest.ImageInspectResult(testRepo, testDigest), nil
		},
	}
	session := imagefreshness.New(fake, imagefreshness.Input{
		Image: sessionplan.Image{Ref: testRef, PullPolicy: config.PullAlways},
	})

	if err := session.PrepareReload(t.Context()); err != nil {
		t.Fatalf("PrepareReload() error: %v", err)
	}
	if got := session.PrepareStart(t.Context(), imagefreshness.StartCreate); got != imagefreshness.Proceed {
		t.Fatalf("PrepareStart() = %v, want Proceed", got)
	}
	if got := fake.ImagePullCalls(); got != 1 {
		t.Errorf("ImagePull calls = %d, want 1: PrepareStart repeated the reload sync", got)
	}
}

func TestAttachStillProbesAfterReloadPreparation(t *testing.T) {
	dir := t.TempDir()
	fake := &dockertest.Fake{
		ImagePullFn: func(context.Context, string) (client.ImagePullResponse, error) {
			return dockertest.PullResponse{ReadCloser: io.NopCloser(strings.NewReader(""))}, nil
		},
		ImageInspectFn: func(context.Context, string) (client.ImageInspectResult, error) {
			return dockertest.ImageInspectResult(testRepo, testDigest), nil
		},
		DistributionInspectFn: func(context.Context, string) (client.DistributionInspectResult, error) {
			return dockertest.DistributionResult(testDigest), nil
		},
	}
	session := imagefreshness.New(fake, imagefreshness.Input{
		Image:      sessionplan.Image{Ref: testRef, PullPolicy: config.PullAlways},
		StateDir:   dir,
		CLIVersion: "dev",
	})
	if err := session.PrepareReload(t.Context()); err != nil {
		t.Fatalf("PrepareReload() error: %v", err)
	}

	stop := session.Attach(t.Context(), testDigest)
	t.Cleanup(stop)
	deadline := time.Now().Add(time.Second)
	for fake.DistributionInspectCalls() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("Attach made no probe: reload preparation must not claim the start-up probe turn")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestAttachHonoursRegistryCommunicationRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   imagefreshness.Input
	}{
		{
			name: "pull never",
			in: imagefreshness.Input{
				Image:    sessionplan.Image{Ref: testRef, PullPolicy: config.PullNever},
				StateDir: t.TempDir(),
			},
		},
		{
			name: "update checks disabled",
			in: imagefreshness.Input{
				Image:         sessionplan.Image{Ref: testRef},
				StateDir:      t.TempDir(),
				NoUpdateCheck: true,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stop := imagefreshness.New(&dockertest.Fake{}, tc.in).Attach(t.Context(), testDigest)
			stop()
		})
	}
}

func TestAttachReusesTheStartSync(t *testing.T) {
	dir := t.TempDir()
	fake := &dockertest.Fake{
		ImagePullFn: func(context.Context, string) (client.ImagePullResponse, error) {
			return dockertest.PullResponse{ReadCloser: io.NopCloser(strings.NewReader(""))}, nil
		},
		ImageInspectFn: func(context.Context, string) (client.ImageInspectResult, error) {
			return dockertest.ImageInspectResult(testRepo, testDigest), nil
		},
	}
	session := imagefreshness.New(fake, imagefreshness.Input{
		Image:      sessionplan.Image{Ref: testRef, PullPolicy: config.PullAlways},
		StateDir:   dir,
		CLIVersion: "dev",
	})

	if got := session.PrepareStart(t.Context(), imagefreshness.StartCreate); got != imagefreshness.Proceed {
		t.Fatalf("PrepareStart() = %v, want Proceed", got)
	}
	ctx, cancel := context.WithCancel(t.Context())
	stop := session.Attach(ctx, testDigest)
	t.Cleanup(func() {
		stop()
		cancel()
	})

	stamp := filepath.Join(dir, "update-check.stamp")
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(stamp); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Attach left no attempt stamp for the sync already performed by PrepareStart")
		}
		time.Sleep(time.Millisecond)
	}
	if got := fake.DistributionInspectCalls(); got != 0 {
		t.Errorf("DistributionInspect calls = %d, want 0: PrepareStart already synchronized the store", got)
	}
}
