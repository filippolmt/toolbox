package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setConfigSetFlag marks a config-set flag as Changed (RunE reads Changed,
// which cobra would set during real flag parsing) and records cleanup so the
// pflag state does not leak across tests.
func setConfigSetFlag(t *testing.T, name, value string) {
	t.Helper()
	if err := configSetCmd.Flags().Set(name, value); err != nil {
		t.Fatalf("set --%s: %v", name, err)
	}
	t.Cleanup(func() {
		configSetImage, configSetRegistryMirror, configSetPull, configSetWhere = "", "", "", "global"
		for _, n := range []string{"image", "registry-mirror", "pull", "where"} {
			if f := configSetCmd.Flags().Lookup(n); f != nil {
				f.Changed = false
			}
		}
	})
}

func TestConfigSetRegistryMirrorGlobal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	setConfigSetFlag(t, "registry-mirror", "harbor.corp.io/ghcr-proxy")

	out := &bytes.Buffer{}
	configSetCmd.SetOut(out)
	if err := runConfigSet(configSetCmd, nil); err != nil {
		t.Fatalf("runConfigSet: %v", err)
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
	if !strings.Contains(got, "registry_mirror: harbor.corp.io/ghcr-proxy") {
		t.Errorf("missing registry_mirror key:\n%s", got)
	}
	if !strings.Contains(out.String(), cfgPath+": created") {
		t.Errorf("report must say created, got: %s", out.String())
	}
}

func TestConfigSetNoFlagsErrors(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	configSetCmd.SetOut(&bytes.Buffer{})
	err := runConfigSet(configSetCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "at least one of") {
		t.Fatalf("runConfigSet with no flags = %v, want usage error", err)
	}
}

func TestConfigSetInvalidPullRejected(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	setConfigSetFlag(t, "pull", "sometimes")
	configSetCmd.SetOut(&bytes.Buffer{})
	err := runConfigSet(configSetCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "pull policy") {
		t.Fatalf("runConfigSet bad pull = %v, want validation error", err)
	}
}

func TestConfigSetEmptyImageRemovesKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgPath := filepath.Join(home, ".toolbox.yaml")
	if err := os.WriteFile(cfgPath, []byte("image: ghcr.io/x/y:1\nshell: zsh\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	setConfigSetFlag(t, "image", "")

	configSetCmd.SetOut(&bytes.Buffer{})
	if err := runConfigSet(configSetCmd, nil); err != nil {
		t.Fatalf("runConfigSet: %v", err)
	}
	body, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(body), "image:") {
		t.Errorf("empty --image must remove the key, got:\n%s", body)
	}
	if !strings.Contains(string(body), "shell: zsh") {
		t.Errorf("sibling keys must survive, got:\n%s", body)
	}
}
