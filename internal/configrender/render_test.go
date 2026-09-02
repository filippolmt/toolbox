package configrender

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/configedit"
)

// chdirTemp switches to a fresh temp dir for the duration of the test and
// restores the previous working directory on cleanup.
func chdirTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	return dir
}

// fullFixture is the every-field-set config shared by the coverage and pin
// tests.
func fullFixture() *config.Config {
	yes := true
	return &config.Config{
		Shell:             "zsh",
		Agent:             "codex",
		Image:             "img",
		RegistryMirror:    "mirror",
		Pull:              "always",
		MountsRoot:        "~/r",
		Bridge:            &yes,
		Proximo:           &yes,
		ManagedStatusline: &yes,
		SDD:               map[string]config.SDDSkill{"gsd": {Enabled: true}},
		Env:               map[string]string{"FOO": "bar"},
		Worktree:          config.WorktreeConfig{Seed: []string{".env"}},
		InheritHostAuth:   []string{"gh"},
		Shells:            map[string]config.NamedShell{"infra": {Path: "/tmp/infra"}},
		Mounts:            []config.Mount{{Name: "extra", Source: "/tmp/x", Target: "/mnt/x"}},
	}
}

// TestConfigShowDefaultOutputUnchanged is the golden guard: the no-flag renderer
// and the origin renderer with nil provenance produce byte-identical output.
func TestConfigShowDefaultOutputUnchanged(t *testing.T) {
	c := &config.Config{
		Shell:           "zsh",
		InheritHostAuth: []string{"gh"},
		Mounts: []config.Mount{
			{Name: "extra", Source: "/tmp/x", Target: "/mnt/x", ReadOnly: true},
		},
	}

	var plain, viaOrigin bytes.Buffer
	if err := Resolved(&plain, c); err != nil {
		t.Fatalf("Resolved: %v", err)
	}
	if err := ResolvedWithOrigin(&viaOrigin, c, nil, ""); err != nil {
		t.Fatalf("ResolvedWithOrigin: %v", err)
	}
	if plain.String() != viaOrigin.String() {
		t.Errorf("nil-prov origin renderer must match plain renderer:\n%q\nvs\n%q", plain.String(), viaOrigin.String())
	}
	want := "shell: zsh\nagent: claude\nimage: \"\"\nregistry_mirror: \"\"\npull: auto\nmounts_root: \"\"\nbridge: auto\nproximo: auto\nmanaged_statusline: auto\nimage_reclaim: auto\npeer_messaging: false\nsdd: {}\nenv: {}\nworktree:\n  seed: []\ninherit_host_auth:\n  - gh\nshells: {}\nmounts:\n  - name: extra\n    source: /tmp/x\n    target: /mnt/x\n    readonly: true\n"
	if plain.String() != want {
		t.Errorf("default output drifted:\n%q\nwant\n%q", plain.String(), want)
	}
}

// TestConfigShowCoversSchema is the anti-drift guard for the resolved renderer:
// every config.SchemaKeys() field must be rendered, except the deprecated
// browser_bridge alias (only the canonical bridge is shown). A new Config field
// that the renderer forgets turns this red.
func TestConfigShowCoversSchema(t *testing.T) {
	var buf bytes.Buffer
	if err := Resolved(&buf, fullFixture()); err != nil {
		t.Fatalf("Resolved: %v", err)
	}
	out := buf.String()

	const skip = "browser_bridge" // deprecated alias, rendered only as bridge
	for _, key := range config.SchemaKeys() {
		if key == skip {
			if strings.Contains(out, "\n"+key+":") || strings.HasPrefix(out, key+":") {
				t.Errorf("deprecated key %q must not be rendered", key)
			}
			continue
		}
		if !strings.Contains(out, "\n"+key+":") && !strings.HasPrefix(out, key+":") {
			t.Errorf("config show is missing key %q:\n%s", key, out)
		}
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
	if err := ResolvedWithOrigin(&buf, resolved, prov, ""); err != nil {
		t.Fatalf("ResolvedWithOrigin: %v", err)
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

func TestWriteResolvedConfigDeterministic(t *testing.T) {
	c := &config.Config{
		Shell:           "zsh",
		MountsRoot:      "~/work-toolbox",
		InheritHostAuth: []string{"gh", "gcloud"},
		Mounts: []config.Mount{
			{Name: "claude", Source: "~/work/.claude"},
			{Name: "extra", Source: "/tmp/x", Target: "/mnt/x", ReadOnly: true},
		},
	}

	var buf bytes.Buffer
	if err := Resolved(&buf, c); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := buf.String()

	for _, want := range []string{
		"shell: zsh\n",
		"mounts_root: ~/work-toolbox\n",
		"inherit_host_auth:\n",
		"  - gh\n",
		"  - gcloud\n",
		"  - name: claude\n",
		"    source: ~/work/.claude\n",
		"  - name: extra\n",
		"    target: /mnt/x\n",
		"    readonly: true\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\n---\n%s", want, got)
		}
	}
}

func TestWriteResolvedConfigEmptyMounts(t *testing.T) {
	c := &config.Config{Shell: "zsh"}
	var buf bytes.Buffer
	if err := Resolved(&buf, c); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "mounts: []\n") {
		t.Errorf("empty mounts should render as `mounts: []`, got:\n%s", got)
	}
	if !strings.Contains(got, `mounts_root: ""`) {
		t.Errorf("empty mounts_root should render as quoted empty, got:\n%s", got)
	}
	if !strings.Contains(got, "inherit_host_auth: []\n") {
		t.Errorf("empty InheritHostAuth should render as `inherit_host_auth: []`, got:\n%s", got)
	}
}

func TestWriteResolvedConfigNilConfigErrors(t *testing.T) {
	var buf bytes.Buffer
	if err := Resolved(&buf, nil); err == nil {
		t.Fatal("expected error for nil config")
	}
}
