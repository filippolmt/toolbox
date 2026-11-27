package devrules

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// codegraphPromptHook returns the project's UserPromptSubmit command that
// feeds codegraph, with the codegraph call swapped for a marker echo so a test
// sees whether a prompt would have reached it.
func codegraphPromptHook(t *testing.T) string {
	t.Helper()
	var settings struct {
		Hooks struct {
			UserPromptSubmit []struct {
				Hooks []struct{ Command string }
			}
		}
	}
	if err := json.Unmarshal([]byte(readRepoFile(t, ".claude/settings.json")), &settings); err != nil {
		t.Fatalf("parse .claude/settings.json: %v", err)
	}
	for _, group := range settings.Hooks.UserPromptSubmit {
		for _, h := range group.Hooks {
			if strings.Contains(h.Command, "codegraph prompt-hook") {
				return strings.Replace(h.Command, "codegraph prompt-hook", "echo REACHED", 1)
			}
		}
	}
	t.Fatal(".claude/settings.json: no UserPromptSubmit hook runs `codegraph prompt-hook`")
	return ""
}

// TestCodegraphPromptHookSkipsSlashCommandsAndNotifications: a slash command's
// context comes from its skill and a notification is not a question, so
// codegraph's lexical symbol match only adds noise there; a prompt that merely
// opens with an absolute path is a question and still gets the context.
func TestCodegraphPromptHookSkipsSlashCommandsAndNotifications(t *testing.T) {
	for _, tool := range []string{"sh", "jq"} {
		if _, err := exec.LookPath(tool); err != nil {
			if os.Getenv("CI") != "" {
				t.Fatalf("%s not on PATH: CI must provide it, never skip the test", tool)
			}
			t.Skipf("%s not on PATH: `make go-test` provides it", tool)
		}
	}
	hook := codegraphPromptHook(t)

	for prompt, wantReached := range map[string]bool{
		"/implement-spec:implement-spec #1610":    false,
		"/clear":                                  false,
		"<task-notification>done":                 false,
		"<cross-session-message from=x>hi":        false,
		"how does startSession reach the plan?":   true,
		"/Users/me/repo/cmd/shell.go fails, why?": true,
	} {
		in, _ := json.Marshal(map[string]string{"prompt": prompt})
		cmd := exec.Command("sh", "-c", hook)
		cmd.Stdin = strings.NewReader(string(in))
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("hook on %q: %v", prompt, err)
		}
		if got := strings.Contains(string(out), "REACHED"); got != wantReached {
			t.Errorf("prompt %q reached codegraph = %v, want %v", prompt, got, wantReached)
		}
	}
}
