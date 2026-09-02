package container

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/dockertest"
	"github.com/filippolmt/toolbox/internal/imageplan"
	"github.com/filippolmt/toolbox/internal/reload"
	"github.com/filippolmt/toolbox/internal/sessionplan"
)

// stubExecShellWritingMarker drives the reload branch the way the real shell
// does: the marker appears while the exec session is attached, and is there by
// the time execShell returns. Nothing else in the test has to know the path.
func stubExecShellWritingMarker(t *testing.T, plan *sessionplan.SessionPlan, cwd string) {
	t.Helper()
	orig := execShellFn
	execShellFn = func(context.Context, client.APIClient, string, []string) error {
		return reload.WriteMarker(plan.ReloadMarkerPath(), cwd)
	}
	t.Cleanup(func() { execShellFn = orig })
}

// runningContainerMock is the connect path with nothing else stubbed: the
// container is up, so Shell attaches and the reload branch is the only thing
// under test.
func runningContainerMock() *mockClient {
	return &mockClient{
		inspectFn: func(context.Context, string) (container.InspectResponse, error) {
			return container.InspectResponse{ID: "abc123", State: &container.State{Running: true}}, nil
		},
	}
}

// The highest-value assertion on the reload path, and the one whose regression
// is silent: Shell's teardown defer must not fire when the shell asked for a
// reload. Firing destroys the container before the next binary has proved it
// has a usable image to move onto — which voids the whole safety gate and
// leaves a failed re-exec with nothing to go back to.
func TestShellReloadSuppressesTeardown(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	plan := testPlan(t, testWorkspace(t), nil)
	stubExecShellWritingMarker(t, plan, "")
	mock := runningContainerMock()

	rl, err := Shell(context.Background(), mock, plan)
	if err != nil {
		t.Fatalf("Shell(): %v", err)
	}
	if rl == nil {
		t.Fatal("Shell() reported no reload after the shell wrote a marker")
	}
	for _, call := range mock.calls {
		if call == "ContainerRemove" || call == "ContainerKill" {
			t.Fatalf("teardown reached the daemon on the reload path: %v", mock.calls)
		}
	}
}

// The handover is composed where the facts are still available. The container
// name is the load-bearing field — the next process cannot safely recompute it
// — and the "before" pair is what turns a summary into evidence.
func TestShellReloadComposesTheHandover(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	plan := testPlan(t, testWorkspace(t), nil)
	plan.Env = append(plan.Env, sessionplan.ImageDigestEnv+"=sha256:old", sessionplan.CLIVersionEnv+"=v0.1.0")
	stubExecShellWritingMarker(t, plan, "/workspace/sub")

	rl, err := Shell(context.Background(), runningContainerMock(), plan)
	if err != nil {
		t.Fatalf("Shell(): %v", err)
	}
	want := reload.From{
		Container:   plan.ContainerName,
		Cwd:         "/workspace/sub",
		ImageDigest: "sha256:old",
		CLIVersion:  "v0.1.0",
	}
	if rl == nil || !reflect.DeepEqual(*rl, want) {
		t.Errorf("handover = %+v, want %+v", rl, want)
	}
}

// An ordinary exit must stay an ordinary exit: no marker, no reload, and the
// teardown the container has always had.
func TestShellWithoutMarkerTearsDownAsBefore(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, restore := stubExecShell()
	defer restore()

	mock := runningContainerMock()
	rl, err := Shell(context.Background(), mock, testPlan(t, testWorkspace(t), nil))
	if err != nil {
		t.Fatalf("Shell(): %v", err)
	}
	if rl != nil {
		t.Fatalf("Shell() reported a reload nobody asked for: %+v", rl)
	}
	if !slices.Contains(mock.calls, "ContainerRemove") {
		t.Errorf("no teardown on the ordinary exit path: %v", mock.calls)
	}
}

// reloadPlan is a session that arrives holding a container to replace.
func reloadPlan(t *testing.T, oldContainer string) *sessionplan.SessionPlan {
	t.Helper()
	plan := testPlan(t, testWorkspace(t), nil)
	plan.ReloadFrom = &reload.From{Container: oldContainer, ImageDigest: "sha256:old", CLIVersion: "v0.1.0"}
	return plan
}

// createAfterReloadMock answers the sequence a reload sees: the image is in
// the local store, the container is gone once the reload removed it, and the
// create succeeds.
func createAfterReloadMock() *mockClient {
	removed := false
	m := &mockClient{
		imgInspFn: func(context.Context, string) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, nil
		},
		createFn: func(context.Context, *container.Config, *container.HostConfig, string) (container.CreateResponse, error) {
			return container.CreateResponse{ID: "new"}, nil
		},
	}
	m.inspectFn = func(context.Context, string) (container.InspectResponse, error) {
		if removed {
			return container.InspectResponse{}, &dockertest.NotFoundError{Msg: "no such container"}
		}
		return container.InspectResponse{ID: "old", State: &container.State{Running: true}}, nil
	}
	m.removeFn = func(context.Context, string, client.ContainerRemoveOptions) error {
		removed = true
		return nil
	}
	return m
}

// The ordering is the contract, not the call set: prove the image is present,
// destroy, wait for the name to be free, only then create. Getting it wrong
// fails in the direction that costs a session — a teardown before the verify
// leaves a developer with neither the old container nor a new one.
func TestShellReloadVerifiesBeforeItDestroys(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, restore := stubExecShell()
	defer restore()

	mock := createAfterReloadMock()
	if _, err := Shell(context.Background(), mock, reloadPlan(t, "toolbox-old-1234abcd")); err != nil {
		t.Fatalf("Shell(): %v", err)
	}

	order := func(call string) int { return slices.Index(mock.calls, call) }
	for _, step := range []string{"ImageInspect", "ContainerWait", "ContainerRemove", "ContainerCreate"} {
		if order(step) < 0 {
			t.Fatalf("%s never reached the daemon: %v", step, mock.calls)
		}
	}
	if order("ImageInspect") > order("ContainerRemove") {
		t.Errorf("the image was not verified before the teardown: %v", mock.calls)
	}
	if order("ContainerWait") > order("ContainerRemove") {
		t.Errorf("the removal wait was subscribed after the removal: %v", mock.calls)
	}
	if order("ContainerRemove") > order("ContainerCreate") {
		t.Errorf("the create raced the old container's name: %v", mock.calls)
	}
}

// The contract that makes a reload safe to type at any moment: no usable image
// is not a failed reload, it is a no-op that leaves the session alive. The
// test is written against the observable half — nothing was destroyed.
func TestShellReloadWithNoUsableImageDestroysNothing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, restore := stubExecShell()
	defer restore()

	mock := createAfterReloadMock()
	mock.imgInspFn = func(context.Context, string) (client.ImageInspectResult, error) {
		return client.ImageInspectResult{}, errors.New("no such image")
	}

	_, err := Shell(context.Background(), mock, reloadPlan(t, "toolbox-old-1234abcd"))
	if err == nil {
		t.Fatal("Shell() accepted a reload with no image in the local store")
	}
	if slices.Contains(mock.calls, "ContainerRemove") {
		t.Errorf("a refused reload still destroyed the container: %v", mock.calls)
	}
}

// The exact trap #837 names: teardown.OnShellExit declines to destroy while a
// sibling pane is attached, and reusing it here would spare a container whose
// name the create is about to ask for — turning a considerate refusal into a
// name collision. So the reload's teardown must ignore running execs.
func TestShellReloadKillsThroughAnAttachedSibling(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, restore := stubExecShell()
	defer restore()

	mock := createAfterReloadMock()
	inner := mock.inspectFn
	mock.inspectFn = func(ctx context.Context, id string) (container.InspectResponse, error) {
		res, err := inner(ctx, id)
		if err == nil {
			res.ExecIDs = []string{"sibling-pane"}
		}
		return res, err
	}
	mock.execInspectFn = func(context.Context, string) (client.ExecInspectResult, error) {
		return client.ExecInspectResult{Running: true}, nil
	}

	if _, err := Shell(context.Background(), mock, reloadPlan(t, "toolbox-old-1234abcd")); err != nil {
		t.Fatalf("Shell(): %v", err)
	}
	if !slices.Contains(mock.calls, "ContainerRemove") {
		t.Errorf("the reload spared a container with an attached sibling: %v", mock.calls)
	}
}

// The banner cache describes the container the reload just retired. Deleted,
// never rewritten with the digest landed on: the state mount is shared, so a
// rewrite would tell a sibling session still on the old image that it is up to
// date.
func TestShellReloadClearsTheBannerCache(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, restore := stubExecShell()
	defer restore()

	plan := reloadPlan(t, "toolbox-old-1234abcd")
	stale := []string{
		filepath.Join(plan.StateDir, "update-check"),
		filepath.Join(plan.StateDir, "update-check.shown"),
		// The attempt stamp goes too: it gates the next probe behind up to a
		// full cadence, and the reload has just invalidated the answer that
		// cadence was throttling.
		filepath.Join(plan.StateDir, "update-check.stamp"),
	}
	if err := os.MkdirAll(plan.StateDir, 0o755); err != nil {
		t.Fatalf("seed state dir: %v", err)
	}
	for _, f := range stale {
		if err := os.WriteFile(f, []byte("image_update=1\n"), 0o644); err != nil {
			t.Fatalf("seed %s: %v", f, err)
		}
	}

	if _, err := Shell(context.Background(), createAfterReloadMock(), plan); err != nil {
		t.Fatalf("Shell(): %v", err)
	}
	for _, f := range stale {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Errorf("%s survived the reload (stat = %v)", filepath.Base(f), err)
		}
	}
}

// TestFilterCasualties pins the deny-list's one subtlety. Everything else it
// drops is fixed infrastructure; the session shell is not, because the
// container's idle main process and a sibling attached pane run the identical
// command line. Dropping every match hides the pane — the loud loss the
// developer most needs to see — and dropping none reports the idle shell on
// every single reload.
func TestFilterCasualties(t *testing.T) {
	titles := []string{"UID", "PID", "PPID", "C", "STIME", "TTY", "TIME", "CMD"}
	row := func(cmd string) []string { return []string{"1000", "1", "0", "0", "10:00", "?", "00:00:00", cmd} }

	cases := []struct {
		name       string
		processes  [][]string
		sessionCmd []string
		want       []string
	}{
		{
			name: "baseline alone reports nothing",
			processes: [][]string{
				row("/usr/bin/tini -g -- /usr/local/bin/entrypoint"),
				row("/bin/zsh"),
				row("/bin/sh /usr/local/bin/proximo-hosts --watch"),
				row("/usr/local/bin/proximo-hosts --watch"),
				row("docker events --filter type=container"),
				row("socat TCP-LISTEN:8976,fork TCP:127.0.0.1:8976"),
			},
			sessionCmd: []string{"/bin/zsh"},
			want:       nil,
		},
		{
			name: "a sibling pane is the second shell, and it is reported",
			processes: [][]string{
				row("/bin/zsh"),
				row("/bin/zsh"),
			},
			sessionCmd: []string{"/bin/zsh"},
			want:       []string{"/bin/zsh"},
		},
		{
			name: "the forgotten job is what the look is for",
			processes: [][]string{
				row("/usr/bin/tini -g -- /usr/local/bin/entrypoint"),
				row("/bin/zsh"),
				row("claude"),
				row("npm run dev"),
			},
			sessionCmd: []string{"/bin/zsh"},
			want:       []string{"claude", "npm run dev"},
		},
		{
			name: "identical lines collapse to one, carrying their count",
			processes: [][]string{
				row("/bin/zsh"), row("/bin/zsh"), row("/bin/zsh"),
				row("npm run dev"),
			},
			sessionCmd: []string{"/bin/zsh"},
			want:       []string{"/bin/zsh (×2)", "npm run dev"},
		},
		{
			// Arithmetic, by hand: the line ends at 120 runes, ` (×2)` claims 5
			// of them and the ellipsis 1, leaving 114 for the command — 8 for
			// `node -e ` and 106 x's.
			name: "two long command lines that differ past the cut are one line, counted",
			processes: [][]string{
				row("node -e " + strings.Repeat("x", 400) + " --pid 1"),
				row("node -e " + strings.Repeat("x", 400) + " --pid 2"),
			},
			sessionCmd: []string{"/bin/zsh"},
			want:       []string{"node -e " + strings.Repeat("x", 106) + "… (×2)"},
		},
		{
			// No suffix to make room for: 119 runes of command, then the ellipsis.
			name:       "a long command line is cut at the cap",
			processes:  [][]string{row("node -e " + strings.Repeat("x", 400))},
			sessionCmd: []string{"/bin/zsh"},
			want:       []string{"node -e " + strings.Repeat("x", 111) + "…"},
		},
		{
			name:       "a multi-byte command line is cut on character boundaries",
			processes:  [][]string{row(strings.Repeat("è", 200))},
			sessionCmd: []string{"/bin/zsh"},
			want:       []string{strings.Repeat("è", 119) + "…"},
		},
		{
			name:       "a line exactly at the cap is left whole",
			processes:  [][]string{row(strings.Repeat("y", 120))},
			sessionCmd: []string{"/bin/zsh"},
			want:       []string{strings.Repeat("y", 120)},
		},
		{
			name:       "an unrecognised header yields no list rather than a column of timestamps",
			processes:  [][]string{row("npm run dev")},
			sessionCmd: []string{"/bin/zsh"},
			want:       nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hdr := titles
			if tc.name == "an unrecognised header yields no list rather than a column of timestamps" {
				hdr = []string{"UID", "PID", "WHAT"}
			}
			got := filterCasualties(hdr, tc.processes, tc.sessionCmd)
			if !slices.Equal(got, tc.want) {
				t.Errorf("filterCasualties = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestBaselineKey pins the reduction the deny-list is keyed on. Its whole
// subtlety is the shebang: ContainerTop names a script by its interpreter, so
// the key has to look one field further — except where the interpreter is
// running something of its own, and is the honest name.
func TestBaselineKey(t *testing.T) {
	cases := []struct {
		cmd  string
		want string
	}{
		{"/usr/bin/tini -g -- /usr/local/bin/entrypoint", "tini"},
		{"/usr/local/bin/proximo-hosts --watch", "proximo-hosts"},
		{"/bin/sh /usr/local/bin/proximo-hosts --watch", "proximo-hosts"},
		{"/bin/bash /usr/local/bin/proximo-hosts", "proximo-hosts"},
		{"/bin/zsh /usr/local/bin/proximo-hosts", "proximo-hosts"},
		// A flag says the shell is the process, not the launcher of one.
		{"/bin/sh -c 'npm run dev'", "sh"},
		{"/bin/zsh", "zsh"},
		// `docker` alone says nothing: the subcommand is half the name, and it
		// stays half the name behind an interpreter.
		{"docker events --filter type=container", "docker events"},
		{"/bin/sh /usr/bin/docker events --filter type=container", "docker events"},
		{"docker", "docker"},
	}
	for _, tc := range cases {
		if got := baselineKey(strings.Fields(tc.cmd)); got != tc.want {
			t.Errorf("baselineKey(%q) = %q, want %q", tc.cmd, got, tc.want)
		}
	}
}

// TestTransition pins the wording the summary owes a command that always
// fires: "same digest" is a result, not the absence of one, so it has to be
// said out loud or a pointless reload and a silently failed one look alike.
func TestTransition(t *testing.T) {
	cases := []struct{ before, after, want string }{
		{"sha256:a", "sha256:b", "sha256:a → sha256:b"},
		{"sha256:a", "sha256:a", "sha256:a (unchanged)"},
		{"sha256:a", "", "sha256:a (unchanged)"},
		{"", "sha256:b", "sha256:b"},
		{"", "", "unknown"},
	}
	for _, tc := range cases {
		if got := transition(tc.before, tc.after); got != tc.want {
			t.Errorf("transition(%q, %q) = %q, want %q", tc.before, tc.after, got, tc.want)
		}
	}
}

// A plan with no state mount can neither be signalled nor read: the container
// would see no ~/.toolbox-state either. Reporting a reload from a path built
// on an empty state dir would key the marker off the filesystem root.
func TestReloadMarkerPathNeedsTheStateMount(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	plan := testPlan(t, testWorkspace(t), nil)
	plan.StateDir = ""
	if got := plan.ReloadMarkerPath(); got != "" {
		t.Errorf("ReloadMarkerPath = %q with no state mount, want \"\"", got)
	}
	if got := takeReloadRequest(plan); got != nil {
		t.Errorf("takeReloadRequest = %+v with no state mount, want nil", got)
	}
}

// TestReloadTeardownOutcomes covers the daemon answers the wait-for-removal
// has to absorb. Every one of them ends with the name free, which is the only
// property the create that follows depends on; treating any of them as a
// failure would abort a reload that had already succeeded.
func TestReloadTeardownOutcomes(t *testing.T) {
	cases := []struct {
		name    string
		mock    func() *mockClient
		wantErr bool
	}{
		{
			name: "removed cleanly",
			mock: func() *mockClient { return &mockClient{} },
		},
		{
			// Already gone: an external `toolbox stop`, or a daemon that reaped
			// it while this process was re-execing. Nothing to wait for, and
			// nothing to complain about.
			name: "already gone",
			mock: func() *mockClient {
				return &mockClient{removeFn: func(context.Context, string, client.ContainerRemoveOptions) error {
					return &dockertest.NotFoundError{Msg: "no such container"}
				}}
			},
		},
		{
			// The daemon's own auto-remove worker got there first. Redundant,
			// not an error — and the wait is exactly how we learn it finished.
			name: "removal already in progress",
			mock: func() *mockClient {
				return &mockClient{removeFn: func(context.Context, string, client.ContainerRemoveOptions) error {
					return conflictErr{}
				}}
			},
		},
		{
			name: "the daemon refuses",
			mock: func() *mockClient {
				return &mockClient{removeFn: func(context.Context, string, client.ContainerRemoveOptions) error {
					return errors.New("daemon unreachable")
				}}
			},
			wantErr: true,
		},
		{
			// A wait that fails leaves the name's fate unknown, and creating
			// into an unknown name is how a reload turns into a collision.
			name: "the removal wait fails",
			mock: func() *mockClient {
				return &mockClient{waitFn: func(context.Context, string, client.ContainerWaitOptions) (int64, error) {
					return 0, errors.New("stream closed")
				}}
			},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := removeAndWait(context.Background(), tc.mock(), "toolbox-old-1234abcd", "reload")
			if (err != nil) != tc.wantErr {
				t.Errorf("removeAndWait() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

// conflictErr is the daemon's "removal already in progress", in the shape
// cerrdefs recognises.
type conflictErr struct{}

func (conflictErr) Error() string { return "removal of container is already in progress" }
func (conflictErr) Conflict() {
	// Marker method: cerrdefs.IsConflict matches on the interface, not the text.
}

// The list is the whole reason the reload looks at all — a Ctrl+Z-suspended
// agent and a detached job are invisible at the prompt where `toolbox-reload`
// was typed. So it has to actually reach the developer, past a filter that
// throws most of the process table away.
func TestShellReloadReportsWhatItKilled(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, restore := stubExecShell()
	defer restore()

	mock := createAfterReloadMock()
	mock.topFn = func(context.Context, string) (client.ContainerTopResult, error) {
		return client.ContainerTopResult{
			Titles: []string{"UID", "PID", "PPID", "C", "STIME", "TTY", "TIME", "CMD"},
			Processes: [][]string{
				{"1000", "1", "0", "0", "10:00", "?", "00:00:00", "/usr/bin/tini -g -- /usr/local/bin/entrypoint"},
				{"1000", "7", "1", "0", "10:00", "?", "00:00:00", "/bin/zsh"},
				{"1000", "42", "1", "0", "10:01", "?", "00:00:00", "claude"},
			},
		}, nil
	}

	out := captureStderr(t, func() {
		if _, err := Shell(context.Background(), mock, reloadPlan(t, "toolbox-old-1234abcd")); err != nil {
			t.Fatalf("Shell(): %v", err)
		}
	})

	if !strings.Contains(out, "claude") {
		t.Errorf("the forgotten agent was not reported:\n%s", out)
	}
	if strings.Contains(out, "tini") {
		t.Errorf("the baseline leaked into the casualty list:\n%s", out)
	}
	// The summary is the only evidence a reload that changes nothing did
	// anything at all, so it must be there whatever the process list held.
	if !strings.Contains(out, "sha256:old") {
		t.Errorf("no before/after summary:\n%s", out)
	}
}

// ContainerTop answers 409 on a container that is no longer running. The look
// is informational, so losing it costs evidence and never the reload.
func TestShellReloadSurvivesAnUnreadableProcessList(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, restore := stubExecShell()
	defer restore()

	mock := createAfterReloadMock()
	mock.topFn = func(context.Context, string) (client.ContainerTopResult, error) {
		return client.ContainerTopResult{}, errors.New("container is not running")
	}
	if _, err := Shell(context.Background(), mock, reloadPlan(t, "toolbox-old-1234abcd")); err != nil {
		t.Fatalf("a failed process listing broke the reload: %v", err)
	}
}

// A session that asked for a reload and then died must not leave the request
// behind: the state mount is shared with every later session, and an
// unconsumed marker would fire a reload nobody asked for at the next ordinary
// exit. The failing session itself tears down as any failing session does.
func TestShellConsumesTheMarkerEvenWhenTheSessionFails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	plan := testPlan(t, testWorkspace(t), nil)
	orig := execShellFn
	execShellFn = func(context.Context, client.APIClient, string, []string) error {
		if err := reload.WriteMarker(plan.ReloadMarkerPath(), "/workspace"); err != nil {
			return err
		}
		return errors.New("shell session ended: the container is gone")
	}
	t.Cleanup(func() { execShellFn = orig })

	mock := runningContainerMock()
	rl, err := Shell(context.Background(), mock, plan)
	if err == nil {
		t.Fatal("Shell() swallowed the session failure")
	}
	if rl != nil {
		t.Errorf("a failed session still asked for a reload: %+v", rl)
	}
	if _, requested := reload.TakeMarker(plan.ReloadMarkerPath()); requested {
		t.Error("the marker outlived the session that wrote it")
	}
	if !slices.Contains(mock.calls, "ContainerRemove") {
		t.Errorf("a failed session did not tear down: %v", mock.calls)
	}
}

// A reload never reaches the start-up prompt. It has already refreshed and
// proved the image before it destroyed anything, and its whole premise is
// that the move onto the newer image was asked for — by a developer typing
// the command or by a session that had already been asked and said "later".
// Asking again here would also mean asking with nobody watching, since the
// same path is what an unattended trigger walks.
func TestShellReloadNeverReachesTheStartUpPrompt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, restore := stubExecShell()
	defer restore()
	stubPrefetch(t)
	stakes := stubRefresh(t, imageplan.Outcome{})

	if _, err := Shell(context.Background(), createAfterReloadMock(), reloadPlan(t, "toolbox-old-1234abcd")); err != nil {
		t.Fatalf("Shell(): %v", err)
	}
	if len(*stakes) != 0 {
		t.Errorf("the reload path ran the start-up refresh %d times, want 0", len(*stakes))
	}
}
