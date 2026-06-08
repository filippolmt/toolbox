package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/configedit"
)

func TestConfigPathOrdering(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".toolbox.yaml"), []byte("shell: zsh\n"), 0o600); err != nil {
		t.Fatalf("write global: %v", err)
	}
	t.Setenv("HOME", home)
	cwd := chdirTemp(t)
	if err := os.WriteFile(filepath.Join(cwd, ".toolbox.yaml"), []byte("mounts_root: /tmp/x\n"), 0o600); err != nil {
		t.Fatalf("write project: %v", err)
	}

	out := &bytes.Buffer{}
	configPathCmd.SetOut(out)
	if err := runConfigPath(configPathCmd, nil); err != nil {
		t.Fatalf("runConfigPath: %v", err)
	}
	got := out.String()

	projIdx := strings.Index(got, "project .toolbox.yaml")
	globalIdx := strings.Index(got, "global ~/.toolbox.yaml")
	envIdx := strings.Index(got, "TOOLBOX_* env")
	defIdx := strings.Index(got, "defaults")
	flagIdx := strings.Index(got, "--config")
	for label, idx := range map[string]int{"--config": flagIdx, "project": projIdx, "global": globalIdx, "env": envIdx, "defaults": defIdx} {
		if idx < 0 {
			t.Fatalf("layer %s missing from output:\n%s", label, got)
		}
	}
	ordered := flagIdx < projIdx && projIdx < globalIdx && globalIdx < envIdx && envIdx < defIdx
	if !ordered {
		t.Errorf("layers out of precedence order:\n%s", got)
	}
	if strings.Count(got, ".toolbox.yaml (found)") != 2 {
		t.Errorf("project and global layers must both show found markers:\n%s", got)
	}
	if !strings.Contains(got, "(not set)") {
		t.Errorf("absent --config must be listed as not set:\n%s", got)
	}
}

func TestConfigPathAbsentLayersListed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	chdirTemp(t)

	out := &bytes.Buffer{}
	configPathCmd.SetOut(out)
	if err := runConfigPath(configPathCmd, nil); err != nil {
		t.Fatalf("runConfigPath: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "(none found)") {
		t.Errorf("absent project layer must be listed, not omitted:\n%s", got)
	}
	if !strings.Contains(got, "(not present)") {
		t.Errorf("absent global layer must be listed, not omitted:\n%s", got)
	}
}

func TestConfigDoctorExitCodes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd := chdirTemp(t)

	out := &bytes.Buffer{}
	configDoctorCmd.SetOut(out)
	if err := runConfigDoctor(configDoctorCmd, nil); err != nil {
		t.Fatalf("clean config must exit 0, got: %v", err)
	}
	if !strings.Contains(out.String(), "no findings") {
		t.Errorf("clean run must report no findings, got: %s", out.String())
	}

	// Error-severity finding → non-nil (exit 1).
	if err := os.WriteFile(filepath.Join(cwd, ".toolbox.yaml"),
		[]byte("shells:\n  infra:\n    path: \"\"\n"), 0o600); err != nil {
		t.Fatalf("write project: %v", err)
	}
	out.Reset()
	err := runConfigDoctor(configDoctorCmd, nil)
	if err == nil {
		t.Fatal("error finding must exit non-zero")
	}
	if _, ok := errors.AsType[*usageError](err); ok {
		t.Error("doctor failure is a plain error (exit 1), not usage")
	}
	if !strings.Contains(out.String(), "errors:") {
		t.Errorf("findings must be grouped under errors:, got: %s", out.String())
	}

	// Warning-only finding → nil (exit 0).
	if err := os.WriteFile(filepath.Join(cwd, ".toolbox.yaml"),
		[]byte("mont_root: /tmp/x\n"), 0o600); err != nil {
		t.Fatalf("write project: %v", err)
	}
	out.Reset()
	if err := runConfigDoctor(configDoctorCmd, nil); err != nil {
		t.Fatalf("warning-only config must exit 0, got: %v", err)
	}
	if !strings.Contains(out.String(), "warnings:") {
		t.Errorf("warning must be reported, got: %s", out.String())
	}
}

// TestConfigShowDefaultOutputUnchanged is the golden guard for D4: the
// no-flag renderer and the origin renderer with nil provenance produce
// byte-identical output.
func TestConfigShowDefaultOutputUnchanged(t *testing.T) {
	c := &config.Config{
		Shell:           "zsh",
		InheritHostAuth: []string{"gh"},
		Mounts: []config.Mount{
			{Name: "extra", Source: "/tmp/x", Target: "/mnt/x", ReadOnly: true},
		},
	}

	var plain, viaOrigin bytes.Buffer
	if err := writeResolvedConfig(&plain, c); err != nil {
		t.Fatalf("writeResolvedConfig: %v", err)
	}
	if err := writeResolvedConfigWithOrigin(&viaOrigin, c, nil, ""); err != nil {
		t.Fatalf("writeResolvedConfigWithOrigin: %v", err)
	}
	if plain.String() != viaOrigin.String() {
		t.Errorf("nil-prov origin renderer must match plain renderer:\n%q\nvs\n%q", plain.String(), viaOrigin.String())
	}
	want := "shell: zsh\nimage: \"\"\nregistry_mirror: \"\"\npull: auto\nmounts_root: \"\"\ninherit_host_auth:\n  - gh\nmounts:\n  - name: extra\n    source: /tmp/x\n    target: /mnt/x\n    readonly: true\n"
	if plain.String() != want {
		t.Errorf("default output drifted:\n%q\nwant\n%q", plain.String(), want)
	}
}

func TestConfigShowOriginAnnotations(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".toolbox.yaml"),
		[]byte("inherit_host_auth: [gh]\n"), 0o600); err != nil {
		t.Fatalf("write global: %v", err)
	}
	t.Setenv("HOME", home)
	cwd := chdirTemp(t)
	if err := os.WriteFile(filepath.Join(cwd, ".toolbox.yaml"),
		[]byte("mounts_root: /tmp/root\nmounts:\n  - name: scratch\n    source: ~/s\n    target: /s\n"), 0o600); err != nil {
		t.Fatalf("write project: %v", err)
	}

	resolved, err := config.Plan(cwd, "")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	prov, err := configedit.Compute(cwd, "")
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	var buf bytes.Buffer
	if err := writeResolvedConfigWithOrigin(&buf, resolved, prov, ""); err != nil {
		t.Fatalf("writeResolvedConfigWithOrigin: %v", err)
	}
	got := buf.String()
	for _, want := range []string{
		"shell: zsh (default)",
		"mounts_root: /tmp/root (./.toolbox.yaml)",
		"inherit_host_auth: (~/.toolbox.yaml)",
		"- name: scratch (./.toolbox.yaml)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("origin output missing %q:\n%s", want, got)
		}
	}
}
