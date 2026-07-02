package configedit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/filippolmt/toolbox/internal/config"
)

// writeLayeredFixture seeds a global ~/.toolbox.yaml and a project
// .toolbox.yaml in a fresh cwd, returning the cwd. HOME is faked per test.
func writeLayeredFixture(t *testing.T, globalYAML, projectYAML string) string {
	t.Helper()
	home := t.TempDir()
	if globalYAML != "" {
		if err := os.WriteFile(filepath.Join(home, ".toolbox.yaml"), []byte(globalYAML), 0o600); err != nil {
			t.Fatalf("write global: %v", err)
		}
	}
	t.Setenv("HOME", home)
	cwd := t.TempDir()
	if projectYAML != "" {
		if err := os.WriteFile(filepath.Join(cwd, ".toolbox.yaml"), []byte(projectYAML), 0o600); err != nil {
			t.Fatalf("write project: %v", err)
		}
	}
	return cwd
}

func TestComputeLayeredOrigins(t *testing.T) {
	cwd := writeLayeredFixture(t,
		"inherit_host_auth: [gh]\nshells:\n  infra:\n    path: /tmp/infra\n",
		"mounts_root: /tmp/root\nmounts:\n  - name: scratch\n    source: ~/s\n    target: /s\n")

	prov, err := Compute(cwd, "")
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	cases := map[string]Origin{
		"inherit_host_auth": OriginGlobal,
		"shells.infra":      OriginGlobal,
		"mounts_root":       OriginProject,
		"mounts.scratch":    OriginProject,
		"shell":             OriginDefault, // set by neither file
		"browser_bridge":    OriginDefault,
	}
	for key, want := range cases {
		if got := prov[key]; got != want {
			t.Errorf("prov[%q] = %v, want %v", key, got, want)
		}
	}
}

// TestDiffLayerCoversSchema is the anti-drift guard for provenance, symmetric
// to the renderer/example/validation coverage tests: the reflection walk must
// attribute every config.SchemaKeys() field (agent and managed_statusline were
// the fields the old hand-written diffLayer silently dropped). shells/mounts
// are attributed per entry and asserted separately. A new Config field left
// out of the fully-populated fixture turns this red.
func TestDiffLayerCoversSchema(t *testing.T) {
	yes := true
	full := &config.Config{
		Shell:             "zsh",
		Agent:             "codex",
		Image:             "img",
		RegistryMirror:    "mirror",
		Pull:              "always",
		MountsRoot:        "~/r",
		Bridge:            &yes,
		BrowserBridge:     &yes,
		Proximo:           &yes,
		ManagedStatusline: &yes,
		SDD:               map[string]config.SDDSkill{"gsd": {Enabled: true}},
		Env:               map[string]string{"FOO": "bar"},
		Worktree:          config.WorktreeConfig{Seed: []string{".env"}},
		InheritHostAuth:   []string{"gh"},
		Shells:            map[string]config.NamedShell{"infra": {Path: "/tmp/infra"}},
		Mounts:            []config.Mount{{Name: "extra", Source: "/tmp/x"}},
	}
	prov := Provenance{}
	diffLayer(prov, &config.Config{}, full, OriginProject)

	for _, key := range config.SchemaKeys() {
		if perEntryDiffKeys[key] {
			continue // attributed per entry (shells.<name>/mounts.<name>), checked below
		}
		if prov[key] != OriginProject {
			t.Errorf("diffLayer did not attribute key %q — the reflection walk must cover every field", key)
		}
	}
	if prov[ShellKey("infra")] != OriginProject {
		t.Errorf("per-entry shells attribution lost: shells.infra not attributed")
	}
	if prov[MountKey("extra")] != OriginProject {
		t.Errorf("per-entry mounts attribution lost: mounts.extra not attributed")
	}
}

func TestComputeProjectOverridesGlobal(t *testing.T) {
	cwd := writeLayeredFixture(t,
		"mounts_root: /tmp/from-global\n",
		"mounts_root: /tmp/from-project\n")

	prov, err := Compute(cwd, "")
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if got := prov["mounts_root"]; got != OriginProject {
		t.Errorf("project layer must win attribution: got %v", got)
	}
}

func TestComputeExplicitShortCircuits(t *testing.T) {
	cwd := writeLayeredFixture(t,
		"mounts_root: /tmp/from-global\n",
		"inherit_host_auth: [gh]\n")
	explicit := filepath.Join(t.TempDir(), "custom.yaml")
	if err := os.WriteFile(explicit, []byte("mounts_root: /tmp/explicit\n"), 0o600); err != nil {
		t.Fatalf("write explicit: %v", err)
	}

	prov, err := Compute(cwd, explicit)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if got := prov["mounts_root"]; got != OriginExplicit {
		t.Errorf("prov[mounts_root] = %v, want OriginExplicit", got)
	}
	// Global / project keys must not leak in under --config.
	if got := prov["inherit_host_auth"]; got != OriginDefault {
		t.Errorf("prov[inherit_host_auth] = %v, want OriginDefault (short-circuit)", got)
	}
}

func TestOriginLabels(t *testing.T) {
	cases := map[Origin]string{
		OriginDefault:  "(default)",
		OriginGlobal:   "(~/.toolbox.yaml)",
		OriginProject:  "(./.toolbox.yaml)",
		OriginExplicit: "(--config)",
	}
	for o, want := range cases {
		if got := o.Label(); got != want {
			t.Errorf("Label(%v) = %q, want %q", o, got, want)
		}
	}
	if got := OriginExplicit.LabelWithPath("/tmp/c.yaml"); got != "(--config /tmp/c.yaml)" {
		t.Errorf("LabelWithPath = %q", got)
	}
	if got := OriginGlobal.LabelWithPath("/tmp/c.yaml"); got != "(~/.toolbox.yaml)" {
		t.Errorf("LabelWithPath on non-explicit = %q", got)
	}
}
