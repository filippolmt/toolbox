package config

import (
	"io"
	"os"
	"strings"
	"sync"
	"testing"
)

// TestWarnRetiredKeysNamesEachKeyOnce: a retired key found in any layer is
// warned about once per process, however many layers or loads carry it.
func TestWarnRetiredKeysNamesEachKeyOnce(t *testing.T) {
	retiredKeysWarned = sync.Map{}
	t.Cleanup(func() { retiredKeysWarned = sync.Map{} })

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = w
	warnRetiredKeys([]byte("worktree:\n  seed: [.env]\n"), nil, []byte("agent: codex\nworktree: {}\n"))
	warnRetiredKeys([]byte("agent: pi\n"))
	os.Stderr = orig
	_ = w.Close()
	out, _ := io.ReadAll(r)

	got := string(out)
	for _, key := range []string{"worktree", "agent"} {
		r := RetiredKeys()[key]
		if n := strings.Count(got, r.Hint); n != 1 {
			t.Errorf("%q warned %d times, want once:\n%s", key, n, got)
		}
		if !strings.Contains(got, r.Doc) {
			t.Errorf("%q warning must name its guide %s:\n%s", key, r.Doc, got)
		}
	}
	if !strings.Contains(got, ".worktreeinclude") {
		t.Errorf("worktree warning must point to .worktreeinclude:\n%s", got)
	}
	if strings.Contains(got, "'tools:'") {
		t.Errorf("no buffer carried tools:, yet it was warned about:\n%s", got)
	}
}
