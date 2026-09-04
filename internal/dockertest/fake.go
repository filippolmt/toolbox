package dockertest

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/moby/moby/client"
)

// Fake is the daemon double the image packages drive their narrow interfaces
// with: one function field per method those interfaces declare, and nothing
// else. It satisfies each of them structurally, so a package whose interface
// is unexported still takes this value without naming a type.
//
// A nil field panics naming the method. That is the behaviour the hand-rolled
// adapters got from embedding a nil client.APIClient, and it is load-bearing:
// several of the sharpest assertions in the image family are assertions of
// *absence* — the registry is asked nothing while the attempt stamp is fresh,
// ImageRemove takes neither force nor PruneChildren — and they are expressed
// as "no stub, therefore no call". The panic keeps them, with a message that
// names the method instead of a nil-pointer dereference.
//
// Fake must not embed client.APIClient. Embedding it to avoid writing a method
// would satisfy every narrow interface in the tree by accident and undo,
// silently, the only thing the narrowing buys in tests.
// TestFakeIsNotAnAPIClient is the guard.
//
// The option arguments the SDK carries are dropped from the fields the tests
// never assert on, and kept on the two they do (ImageRemove's flags, the
// ImageBuild context and options).
type Fake struct {
	ImageInspectFn        func(ctx context.Context, ref string) (client.ImageInspectResult, error)
	ImagePullFn           func(ctx context.Context, ref string) (client.ImagePullResponse, error)
	DistributionInspectFn func(ctx context.Context, ref string) (client.DistributionInspectResult, error)
	ImageListFn           func(ctx context.Context) (client.ImageListResult, error)
	ImageRemoveFn         func(ctx context.Context, id string, opts client.ImageRemoveOptions) (client.ImageRemoveResult, error)
	ImageBuildFn          func(ctx context.Context, buildContext io.Reader, opts client.ImageBuildOptions) (client.ImageBuildResult, error)

	// mu guards the counters alone: a poll runs on its own goroutine, and a
	// test that reads a count while one is in flight would otherwise race.
	// What a stub itself records is the stub's own business.
	mu    sync.Mutex
	calls map[string]int
}

// record counts one call to method and reports whether the caller supplied a
// stub for it, panicking when it did not.
func (f *Fake) record(method string, stubbed bool) {
	f.mu.Lock()
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[method]++
	f.mu.Unlock()
	if !stubbed {
		panic("dockertest: unexpected " + method + " call — no " + method + "Fn set on the Fake")
	}
}

// count reports how many times method was called, stub or not.
func (f *Fake) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[method]
}

func (f *Fake) ImageInspect(ctx context.Context, ref string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
	f.record("ImageInspect", f.ImageInspectFn != nil)
	return f.ImageInspectFn(ctx, ref)
}

func (f *Fake) ImagePull(ctx context.Context, ref string, _ client.ImagePullOptions) (client.ImagePullResponse, error) {
	f.record("ImagePull", f.ImagePullFn != nil)
	return f.ImagePullFn(ctx, ref)
}

func (f *Fake) DistributionInspect(ctx context.Context, ref string, _ client.DistributionInspectOptions) (client.DistributionInspectResult, error) {
	f.record("DistributionInspect", f.DistributionInspectFn != nil)
	return f.DistributionInspectFn(ctx, ref)
}

func (f *Fake) ImageList(ctx context.Context, _ client.ImageListOptions) (client.ImageListResult, error) {
	f.record("ImageList", f.ImageListFn != nil)
	return f.ImageListFn(ctx)
}

func (f *Fake) ImageRemove(ctx context.Context, id string, opts client.ImageRemoveOptions) (client.ImageRemoveResult, error) {
	f.record("ImageRemove", f.ImageRemoveFn != nil)
	return f.ImageRemoveFn(ctx, id, opts)
}

func (f *Fake) ImageBuild(ctx context.Context, buildContext io.Reader, opts client.ImageBuildOptions) (client.ImageBuildResult, error) {
	f.record("ImageBuild", f.ImageBuildFn != nil)
	return f.ImageBuildFn(ctx, buildContext, opts)
}

// The call counters. Separate methods rather than one string-keyed accessor:
// a mistyped method name is then a compile error rather than a count of zero
// that quietly passes.

func (f *Fake) ImageInspectCalls() int        { return f.count("ImageInspect") }
func (f *Fake) ImagePullCalls() int           { return f.count("ImagePull") }
func (f *Fake) DistributionInspectCalls() int { return f.count("DistributionInspect") }
func (f *Fake) ImageListCalls() int           { return f.count("ImageList") }
func (f *Fake) ImageRemoveCalls() int         { return f.count("ImageRemove") }
func (f *Fake) ImageBuildCalls() int          { return f.count("ImageBuild") }

// InspectSeq builds an ImageInspectFn that answers one queued result per call
// and then reports the image missing. Shared because two packages need the
// same thing for the same reason: a pull changes what the store holds, so a
// test that asserts on the digest after a fetch has to distinguish the two
// inspects, and the end of the queue is the store that no longer answers.
// The queue position is guarded: a poller reissued on its own goroutine can be
// inside an inspect while the next one starts.
func InspectSeq(res ...client.ImageInspectResult) func(context.Context, string) (client.ImageInspectResult, error) {
	var mu sync.Mutex
	n := 0
	return func(context.Context, string) (client.ImageInspectResult, error) {
		mu.Lock()
		defer mu.Unlock()
		if n >= len(res) {
			return client.ImageInspectResult{}, errors.New("no such image")
		}
		n++
		return res[n-1], nil
	}
}
