package ui

import (
	"io"
	"os"
	"strings"
	"sync"
	"testing"
)

// captureStderr swaps os.Stderr for a pipe, runs fn, restores os.Stderr, and
// returns what fn wrote. Used to verify diagnostic output lands on stderr —
// stdout must remain reserved for program output so pipelines stay clean.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w

	var got strings.Builder
	var wg sync.WaitGroup
	wg.Go(func() {
		_, _ = io.Copy(&got, r)
	})

	fn()

	_ = w.Close()
	wg.Wait()
	os.Stderr = old
	return got.String()
}

func TestSuccessWritesToStderr(t *testing.T) {
	out := captureStderr(t, func() { Success("ready") })
	if !strings.Contains(out, "OK: ready") {
		t.Errorf("stderr = %q, want to contain %q", out, "OK: ready")
	}
}

func TestWarningWritesToStderr(t *testing.T) {
	out := captureStderr(t, func() { Warning("careful") })
	if !strings.Contains(out, "WARN: careful") {
		t.Errorf("stderr = %q, want to contain %q", out, "WARN: careful")
	}
}

func TestInfoWritesToStderr(t *testing.T) {
	out := captureStderr(t, func() { Info("pulling image") })
	if !strings.Contains(out, "pulling image") {
		t.Errorf("stderr = %q, want to contain %q", out, "pulling image")
	}
}

// The printf variants exist so callers stop wrapping every interpolated
// message in fmt.Sprintf: the wrapper adds an import and a nesting level, and
// building the message by concatenation instead duplicates the shared prefix
// literal across call sites.
func TestFormattingVariantsWriteToStderr(t *testing.T) {
	for _, tc := range []struct {
		name string
		fn   func()
		want string
	}{
		{"Successf", func() { Successf("Container %s stopped", "web") }, "OK: Container web stopped"},
		{"Warningf", func() { Warningf("Container %s not found", "web") }, "WARN: Container web not found"},
		{"Infof", func() { Infof("routing %d host(s)", 3) }, "routing 3 host(s)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := captureStderr(t, tc.fn)
			if !strings.Contains(out, tc.want) {
				t.Errorf("stderr = %q, want to contain %q", out, tc.want)
			}
		})
	}
}

// TestOutputDoesNotWriteToStdout locks the stdout/stderr discipline in place:
// if ui ever regresses to fmt.Println (stdout), pipelines like
// `toolbox shell | grep foo` get corrupted by diagnostic lines.
func TestOutputDoesNotWriteToStdout(t *testing.T) {
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = oldStdout })

	var got strings.Builder
	var wg sync.WaitGroup
	wg.Go(func() {
		_, _ = io.Copy(&got, r)
	})

	_ = captureStderr(t, func() {
		Success("a")
		Warning("b")
		Info("c")
	})

	_ = w.Close()
	wg.Wait()

	if got.Len() != 0 {
		t.Errorf("ui functions must not write to stdout, got %q", got.String())
	}
}

// A background act prints while an interactive shell may already hold the tty
// in raw mode, where term.MakeRaw has cleared ONLCR and a bare LF drops a line
// without returning the carriage — staircasing everything printed after it.
// InfoAsyncf owns that so no caller has to smuggle a control character into a
// domain format string.
func TestInfoAsyncfReturnsTheCarriage(t *testing.T) {
	out := captureStderr(t, func() { InfoAsyncf("reclaimed %d images", 2) })

	if !strings.Contains(out, "reclaimed 2 images") {
		t.Errorf("stderr = %q, want to contain the message", out)
	}
	body, ok := strings.CutSuffix(out, "\n")
	if !ok {
		t.Fatalf("stderr = %q, want a newline-terminated line", out)
	}
	if !strings.HasSuffix(body, "\r") {
		t.Errorf("stderr = %q, want the newline preceded by a carriage return", out)
	}
}
