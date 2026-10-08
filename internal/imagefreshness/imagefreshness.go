// Package imagefreshness owns the handoff between synchronous image planning
// and the background freshness detector for one container session.
package imagefreshness

import (
	"context"

	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/imageplan"
	"github.com/filippolmt/toolbox/internal/imageprefetch"
	"github.com/filippolmt/toolbox/internal/sessionplan"
)

// imageSource is the Docker surface reached by the Image Plan and Image
// Prefetch implementations.
type imageSource interface {
	ImageInspect(context.Context, string, ...client.ImageInspectOption) (client.ImageInspectResult, error)
	DistributionInspect(context.Context, string, client.DistributionInspectOptions) (client.DistributionInspectResult, error)
	ImagePull(context.Context, string, client.ImagePullOptions) (client.ImagePullResponse, error)
}

var syncImage = func(ctx context.Context, cli imageSource, image sessionplan.Image, stateDir string, reason imageplan.Reason) imageplan.Outcome {
	return imageplan.Sync(ctx, cli, image, stateDir, reason)
}

// Input contains the session facts that remain stable across preparation and
// attachment.
type Input struct {
	Image         sessionplan.Image
	StateDir      string
	CLIVersion    string
	NoUpdateCheck bool
}

// StartKind is the container state observed before image preparation.
type StartKind int

const (
	// StartCreate means no container exists yet.
	StartCreate StartKind = iota
	// StartStopped means the existing container can be offered for replacement.
	StartStopped
	// StartRunning means the existing container must be kept.
	StartRunning
)

// Directive tells the container lifecycle how to honour the start-up
// settlement without exposing the freshness protocol behind it.
type Directive int

const (
	// Proceed keeps the lifecycle branch already selected by the container.
	Proceed Directive = iota
	// Postpone proceeds and records the declined refresh for idle reload.
	Postpone
	// Interrupt abandons the session start.
	Interrupt
	// Replace recreates the stopped container before attachment.
	Replace
)

// Session owns image freshness for one container session.
type Session struct {
	cli            imageSource
	in             Input
	startSynced    bool
	reloadPrepared bool
}

// New creates the freshness session for one container lifecycle invocation.
func New(cli imageSource, in Input) *Session {
	return &Session{cli: cli, in: in}
}

// PrepareReload refreshes and proves the base image before a reload may
// destroy its current container.
func (s *Session) PrepareReload(ctx context.Context) error {
	syncImage(ctx, s.cli, s.in.Image, s.in.StateDir, imageplan.ReasonReload)
	if err := imageplan.Ensure(ctx, s.cli, s.in.Image); err != nil {
		return err
	}
	s.reloadPrepared = true
	return nil
}

// PrepareStart settles the synchronous start-up refresh for the observed
// container state.
func (s *Session) PrepareStart(ctx context.Context, kind StartKind) Directive {
	if s.reloadPrepared || kind == StartRunning {
		return Proceed
	}
	reason := imageplan.ReasonCreate
	if kind == StartStopped {
		reason = imageplan.ReasonStart
	}
	outcome := syncImage(ctx, s.cli, s.in.Image, s.in.StateDir, reason)
	s.startSynced = outcome.Synced()
	switch outcome {
	case imageplan.OutcomeDeclined:
		return Postpone
	case imageplan.OutcomeInterrupted:
		return Interrupt
	case imageplan.OutcomeAccepted:
		if kind == StartStopped {
			return Replace
		}
	}
	return Proceed
}

// Ensure guarantees that image is present before container creation.
func (s *Session) Ensure(ctx context.Context, image sessionplan.Image) error {
	return imageplan.Ensure(ctx, s.cli, image)
}

// Replaced invalidates freshness state published about a container that no
// longer exists.
func (s *Session) Replaced() {
	imageprefetch.ClearResult(s.in.StateDir)
}

// Attach starts background freshness work for the lifetime of ctx and returns
// an idempotent stop function.
func (s *Session) Attach(ctx context.Context, containerDigest string) func() {
	attachCtx, stop := context.WithCancel(ctx)
	if s.in.Image.PullPolicy != config.PullNever && !s.in.NoUpdateCheck {
		imageprefetch.Start(attachCtx, s.cli, imageprefetch.Input{
			Ref:             s.in.Image.Ref,
			ContainerDigest: containerDigest,
			StateDir:        s.in.StateDir,
			StartSynced:     s.startSynced,
			CLIVersion:      s.in.CLIVersion,
		})
	}
	return stop
}
