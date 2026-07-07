package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/container"
)

func TestRenderListEmpty(t *testing.T) {
	var buf bytes.Buffer
	renderList(&buf, nil)
	if got := buf.String(); got != "No toolbox containers.\n" {
		t.Errorf("empty render = %q", got)
	}
}

func TestRenderListTable(t *testing.T) {
	var buf bytes.Buffer
	renderList(&buf, []container.Item{
		{Name: "toolbox-x-1", Workspace: "/home/u/x", Status: "Up 2 hours"},
		{Name: "toolbox-longer-name-2", Workspace: "-", Status: "Exited (0)"},
	})
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("want header + 2 rows, got %d lines: %q", len(lines), buf.String())
	}
	if !strings.HasPrefix(lines[0], "NAME") || !strings.Contains(lines[0], "WORKSPACE") || !strings.HasSuffix(lines[0], "STATUS") {
		t.Errorf("header = %q", lines[0])
	}
	// Columns align: WORKSPACE starts at the same offset on header and rows.
	col := strings.Index(lines[0], "WORKSPACE")
	if !strings.HasPrefix(lines[1][col:], "/home/u/x") {
		t.Errorf("row 1 workspace misaligned at col %d: %q", col, lines[1])
	}
}
