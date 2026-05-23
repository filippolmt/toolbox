package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/config"
)

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
	if err := writeResolvedConfig(&buf, c); err != nil {
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
	if !strings.Contains(got, "inherit_host_auth: []\n") {
		t.Errorf("empty InheritHostAuth should render as `inherit_host_auth: []`, got:\n%s", got)
	}
}

func TestWriteResolvedConfigNilConfigErrors(t *testing.T) {
	var buf bytes.Buffer
	if err := writeResolvedConfig(&buf, nil); err == nil {
		t.Fatal("expected error for nil config")
	}
}
