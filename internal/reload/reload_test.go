package reload_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/filippolmt/toolbox/internal/reload"
)

// TestDecode pins the one asymmetry the payload has: every field degrades to a
// safe zero value except the container name, whose loss is unrecoverable.
// Lose it and nothing destroys the old container; the next `toolbox shell`
// resolves the same deterministic name, reuses what is still there, and the
// developer lands back on the old image with no error anywhere — which is the
// exact failure a session reload exists to remove.
func TestDecode(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr bool
		want    reload.From
	}{
		{
			name: "every field",
			raw:  `{"container":"toolbox-p-1234abcd","cwd":"/workspace/sub","image_digest":"sha256:a","cli_version":"v1.2.3"}`,
			want: reload.From{Container: "toolbox-p-1234abcd", Cwd: "/workspace/sub", ImageDigest: "sha256:a", CLIVersion: "v1.2.3"},
		},
		{
			name: "container name alone is enough",
			raw:  `{"container":"toolbox-p-1234abcd"}`,
			want: reload.From{Container: "toolbox-p-1234abcd"},
		},
		{
			name: "the two continuity fields",
			raw:  `{"container":"c","reentry":["worktree","open","fix/x"],"resume":true}`,
			want: reload.From{Container: "c", Reentry: []string{"worktree", "open", "fix/x"}, Resume: true},
		},
		{
			// A field this version does not know must not turn into a refusal:
			// the payload carries no version precisely so an older binary can
			// read a newer one's.
			name: "unknown field is ignored",
			raw:  `{"container":"c","launch_mode":"agent"}`,
			want: reload.From{Container: "c"},
		},
		{name: "no container name", raw: `{"cwd":"/workspace"}`, wantErr: true},
		{name: "empty container name", raw: `{"container":""}`, wantErr: true},
		{name: "not json", raw: `container=c`, wantErr: true},
		{name: "empty", raw: ``, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := reload.Decode(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Decode(%q) = %+v, want an error", tc.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Decode(%q): %v", tc.raw, err)
			}
			if !reflect.DeepEqual(*got, tc.want) {
				t.Errorf("Decode(%q) = %+v, want %+v", tc.raw, *got, tc.want)
			}
		})
	}
}

// TestEncodeDecodeRoundTrip keeps the two halves of the handover honest about
// each other: the producer and the consumer are the same process family, but
// they are separated by an exec that erases everything else.
func TestEncodeDecodeRoundTrip(t *testing.T) {
	want := reload.From{
		Container: "toolbox-proj-deadbeef",
		// A cwd may hold any byte but NUL — including the characters a
		// delimited string would have to escape. JSON is what makes that free.
		Cwd:         `/workspace/dir with spaces/and"quote`,
		ImageDigest: "sha256:abc",
		CLIVersion:  "v9.9.9",
		Reentry:     []string{"worktree", "open", "fix/thing"},
		Resume:      true,
	}
	raw, err := reload.Encode(want)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := reload.Decode(raw)
	if err != nil {
		t.Fatalf("Decode(%q): %v", raw, err)
	}
	if !reflect.DeepEqual(*got, want) {
		t.Errorf("round trip = %+v, want %+v", *got, want)
	}
}

// TestTakeUnsetsEvenWhenItRefuses covers the correctness property behind the
// choice of one variable rather than one per field: the handover must be gone
// from the environment before anything builds a container env. A payload we
// refuse is exactly the case where an early return could skip the unset.
func TestTakeUnsetsEvenWhenItRefuses(t *testing.T) {
	t.Setenv(reload.FromEnv, `{"cwd":"/workspace"}`)

	if _, err := reload.Take(); err == nil {
		t.Fatal("Take() accepted a payload with no container name")
	}
	if v, ok := os.LookupEnv(reload.FromEnv); ok {
		t.Errorf("%s survived a refused Take() as %q", reload.FromEnv, v)
	}
}

// TestTakeWithoutPayload pins the ordinary shell start: no payload, no error,
// nothing to do.
func TestTakeWithoutPayload(t *testing.T) {
	t.Setenv(reload.FromEnv, "")
	if err := os.Unsetenv(reload.FromEnv); err != nil {
		t.Fatalf("Unsetenv: %v", err)
	}

	got, err := reload.Take()
	if err != nil || got != nil {
		t.Errorf("Take() = (%+v, %v), want (nil, nil)", got, err)
	}
}

// TestMarkerRoundTripDeletesOnRead pins the property that makes an orphaned
// marker harmless: a session that crashed after writing one must not reload
// the next session that happens to attach to the same container.
func TestMarkerRoundTripDeletesOnRead(t *testing.T) {
	path := reload.MarkerPath(t.TempDir(), "toolbox-proj-deadbeef")

	if _, requested := reload.TakeMarker(path); requested {
		t.Fatal("TakeMarker reported a request with no marker on disk")
	}

	if err := reload.WriteMarker(path, "/workspace/sub"); err != nil {
		t.Fatalf("WriteMarker: %v", err)
	}
	cwd, requested := reload.TakeMarker(path)
	if !requested || cwd != "/workspace/sub" {
		t.Fatalf("TakeMarker = (%q, %v), want (\"/workspace/sub\", true)", cwd, requested)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("marker survived the read: stat = %v", err)
	}
	if _, requested := reload.TakeMarker(path); requested {
		t.Error("a consumed marker fired a second reload")
	}
}

// TestTakeMarkerEmptyBodyStillRequests separates the two things the marker
// carries: its existence is the request, its body is the one carried nicety.
// A truncated write must still reload — just from the canonical working dir.
func TestTakeMarkerEmptyBodyStillRequests(t *testing.T) {
	path := filepath.Join(t.TempDir(), "marker")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("seed marker: %v", err)
	}
	cwd, requested := reload.TakeMarker(path)
	if !requested || cwd != "" {
		t.Errorf("TakeMarker = (%q, %v), want (\"\", true)", cwd, requested)
	}
}

// TestMarkerNameIsKeyedOnTheContainer pins the naming to the one input both
// sides can compute: the host names the file before the container exists (the
// env is fixed at ContainerCreate, the id is not yet known), and a sibling
// shell's host process, which created nothing, must arrive at the same file.
func TestMarkerNameIsKeyedOnTheContainer(t *testing.T) {
	a, b := reload.MarkerName("toolbox-a-1111aaaa"), reload.MarkerName("toolbox-b-2222bbbb")
	if a == b {
		t.Fatalf("two containers share the marker name %q", a)
	}
	if a != reload.MarkerName("toolbox-a-1111aaaa") {
		t.Error("MarkerName is not deterministic")
	}
	for _, n := range []string{a, b} {
		if strings.ContainsAny(n, `/\`) {
			t.Errorf("marker name %q is not a plain basename", n)
		}
	}
}

// TestReentryCommand pins the line a failed reload prints. By the time it is
// needed the shell that would normally say how to get back has exited, and
// after the teardown the old container is gone too — so the command has to be
// the one that actually re-enters *this* session, not a generic suggestion.
func TestReentryCommand(t *testing.T) {
	cases := []struct {
		name string
		from reload.From
		want string
	}{
		{
			name: "no form falls back to a plain shell",
			from: reload.From{Container: "c"},
			want: "toolbox shell",
		},
		{
			name: "a named shell re-enters by name",
			from: reload.From{Container: "c", Reentry: []string{"shell", "infra"}},
			want: "toolbox shell infra",
		},
		{
			// Never `worktree create`: the branch exists now, and the prompt
			// that came with it has already been answered.
			name: "a worktree re-enters through open",
			from: reload.From{Container: "c", Reentry: []string{"worktree", "open", "fix/thing"}},
			want: "toolbox worktree open fix/thing",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.from.ReentryCommand(); got != tc.want {
				t.Errorf("ReentryCommand() = %q, want %q", got, tc.want)
			}
		})
	}
}

// The decline stamp is the one thing a "no" at the start-up refresh prompt
// leaves behind: a postponement has to be legible to the session it postponed,
// and the moment it happened is the whole payload — hence a modtime and an
// empty body. Keyed on the container name for the same reason the marker is:
// the path is computed before the container exists and must be identical on
// the connect path.
func TestTouchDeclinedStampsBesideTheMarker(t *testing.T) {
	dir := t.TempDir()

	if err := reload.TouchDeclined(dir, "toolbox-abc123"); err != nil {
		t.Fatalf("TouchDeclined: %v", err)
	}

	path := reload.DeclinedPath(dir, "toolbox-abc123")
	if got := filepath.Dir(path); got != dir {
		t.Errorf("stamp landed in %q, want %q", got, dir)
	}
	if path == reload.MarkerPath(dir, "toolbox-abc123") {
		t.Error("the decline stamp must not collide with the reload marker")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat stamp: %v", err)
	}
	if time.Since(info.ModTime()) > time.Minute {
		t.Errorf("stamp modtime is %v, want the moment of the decline", info.ModTime())
	}
	if info.Size() != 0 {
		t.Errorf("stamp carries %d bytes, want an empty file", info.Size())
	}
}
