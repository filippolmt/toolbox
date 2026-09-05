package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/mountplan"
)

// walkCommands visits every command in the tree rooted at c, c included.
func walkCommands(c *cobra.Command, visit func(*cobra.Command)) {
	visit(c)
	for _, sub := range c.Commands() {
		walkCommands(sub, visit)
	}
}

// TestEveryConfigWriterCommandOffersDryRun is the drift guard on the writer
// surface: --where and --dry-run come from one registration
// (registerWriteFlags), so a command carrying one and not the other means a
// writer was wired by hand and the preview is the half that got forgotten.
//
// The implication runs one way only, and --where is the antecedent: it is what
// makes a command a config writer at all, and every edit such a command applies
// is a Pending Mutation — something that can be rendered instead of written,
// for free. The converse is not a rule: `toolbox worktree prune --dry-run`
// previews removals on disk and has no config file to target.
func TestEveryConfigWriterCommandOffersDryRun(t *testing.T) {
	writers := 0
	walkCommands(rootCmd, func(c *cobra.Command) {
		if c.Flags().Lookup("where") == nil {
			return
		}
		writers++
		if c.Flags().Lookup("dry-run") == nil {
			t.Errorf("%q writes a config file but offers no --dry-run — register both through registerWriteFlags",
				c.CommandPath())
		}
	})
	if writers == 0 {
		t.Fatal("no writer command found — the guard would pass on an empty surface")
	}
}

// TestConfigWritesGoThroughTheDryRunLane is the half the flag pairing cannot
// give: a command could carry --dry-run and still write directly, leaving the
// flag inert. Every config write in cmd goes through applyOrPreview, the one
// place that reads it.
func TestConfigWritesGoThroughTheDryRunLane(t *testing.T) {
	// How many direct calls a file may carry outside the lane. configwrite.go
	// is the lane itself, so it is unbounded. shell_named.go holds the
	// `shell --create` bootstrap — a side effect of entering a shell rather
	// than a writer command with a flag surface of its own (see
	// upsertShellInUserConfig) — and holds exactly one: an exemption by
	// filename alone would let a second writer in beside the one that earned
	// it, which is the escape this test exists to close.
	const unbounded = -1
	budget := map[string]int{"configwrite.go": unbounded, "shell_named.go": 1}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read cmd dir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		calls := bytes.Count(body, []byte("configedit.ApplyChecked("))
		if calls == 0 {
			continue
		}
		allowed, known := budget[name]
		switch {
		case !known:
			t.Errorf("cmd/%s calls configedit.ApplyChecked directly — route the write through applyOrPreview so --dry-run reaches it", name)
		case allowed != unbounded && calls != allowed:
			t.Errorf("cmd/%s carries %d direct configedit.ApplyChecked calls, expected %d — a new writer here bypasses applyOrPreview and its --dry-run", name, calls, allowed)
		}
	}
}

// dryRunCase is one writer command exercised through its RunE with the dry-run
// flag set: the file it starts from, the call, and what the rendered candidate
// must (or must not) carry.
type dryRunCase struct {
	name    string
	cmd     *cobra.Command
	seed    string
	arrange func(t *testing.T)
	run     func() error
	want    string // fragment the candidate must contain
	absent  string // fragment the candidate must have dropped
}

// TestDryRunPrintsTheCandidateAndWritesNothing drives every writer command's
// dry run end to end: stdout carries the file the write would produce, and the
// target is exactly as it was — untouched, or still absent.
func TestDryRunPrintsTheCandidateAndWritesNothing(t *testing.T) {
	defaultMount := mountplan.Defaults()[0].Name

	for _, tc := range []dryRunCase{
		{
			name:    "config set",
			cmd:     configSetCmd,
			arrange: func(t *testing.T) { setConfigSetFlag(t, "pull", "never") },
			run: func() error {
				configSetDryRun = true
				return runConfigSet(configSetCmd, nil)
			},
			want: "pull: never",
		},
		{
			name: "mounts add",
			cmd:  mountsAddCmd,
			run: func() error {
				mountsAddSource, mountsAddTarget, mountsAddDryRun = "~/scratch", "/scratch", true
				return runMountsAdd(mountsAddCmd, []string{"scratch"})
			},
			want: "- name: scratch",
		},
		{
			name:    "mounts disable",
			cmd:     mountsDisableCmd,
			arrange: func(t *testing.T) { withCfg(t, &config.Config{}) },
			run: func() error {
				mountsDisableDryRun = true
				return runMountsDisable(mountsDisableCmd, []string{defaultMount})
			},
			want: "disabled: true",
		},
		{
			name: "mounts remove",
			cmd:  mountsRemoveCmd,
			seed: "mounts:\n  - name: scratch\n    source: ~/s\n    target: /s\n",
			run: func() error {
				mountsRemoveDryRun = true
				return runMountsRemove(mountsRemoveCmd, []string{"scratch"})
			},
			absent: "scratch",
		},
		{
			name: "mounts root",
			cmd:  mountsRootCmd,
			run: func() error {
				mountsRootDryRun = true
				return runMountsRoot(mountsRootCmd, []string{"/vault/toolbox"})
			},
			want: "mounts_root: /vault/toolbox",
		},
		{
			name: "shells add",
			cmd:  shellsAddCmd,
			run: func() error {
				shellsAddPath, shellsAddDryRun = "/tmp/infra", true
				return runShellsAdd(shellsAddCmd, []string{"infra"})
			},
			want: "path: /tmp/infra",
		},
		{
			name: "shells set",
			cmd:  shellsSetCmd,
			seed: "shells:\n  infra:\n    path: /tmp/infra\n",
			arrange: func(t *testing.T) {
				withCfg(t, &config.Config{Shells: map[string]config.NamedShell{"infra": {Path: "/tmp/infra"}}})
			},
			run: func() error {
				shellsSetEnv, shellsSetDryRun = []string{"FOO=bar"}, true
				return runShellsSet(shellsSetCmd, []string{"infra"})
			},
			want: "FOO: bar",
		},
		{
			name: "shells remove",
			cmd:  shellsRemoveCmd,
			seed: "shells:\n  infra:\n    path: /tmp/infra\n",
			run: func() error {
				shellsRemoveDryRun = true
				return runShellsRemove(shellsRemoveCmd, []string{"infra"})
			},
			absent: "infra",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetShellsFlags(t)
			resetMountsFlags(t)
			home := t.TempDir()
			t.Setenv("HOME", home)
			chdirTemp(t)
			cfgPath := filepath.Join(home, ".toolbox.yaml")
			if tc.seed != "" {
				if err := os.WriteFile(cfgPath, []byte(tc.seed), 0o600); err != nil {
					t.Fatalf("seed: %v", err)
				}
			}
			if tc.arrange != nil {
				tc.arrange(t)
			}

			out := &bytes.Buffer{}
			tc.cmd.SetOut(out)
			t.Cleanup(func() { tc.cmd.SetOut(nil) })

			if err := tc.run(); err != nil {
				t.Fatalf("dry run: %v", err)
			}

			printed := out.String()
			if tc.want != "" && !strings.Contains(printed, tc.want) {
				t.Errorf("dry run must print a candidate containing %q, got:\n%s", tc.want, printed)
			}
			if tc.absent != "" && strings.Contains(printed, tc.absent) {
				t.Errorf("dry run must print a candidate without %q, got:\n%s", tc.absent, printed)
			}
			if strings.Contains(printed, ": created") || strings.Contains(printed, ": updated") {
				t.Errorf("a dry run must not report a write:\n%s", printed)
			}

			got, err := os.ReadFile(cfgPath)
			switch {
			case tc.seed == "" && err == nil:
				t.Errorf("a dry run must not create the target file:\n%s", got)
			case tc.seed != "" && err != nil:
				t.Fatalf("read back: %v", err)
			case tc.seed != "" && string(got) != tc.seed:
				t.Errorf("a dry run must leave the file byte-identical:\n%s", got)
			}
		})
	}
}

// TestDryRunReportsTheRejectionTheWriteWould: the flag previews the command and
// not merely its rendering — an edit the doctor gate would refuse fails the dry
// run too, so "it printed fine" means "it would have landed".
func TestDryRunReportsTheRejectionTheWriteWould(t *testing.T) {
	resetShellsFlags(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	chdirTemp(t)

	// An env overlay on a shell whose path no layer supplies is invalid config.
	withCfg(t, &config.Config{Shells: map[string]config.NamedShell{"infra": {Path: ""}}})
	if err := os.WriteFile(filepath.Join(home, ".toolbox.yaml"),
		[]byte("shells:\n  infra:\n    path: \"\"\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	shellsSetEnv, shellsSetDryRun = []string{"FOO=bar"}, true
	out := &bytes.Buffer{}
	shellsSetCmd.SetOut(out)
	t.Cleanup(func() { shellsSetCmd.SetOut(nil) })

	err := runShellsSet(shellsSetCmd, []string{"infra"})
	if err == nil {
		t.Fatal("a dry run of a rejected edit must report the rejection")
	}
	if !strings.Contains(err.Error(), "shells.infra.path is empty") {
		t.Errorf("the dry run must name the same finding the write would, got: %v", err)
	}
	if out.Len() > 0 {
		t.Errorf("a rejected dry run must print no candidate, got:\n%s", out.String())
	}
}

// TestDryRunSkipsHostSideEffects: the two writer flags that reach past the
// config file — the directory `shells add --create-dir` creates and the one
// `shells remove --purge-dir` deletes — are named on stderr instead of
// performed, so the host is untouched and stdout stays the candidate document.
func TestDryRunSkipsHostSideEffects(t *testing.T) {
	t.Run("create-dir", func(t *testing.T) {
		resetShellsFlags(t)
		t.Setenv("HOME", t.TempDir())
		chdirTemp(t)
		dir := filepath.Join(t.TempDir(), "made-by-toolbox")

		shellsAddPath, shellsAddCreateDir, shellsAddDryRun = dir, true, true
		stderr := &bytes.Buffer{}
		shellsAddCmd.SetOut(&bytes.Buffer{})
		shellsAddCmd.SetErr(stderr)
		t.Cleanup(func() { shellsAddCmd.SetOut(nil); shellsAddCmd.SetErr(nil) })

		if err := runShellsAdd(shellsAddCmd, []string{"infra"}); err != nil {
			t.Fatalf("runShellsAdd: %v", err)
		}
		if _, err := os.Stat(dir); err == nil {
			t.Error("a dry run must not create the --create-dir directory")
		}
		if !strings.Contains(stderr.String(), dir) {
			t.Errorf("the skipped directory must be named on stderr, got: %s", stderr.String())
		}
	})

	t.Run("purge-dir", func(t *testing.T) {
		resetShellsFlags(t)
		home := t.TempDir()
		t.Setenv("HOME", home)
		chdirTemp(t)
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(home, ".toolbox.yaml"),
			[]byte("shells:\n  infra:\n    path: "+dir+"\n"), 0o600); err != nil {
			t.Fatalf("seed: %v", err)
		}

		shellsRemovePurge, shellsRemoveDryRun = true, true
		stderr := &bytes.Buffer{}
		shellsRemoveCmd.SetOut(&bytes.Buffer{})
		shellsRemoveCmd.SetErr(stderr)
		t.Cleanup(func() { shellsRemoveCmd.SetOut(nil); shellsRemoveCmd.SetErr(nil) })

		if err := runShellsRemove(shellsRemoveCmd, []string{"infra"}); err != nil {
			t.Fatalf("runShellsRemove: %v", err)
		}
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("a dry run must not purge the directory: %v", err)
		}
		if !strings.Contains(stderr.String(), dir) {
			t.Errorf("the skipped purge must be named on stderr, got: %s", stderr.String())
		}
	})
}
