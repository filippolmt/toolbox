package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/catalog"
	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/mountplan"
)

func TestRenderExampleYAMLContainsAllToolsAndMounts(t *testing.T) {
	got := renderExampleYAML()

	for _, want := range []string{"shell:", "mounts_root:", "tools:", "mounts:", "Precedence"} {
		if !strings.Contains(got, want) {
			t.Errorf("template missing %q", want)
		}
	}
	for _, e := range catalog.Entries {
		if !strings.Contains(got, e.Key+":") {
			t.Errorf("template missing tool key %q", e.Key)
		}
		if !strings.Contains(got, e.BuildArg) {
			t.Errorf("template missing build arg %q", e.BuildArg)
		}
	}
	for _, m := range mountplan.Defaults() {
		if !strings.Contains(got, m.Name) {
			t.Errorf("template missing mount name %q", m.Name)
		}
	}
}

func TestWriteResolvedConfigDeterministic(t *testing.T) {
	c := &config.Config{
		Shell:      "bash",
		MountsRoot: "~/work-toolbox",
		Tools:      map[string]bool{"gh": true, "azure": false, "go": true},
		Mounts: []config.Mount{
			{Name: "claude", Source: "~/work/.claude"},
			{Name: "extra", Source: "/tmp/x", Target: "/mnt/x", ReadOnly: true},
		},
	}

	var buf bytes.Buffer
	if err := writeResolvedConfig(&buf, c); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := buf.String()

	for _, want := range []string{
		"shell: bash\n",
		"mounts_root: ~/work-toolbox\n",
		"tools:\n",
		"  azure: false\n",
		"  gh: true\n",
		"  go: true\n",
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

	// tools must be sorted: azure < gh < go
	if idxAzure, idxGh := strings.Index(got, "azure:"), strings.Index(got, "gh:"); idxAzure >= idxGh {
		t.Errorf("tools not sorted alphabetically:\n%s", got)
	}
}

func TestWriteResolvedConfigEmptyMounts(t *testing.T) {
	c := &config.Config{Shell: "zsh", Tools: map[string]bool{}}
	var buf bytes.Buffer
	if err := writeResolvedConfig(&buf, c); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "mounts: []\n") {
		t.Errorf("empty mounts should render as `mounts: []`, got:\n%s", got)
	}
	if !strings.Contains(got, `mounts_root: ""`) {
		t.Errorf("empty mounts_root should render as quoted empty, got:\n%s", got)
	}
}

func TestWriteResolvedConfigNilConfigErrors(t *testing.T) {
	var buf bytes.Buffer
	if err := writeResolvedConfig(&buf, nil); err == nil {
		t.Fatal("expected error for nil config")
	}
}
