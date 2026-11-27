package bridge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An op outside credentialSubcommand must fail before any exec — the daemon
// already gates on the same map, so this is defense in depth.
func TestRunHostCredential_UnknownOp(t *testing.T) {
	out, exit, err := runHostCredential(context.Background(), "delete", nil, false)
	if err == nil {
		t.Fatalf("want error for unknown op, got out=%q exit=%d", out, exit)
	}
	if out != nil || exit != 0 {
		t.Errorf("unknown op should not exec: out=%q exit=%d", out, exit)
	}
}

// GIT_TERMINAL_PROMPT=0 silences only git's own terminal prompt. A host
// helper such as Git Credential Manager opens a GUI or browser flow of its
// own, and an askpass program is a GUI too: a no-prompt request turns all
// of them off on the host, and a normal one leaves them as they are.
func TestRunHostCredential_NoPromptSilencesHostHelpers(t *testing.T) {
	bin := t.TempDir()
	record := filepath.Join(t.TempDir(), "record")
	fakeGit := "#!/bin/sh\nprintf 'args=%s\\nGCM_INTERACTIVE=%s\\nGIT_ASKPASS=%s\\nSSH_ASKPASS=%s\\n' \"$*\" \"${GCM_INTERACTIVE-unset}\" \"${GIT_ASKPASS-unset}\" \"${SSH_ASKPASS-unset}\" > \"$RECORD\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(fakeGit), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("RECORD", record)
	t.Setenv("GIT_ASKPASS", "/host/askpass")

	for _, tc := range []struct {
		noPrompt bool
		want     []string
		absent   []string
	}{
		{true, []string{
			"args=-c core.askPass= -c credential.interactive=false credential fill",
			"GCM_INTERACTIVE=never", "GIT_ASKPASS=\n", "SSH_ASKPASS=\n",
		}, nil},
		{false, []string{"args=credential fill", "GIT_ASKPASS=/host/askpass"},
			[]string{"GCM_INTERACTIVE=never", "credential.interactive"}},
	} {
		if _, _, err := runHostCredential(context.Background(), "get", nil, tc.noPrompt); err != nil {
			t.Fatalf("noPrompt=%v: %v", tc.noPrompt, err)
		}
		got, err := os.ReadFile(record)
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range tc.want {
			if !strings.Contains(string(got), w) {
				t.Errorf("noPrompt=%v: want %q in\n%s", tc.noPrompt, w, got)
			}
		}
		for _, a := range tc.absent {
			if strings.Contains(string(got), a) {
				t.Errorf("noPrompt=%v: %q must not be set, got\n%s", tc.noPrompt, a, got)
			}
		}
	}
}
