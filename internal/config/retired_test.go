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
	for key := range RetiredKeys() {
		if n := strings.Count(got, "'"+key+":' is ignored"); n != 1 {
			t.Errorf("%q warned %d times, want once:\n%s", key, n, got)
		}
	}
	if !strings.Contains(got, ".worktreeinclude") || !strings.Contains(got, RetiredKeysDoc) {
		t.Errorf("warning must point to .worktreeinclude and the guide:\n%s", got)
	}
}
