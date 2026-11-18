package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// claudeCLI is the real binary bin/claude execs; the test swaps it for a stub.
const claudeCLI = "/usr/local/lib/node_modules/@anthropic-ai/claude-code/bin/claude.exe"

const orcaPlugin = "/home/toolbox/.config/orca/claude-agent-view/abc123/orca-agent-view"

// claudeRun is what the real claude received from the wrapper.
type claudeRun struct {
	argv []string
	env  map[string]string
}

// runClaudeWrapper executes the embedded bin/claude under /bin/sh with a stub in
// place of the real CLI; the stub records its argv and environment.
func runClaudeWrapper(t *testing.T, env []string, args ...string) claudeRun {
	t.Helper()
	dir := t.TempDir()
	stub := filepath.Join(dir, "claude.exe")
	stubBody := "#!/bin/sh\nprintf '%s\\0' \"$@\" > \"$STUB_OUT/argv\"\nenv -0 > \"$STUB_OUT/env\"\n"
	if err := os.WriteFile(stub, []byte(stubBody), 0o755); err != nil {
		t.Fatal(err)
	}
	body := readAsset(t, "bin/claude")
	if !strings.Contains(body, claudeCLI) {
		t.Fatalf("bin/claude does not exec %s", claudeCLI)
	}
	wrapper := filepath.Join(dir, "claude")
	if err := os.WriteFile(wrapper, []byte(strings.ReplaceAll(body, claudeCLI, stub)), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", append([]string{wrapper}, args...)...)
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "STUB_OUT=" + dir}, env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("wrapper: %v\n%s", err, out)
	}
	argv, err := os.ReadFile(filepath.Join(dir, "argv"))
	if err != nil {
		t.Fatal(err)
	}
	rawEnv, err := os.ReadFile(filepath.Join(dir, "env"))
	if err != nil {
		t.Fatal(err)
	}
	run := claudeRun{argv: []string{}, env: map[string]string{}}
	if s := strings.TrimSuffix(string(argv), "\x00"); len(argv) > 0 {
		run.argv = strings.Split(s, "\x00")
	}
	for kv := range strings.SplitSeq(strings.TrimSuffix(string(rawEnv), "\x00"), "\x00") {
		k, v, _ := strings.Cut(kv, "=")
		run.env[k] = v
	}
	return run
}

func TestClaudeWrapperOutsideOrcaPassesEverythingThrough(t *testing.T) {
	args := []string{"--plugin-dir", orcaPlugin, "--plugin-dir=" + orcaPlugin, "-p", "two words", ""}
	dirs := "/opt/mine:" + orcaPlugin
	// An empty ORCA_PANE_KEY is no Orca pane either.
	for name, pane := range map[string][]string{"unset": nil, "empty": {"ORCA_PANE_KEY="}} {
		t.Run(name, func(t *testing.T) {
			env := append([]string{"CLAUDE_CODE_PLUGIN_DIRS=" + dirs, "DO_NOT_TRACK=1"}, pane...)
			run := runClaudeWrapper(t, env, args...)
			if !reflect.DeepEqual(run.argv, args) {
				t.Errorf("argv = %q, want %q", run.argv, args)
			}
			if got := run.env["CLAUDE_CODE_PLUGIN_DIRS"]; got != dirs {
				t.Errorf("CLAUDE_CODE_PLUGIN_DIRS = %q, want %q", got, dirs)
			}
			if _, ok := run.env["DO_NOT_TRACK"]; ok {
				t.Error("DO_NOT_TRACK reached claude")
			}
		})
	}
}

func TestClaudeWrapperInAnOrcaPaneDropsOrcasPluginDir(t *testing.T) {
	run := runClaudeWrapper(t, []string{"ORCA_PANE_KEY=tab-1:pane-1"},
		"--plugin-dir", orcaPlugin, "--model", "opus",
		"--plugin-dir="+orcaPlugin+"/", "--plugin-dir", "/opt/my plugins",
		"--plugin-dir=/opt/other", "-p", "two words", "--plugin-dir")
	want := []string{"--model", "opus", "--plugin-dir", "/opt/my plugins",
		"--plugin-dir=/opt/other", "-p", "two words", "--plugin-dir"}
	if !reflect.DeepEqual(run.argv, want) {
		t.Errorf("argv = %q, want %q", run.argv, want)
	}
}

func TestClaudeWrapperInAnOrcaPaneDropsOrcasPluginDirsEntry(t *testing.T) {
	pane := "ORCA_PANE_KEY=tab-1:pane-1"
	run := runClaudeWrapper(t, []string{pane, "CLAUDE_CODE_PLUGIN_DIRS=/opt/my plugins:" + orcaPlugin + ":~/other"})
	if got, want := run.env["CLAUDE_CODE_PLUGIN_DIRS"], "/opt/my plugins:~/other"; got != want {
		t.Errorf("CLAUDE_CODE_PLUGIN_DIRS = %q, want %q", got, want)
	}
	run = runClaudeWrapper(t, []string{pane, "CLAUDE_CODE_PLUGIN_DIRS=" + orcaPlugin})
	if got, ok := run.env["CLAUDE_CODE_PLUGIN_DIRS"]; ok {
		t.Errorf("CLAUDE_CODE_PLUGIN_DIRS = %q, want it unset", got)
	}
}

func TestClaudeWrapperInAnOrcaPaneLeavesPluginDirsWithoutOrcasEntryAlone(t *testing.T) {
	dirs := "/opt/a::/opt/b"
	run := runClaudeWrapper(t, []string{"ORCA_PANE_KEY=tab-1:pane-1", "CLAUDE_CODE_PLUGIN_DIRS=" + dirs, "DO_NOT_TRACK=1"})
	if got := run.env["CLAUDE_CODE_PLUGIN_DIRS"]; got != dirs {
		t.Errorf("CLAUDE_CODE_PLUGIN_DIRS = %q, want %q", got, dirs)
	}
	if _, ok := run.env["DO_NOT_TRACK"]; ok {
		t.Error("DO_NOT_TRACK reached claude")
	}
}
