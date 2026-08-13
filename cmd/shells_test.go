package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/config"
)

// withCfg swaps the package-level resolved config for one test.
func withCfg(t *testing.T, c *config.Config) {
	t.Helper()
	orig := cfg
	cfg = c
	t.Cleanup(func() { cfg = orig })
}

// resetShellsFlags restores the shells writer flag vars after a test that
// sets them directly (RunE is invoked without cobra's flag parsing).
func resetShellsFlags(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		shellsAddPath, shellsAddCreateDir, shellsAddEnv, shellsAddWhere = "", false, nil, "global"
		shellsSetEnv, shellsSetWhere = nil, "global"
		shellsRemovePurge, shellsRemoveWhere = false, "global"
	})
}

func TestShellsAddCreatesGlobalFileWithHeader(t *testing.T) {
	resetShellsFlags(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	workdir := t.TempDir()

	shellsAddPath = workdir
	shellsAddWhere = "global"
	out := &bytes.Buffer{}
	shellsAddCmd.SetOut(out)

	if err := runShellsAdd(shellsAddCmd, []string{"infra"}); err != nil {
		t.Fatalf("runShellsAdd: %v", err)
	}

	cfgPath := filepath.Join(home, ".toolbox.yaml")
	body, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read %s: %v", cfgPath, err)
	}
	got := string(body)
	if !strings.HasPrefix(got, "# .toolbox.yaml") {
		t.Errorf("created file must start with docs header:\n%s", got)
	}
	if !strings.Contains(got, "shells:\n  infra:\n    path: "+workdir) {
		t.Errorf("missing shells.infra.path:\n%s", got)
	}
	if !strings.Contains(out.String(), cfgPath+": created") {
		t.Errorf("report must say created, got: %s", out.String())
	}
}

func TestShellsAddCreateDir(t *testing.T) {
	resetShellsFlags(t)
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "newdir")

	shellsAddPath = path
	shellsAddCreateDir = true
	shellsAddCmd.SetOut(&bytes.Buffer{})

	if err := runShellsAdd(shellsAddCmd, []string{"qa"}); err != nil {
		t.Fatalf("runShellsAdd: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		t.Errorf("--create-dir must create %s: %v", path, err)
	}
}

func TestShellsAddWhereLocalCreatesProjectFile(t *testing.T) {
	resetShellsFlags(t)
	t.Setenv("HOME", t.TempDir())
	cwd := chdirTemp(t)
	workdir := t.TempDir()

	shellsAddPath = workdir
	shellsAddWhere = "local"
	out := &bytes.Buffer{}
	shellsAddCmd.SetOut(out)

	if err := runShellsAdd(shellsAddCmd, []string{"qa"}); err != nil {
		t.Fatalf("runShellsAdd: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(cwd, ".toolbox.yaml"))
	if err != nil {
		t.Fatalf("local write must create ./.toolbox.yaml: %v", err)
	}
	if !strings.Contains(string(body), "qa:") {
		t.Errorf("missing qa entry:\n%s", body)
	}
}

func TestShellsAddRejectsRelativePath(t *testing.T) {
	resetShellsFlags(t)
	t.Setenv("HOME", t.TempDir())

	shellsAddPath = "relative/dir"
	shellsAddCmd.SetOut(&bytes.Buffer{})

	if err := runShellsAdd(shellsAddCmd, []string{"infra"}); err == nil {
		t.Fatal("relative --path must be rejected")
	}
}

// TestShellsAddRejectsInvalidFile: a CLI write no longer commits into a file
// the doctor rejects — the transactional guarantee `config ui` has always had,
// now shared by every write surface. The seeded shells.broken entry has no
// path, which lintShellPaths reports as an error.
func TestShellsAddRejectsInvalidFile(t *testing.T) {
	resetShellsFlags(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	chdirTemp(t)
	cfgPath := filepath.Join(home, ".toolbox.yaml")
	seed := "shells:\n  broken:\n    env:\n      A: \"1\"\n"
	if err := os.WriteFile(cfgPath, []byte(seed), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	shellsAddPath = t.TempDir()
	out := &bytes.Buffer{}
	shellsAddCmd.SetOut(out)

	err := runShellsAdd(shellsAddCmd, []string{"infra"})
	if err == nil {
		t.Fatal("a write into a doctor-rejected file must fail")
	}
	if !strings.Contains(err.Error(), "shells.broken.path is empty") {
		t.Errorf("error must name the finding, got: %v", err)
	}
	body, readErr := os.ReadFile(cfgPath)
	if readErr != nil {
		t.Fatalf("read %s: %v", cfgPath, readErr)
	}
	if string(body) != seed {
		t.Errorf("a rejected edit must leave the file byte-identical:\n%s", body)
	}
	if out.String() != "" {
		t.Errorf("a rejected edit must print no write report, got: %q", out.String())
	}
}

func TestShellsSetRejectsReservedEnvKey(t *testing.T) {
	resetShellsFlags(t)
	t.Setenv("HOME", t.TempDir())
	withCfg(t, &config.Config{Shells: map[string]config.NamedShell{"infra": {Path: "/tmp/infra"}}})

	shellsSetEnv = []string{"TOOLBOX_HACK=1"}
	shellsSetCmd.SetOut(&bytes.Buffer{})

	err := runShellsSet(shellsSetCmd, []string{"infra"})
	if err == nil {
		t.Fatal("reserved env key must be rejected")
	}
	if _, ok := errors.AsType[*usageError](err); !ok {
		t.Errorf("reserved env key must be a usage error (exit 2), got %T", err)
	}
}

func TestShellsSetWritesEnv(t *testing.T) {
	resetShellsFlags(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	chdirTemp(t)
	// The file backing the resolved shell, not just the in-memory cfg: an env
	// overlay for a shell no layer gives a path to is invalid config, and the
	// write gate now says so.
	if err := os.WriteFile(filepath.Join(home, ".toolbox.yaml"),
		[]byte("shells:\n  infra:\n    path: /tmp/infra\n"), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	withCfg(t, &config.Config{Shells: map[string]config.NamedShell{"infra": {Path: "/tmp/infra"}}})

	shellsSetEnv = []string{"FOO=bar"}
	shellsSetCmd.SetOut(&bytes.Buffer{})

	if err := runShellsSet(shellsSetCmd, []string{"infra"}); err != nil {
		t.Fatalf("runShellsSet: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(home, ".toolbox.yaml"))
	if err != nil {
		t.Fatalf("read global: %v", err)
	}
	if !strings.Contains(string(body), "env:\n      FOO: bar") {
		t.Errorf("missing env entry:\n%s", body)
	}
}

func TestShellsSetUnknownNameSuggests(t *testing.T) {
	resetShellsFlags(t)
	t.Setenv("HOME", t.TempDir())
	withCfg(t, &config.Config{Shells: map[string]config.NamedShell{"infra": {Path: "/tmp/infra"}}})

	shellsSetEnv = []string{"FOO=bar"}
	err := runShellsSet(shellsSetCmd, []string{"infar"})
	if err == nil || !strings.Contains(err.Error(), `did you mean "infra"?`) {
		t.Errorf("expected suggestion, got: %v", err)
	}
}

// TestShellsWriteCanonicalKey: a mixed-case or padded name writes the
// canonical (trimmed, lowercased) key — the one the loader will look the shell
// up under — instead of a second key that only collapses onto it at load time.
func TestShellsWriteCanonicalKey(t *testing.T) {
	for _, name := range []string{"Infra", " infra ", "INFRA"} {
		t.Run(name, func(t *testing.T) {
			resetShellsFlags(t)
			home := t.TempDir()
			t.Setenv("HOME", home)
			workdir := t.TempDir()

			shellsAddPath = workdir
			shellsAddEnv = []string{"FOO=bar"}
			shellsAddCmd.SetOut(&bytes.Buffer{})
			if err := runShellsAdd(shellsAddCmd, []string{name}); err != nil {
				t.Fatalf("runShellsAdd(%q): %v", name, err)
			}

			body, err := os.ReadFile(filepath.Join(home, ".toolbox.yaml"))
			if err != nil {
				t.Fatalf("read global: %v", err)
			}
			if !strings.Contains(string(body), "shells:\n  infra:\n") {
				t.Errorf("want the canonical key shells.infra:\n%s", body)
			}
		})
	}
}

// TestShellsEditExistingKeySpelling: an entry the file spells differently is
// edited in place. Writing the canonical key here would leave two shells:
// entries that collapse into one when viper loads the file.
func TestShellsEditExistingKeySpelling(t *testing.T) {
	resetShellsFlags(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgPath := filepath.Join(home, ".toolbox.yaml")
	if err := os.WriteFile(cfgPath, []byte("shells:\n  Infra:\n    path: /tmp/infra\n"), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	withCfg(t, &config.Config{Shells: map[string]config.NamedShell{"infra": {Path: "/tmp/infra"}}})

	shellsSetEnv = []string{"FOO=bar"}
	shellsSetCmd.SetOut(&bytes.Buffer{})
	if err := runShellsSet(shellsSetCmd, []string{"infra"}); err != nil {
		t.Fatalf("runShellsSet: %v", err)
	}

	body, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read global: %v", err)
	}
	if strings.Contains(string(body), "\n  infra:") {
		t.Errorf("edit added a second key beside the existing Infra::\n%s", body)
	}
	if !strings.Contains(string(body), "env:\n      FOO: bar") {
		t.Errorf("env not written under the existing entry:\n%s", body)
	}
}

// TestShellsReadAndRemoveNormalizeName: the read and delete paths accept any
// spelling of a configured shell, like `toolbox shell <name>` does.
func TestShellsReadAndRemoveNormalizeName(t *testing.T) {
	resetShellsFlags(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, ".toolbox.yaml"),
		[]byte("shells:\n  infra:\n    path: /tmp/infra\n"), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	withCfg(t, &config.Config{Shells: map[string]config.NamedShell{"infra": {Path: "/tmp/infra"}}})

	out := &bytes.Buffer{}
	shellsGetCmd.SetOut(out)
	if err := runShellsGet(shellsGetCmd, []string{" Infra "}); err != nil {
		t.Fatalf("runShellsGet: %v", err)
	}
	if !strings.Contains(out.String(), "path: /tmp/infra") {
		t.Errorf("get did not resolve the shell, got: %s", out.String())
	}

	shellsRemoveCmd.SetOut(&bytes.Buffer{})
	if err := runShellsRemove(shellsRemoveCmd, []string{"INFRA"}); err != nil {
		t.Fatalf("runShellsRemove: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(home, ".toolbox.yaml"))
	if err != nil {
		t.Fatalf("read global: %v", err)
	}
	if strings.Contains(string(body), "infra") {
		t.Errorf("entry must be removed:\n%s", body)
	}
}

func TestShellsRemovePurgeDir(t *testing.T) {
	resetShellsFlags(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	workdir := t.TempDir()

	shellsAddPath = workdir
	shellsAddCmd.SetOut(&bytes.Buffer{})
	if err := runShellsAdd(shellsAddCmd, []string{"infra"}); err != nil {
		t.Fatalf("seed add: %v", err)
	}

	shellsRemovePurge = true
	out := &bytes.Buffer{}
	shellsRemoveCmd.SetOut(out)
	if err := runShellsRemove(shellsRemoveCmd, []string{"infra"}); err != nil {
		t.Fatalf("runShellsRemove: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(home, ".toolbox.yaml"))
	if err != nil {
		t.Fatalf("read global: %v", err)
	}
	if strings.Contains(string(body), "infra") {
		t.Errorf("entry must be removed:\n%s", body)
	}
	if _, err := os.Stat(workdir); !os.IsNotExist(err) {
		t.Errorf("--purge-dir must remove %s", workdir)
	}
	if !strings.Contains(out.String(), workdir+": removed") {
		t.Errorf("purge must be reported, got: %s", out.String())
	}
}

func TestShellsRemoveUnknownNameSuggests(t *testing.T) {
	resetShellsFlags(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, ".toolbox.yaml"),
		[]byte("shells:\n  scratch:\n    path: /tmp/s\n"), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	err := runShellsRemove(shellsRemoveCmd, []string{"scrach"})
	if err == nil || !strings.Contains(err.Error(), `did you mean "scratch"?`) {
		t.Fatalf("expected suggestion, got: %v", err)
	}
	if _, ok := errors.AsType[*usageError](err); !ok {
		t.Errorf("unknown name must be a usage error, got %T", err)
	}
}

func TestShellsGetUnknownNameIsUsageError(t *testing.T) {
	withCfg(t, &config.Config{Shells: map[string]config.NamedShell{"infra": {Path: "/tmp/infra"}}})

	err := runShellsGet(shellsGetCmd, []string{"infar"})
	if err == nil {
		t.Fatal("unknown shell must error")
	}
	if _, ok := errors.AsType[*usageError](err); !ok {
		t.Errorf("unknown shell must be a usage error (exit 2), got %T", err)
	}
	if !strings.Contains(err.Error(), `did you mean "infra"?`) {
		t.Errorf("expected suggestion, got: %v", err)
	}
}

func TestShellsGetShowsEnv(t *testing.T) {
	withCfg(t, &config.Config{Shells: map[string]config.NamedShell{
		"infra": {Path: "/tmp/infra", Env: map[string]string{"B": "2", "A": "1"}},
	}})

	out := &bytes.Buffer{}
	shellsGetCmd.SetOut(out)
	if err := runShellsGet(shellsGetCmd, []string{"infra"}); err != nil {
		t.Fatalf("runShellsGet: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "path: /tmp/infra") {
		t.Errorf("missing path line, got: %s", got)
	}
	if !strings.Contains(got, "env:\n  A: 1\n  B: 2\n") {
		t.Errorf("env must render sorted, got: %s", got)
	}
}

func TestShellsListOutput(t *testing.T) {
	withCfg(t, &config.Config{Shells: map[string]config.NamedShell{
		"infra":  {Path: "/tmp/infra"},
		"qa-env": {Path: "/tmp/qa"},
	}})

	out := &bytes.Buffer{}
	shellsListCmd.SetOut(out)
	if err := runShellsList(shellsListCmd, nil); err != nil {
		t.Fatalf("runShellsList: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "infra   /tmp/infra") || !strings.Contains(got, "qa-env  /tmp/qa") {
		t.Errorf("unexpected list output:\n%s", got)
	}
}

func TestShellsListEmpty(t *testing.T) {
	withCfg(t, &config.Config{Shells: map[string]config.NamedShell{}})

	out := &bytes.Buffer{}
	shellsListCmd.SetOut(out)
	if err := runShellsList(shellsListCmd, nil); err != nil {
		t.Fatalf("runShellsList: %v", err)
	}
	if !strings.Contains(out.String(), "no named shells configured") {
		t.Errorf("empty list must hint at add, got: %s", out.String())
	}
}
