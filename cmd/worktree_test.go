package cmd

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"

	"github.com/filippolmt/toolbox/internal/config"
)

func TestWorktreeCreateArgs(t *testing.T) {
	// ArgsLenAtDash is only populated by cobra's own arg parsing, so exercise
	// the validator through a real command Execute rather than calling it bare.
	cases := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{"branch only", []string{"feat"}, false},
		{"branch then prompt after dash", []string{"feat", "--", "add", "auth"}, false},
		{"branch then empty dash", []string{"feat", "--"}, false},
		{"stray token without dash rejected", []string{"feat", "typo"}, true},
		{"no args rejected", []string{}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cmd := &cobra.Command{
				Use:  "create",
				Args: worktreeCreateArgs,
				RunE: func(*cobra.Command, []string) error { return nil },
			}
			cmd.SetArgs(c.args)
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			if err := cmd.Execute(); (err != nil) != c.wantErr {
				t.Errorf("args %v: err = %v, wantErr = %v", c.args, err, c.wantErr)
			}
		})
	}
}

// gitInitRepo initialises a git repo at root with the given .gitignore body so
// seedWorktreeFiles' `git check-ignore` gate has real rules to evaluate.
func gitInitRepo(t *testing.T, root, gitignore string) {
	t.Helper()
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	writeFile(t, filepath.Join(root, ".gitignore"), gitignore)
}

func mustExist(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected seeded file %s: %v", path, err)
	}
	if string(got) != want {
		t.Errorf("seeded content = %q, want %q", got, want)
	}
}

func mustAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected %s absent, stat err = %v", path, err)
	}
}

func TestSeedWorktreeFiles(t *testing.T) {
	const body = `{"permissions":{"allow":["Bash(go test:*)"]}}`

	// gitignored file + recursive dir + .env variants are all seeded
	t.Run("seeds gitignored state", func(t *testing.T) {
		root, wt := t.TempDir(), t.TempDir()
		gitInitRepo(t, root, ".claude/settings.local.json\n.env\n.env.local\nopenspec/\n")
		writeFile(t, filepath.Join(root, ".claude", "settings.local.json"), body)
		writeFile(t, filepath.Join(root, ".env"), "SECRET=1")
		writeFile(t, filepath.Join(root, ".env.local"), "LOCAL=2")
		writeFile(t, filepath.Join(root, "openspec", "changes", "x", "spec.md"), "# spec")

		seedWorktreeFiles(root, wt, nil)

		mustExist(t, filepath.Join(wt, ".claude", "settings.local.json"), body)
		mustExist(t, filepath.Join(wt, ".env"), "SECRET=1")
		mustExist(t, filepath.Join(wt, ".env.local"), "LOCAL=2")
		mustExist(t, filepath.Join(wt, "openspec", "changes", "x", "spec.md"), "# spec")
	})

	// a candidate that git does NOT ignore is never copied — the core constraint
	t.Run("skips non-ignored paths", func(t *testing.T) {
		root, wt := t.TempDir(), t.TempDir()
		gitInitRepo(t, root, "openspec/\n") // .env deliberately NOT ignored
		writeFile(t, filepath.Join(root, ".env"), "SECRET=1")
		writeFile(t, filepath.Join(root, "openspec", "spec.md"), "# spec")

		seedWorktreeFiles(root, wt, nil)

		mustExist(t, filepath.Join(wt, "openspec", "spec.md"), "# spec")
		mustAbsent(t, filepath.Join(wt, ".env"))
	})

	// never clobbers a worktree-local copy the user already edited
	t.Run("does not clobber existing", func(t *testing.T) {
		root, wt := t.TempDir(), t.TempDir()
		gitInitRepo(t, root, ".env\n")
		writeFile(t, filepath.Join(root, ".env"), "SECRET=1")
		writeFile(t, filepath.Join(wt, ".env"), "LOCAL_EDIT")

		seedWorktreeFiles(root, wt, nil)

		mustExist(t, filepath.Join(wt, ".env"), "LOCAL_EDIT")
	})

	// config extra paths are seeded only when gitignored
	t.Run("config extra gated by gitignore", func(t *testing.T) {
		root, wt := t.TempDir(), t.TempDir()
		gitInitRepo(t, root, "config/local.yaml\n") // tracked.txt not ignored
		writeFile(t, filepath.Join(root, "config", "local.yaml"), "a: 1")
		writeFile(t, filepath.Join(root, "tracked.txt"), "keep")

		seedWorktreeFiles(root, wt, []string{"config/local.yaml", "tracked.txt"})

		mustExist(t, filepath.Join(wt, "config", "local.yaml"), "a: 1")
		mustAbsent(t, filepath.Join(wt, "tracked.txt"))
	})

	// no candidate present in the repo => no-op, no error
	t.Run("no candidates is a no-op", func(t *testing.T) {
		root, wt := t.TempDir(), t.TempDir()
		gitInitRepo(t, root, ".env\n")

		seedWorktreeFiles(root, wt, nil)

		mustAbsent(t, filepath.Join(wt, ".env"))
	})

	// non-ASCII names survive the `git check-ignore -z` round-trip (no C-quoting)
	t.Run("seeds non-ascii names", func(t *testing.T) {
		root, wt := t.TempDir(), t.TempDir()
		gitInitRepo(t, root, ".env.*\n")
		writeFile(t, filepath.Join(root, ".env.località"), "X=1")

		seedWorktreeFiles(root, wt, nil)

		mustExist(t, filepath.Join(wt, ".env.località"), "X=1")
	})

	// a .env.* directory is not dotenv state — never walked or seeded
	t.Run("skips .env.* directories", func(t *testing.T) {
		root, wt := t.TempDir(), t.TempDir()
		gitInitRepo(t, root, ".env.d/\n")
		writeFile(t, filepath.Join(root, ".env.d", "inner"), "nope")

		seedWorktreeFiles(root, wt, nil)

		mustAbsent(t, filepath.Join(wt, ".env.d"))
	})

	// a symlinked source is recreated as a symlink, not dereferenced into a file
	t.Run("preserves symlinks", func(t *testing.T) {
		root, wt := t.TempDir(), t.TempDir()
		gitInitRepo(t, root, ".env\n")
		external := filepath.Join(t.TempDir(), "real.env")
		writeFile(t, external, "SECRET=1")
		if err := os.Symlink(external, filepath.Join(root, ".env")); err != nil {
			t.Fatalf("symlink: %v", err)
		}

		seedWorktreeFiles(root, wt, nil)

		fi, err := os.Lstat(filepath.Join(wt, ".env"))
		if err != nil {
			t.Fatalf("expected seeded symlink: %v", err)
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			t.Errorf("seeded .env is a real file, want symlink (mode %v)", fi.Mode())
		}
		if target, _ := os.Readlink(filepath.Join(wt, ".env")); target != external {
			t.Errorf("symlink target = %q, want %q", target, external)
		}
	})

	// when `git check-ignore` itself fails, fall back to the permission allowlist
	t.Run("git error falls back to allowlist", func(t *testing.T) {
		root, wt := t.TempDir(), t.TempDir() // NOT a git repo => check-ignore errors
		writeFile(t, filepath.Join(root, ".claude", "settings.local.json"), body)
		writeFile(t, filepath.Join(root, ".env"), "SECRET=1")

		seedWorktreeFiles(root, wt, nil)

		mustExist(t, filepath.Join(wt, ".claude", "settings.local.json"), body)
		mustAbsent(t, filepath.Join(wt, ".env")) // fallback seeds only the allowlist
	})
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveAgentPrecedence(t *testing.T) {
	orig := cfg
	t.Cleanup(func() { cfg = orig })

	cfg = &config.Config{Agent: "codex"}
	if got, err := resolveAgent("claude"); err != nil || got != "claude" {
		t.Errorf("flag should win: got %q, %v", got, err)
	}
	if got, err := resolveAgent(""); err != nil || got != "codex" {
		t.Errorf("config should be used when no flag: got %q, %v", got, err)
	}

	cfg = &config.Config{}
	if got, err := resolveAgent(""); err != nil || got != config.DefaultAgent {
		t.Errorf("default should apply when neither set: got %q, %v", got, err)
	}

	if _, err := resolveAgent("gemini"); err == nil {
		t.Error("resolveAgent should reject an unsupported agent")
	}
}
