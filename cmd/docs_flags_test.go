package cmd

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

// flagRow matches a flag-table row in docs/commands.md and captures the long
// flag name: "| `-p, --publish <spec>` |" → "publish".
var flagRow = regexp.MustCompile("^\\| `(?:-\\w, )?--([a-z0-9-]+)")

// commandFlagTables returns, per `## toolbox <cmd>` section of
// docs/commands.md, the long flags its flag table lists. Sections without a
// table are absent.
func commandFlagTables(t *testing.T) map[string][]string {
	t.Helper()
	raw, err := os.ReadFile("../docs/commands.md")
	if err != nil {
		t.Fatalf("read docs/commands.md: %v", err)
	}
	tables := map[string][]string{}
	section := ""
	fenced := false
	for line := range strings.SplitSeq(string(raw), "\n") {
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		if strings.HasPrefix(line, "## ") {
			section = ""
			if name, ok := strings.CutPrefix(line, "## toolbox "); ok {
				section = name
			}
			continue
		}
		if m := flagRow.FindStringSubmatch(line); m != nil && section != "" {
			tables[section] = append(tables[section], m[1])
		}
	}
	return tables
}

// TestCommandsDocFlagTablesMatchTheirCommand holds each flag table in
// docs/commands.md to the command its section names: every row is a flag of
// that command, and every flag of that command has a row. A row pasted under
// the wrong command (a merge resolving two sections) fails here, where no link
// check can see it.
func TestCommandsDocFlagTablesMatchTheirCommand(t *testing.T) {
	tables := commandFlagTables(t)
	if len(tables) == 0 {
		t.Fatal("docs/commands.md has no flag table: the row pattern no longer matches")
	}
	for section, rows := range tables {
		c, _, err := rootCmd.Find([]string{section})
		if err != nil || c == rootCmd {
			t.Errorf("## toolbox %s: no such command", section)
			continue
		}
		var flags []string
		c.NonInheritedFlags().VisitAll(func(f *pflag.Flag) {
			if !f.Hidden && f.Name != "help" {
				flags = append(flags, f.Name)
			}
		})
		for _, r := range rows {
			if !slices.Contains(flags, r) {
				t.Errorf("## toolbox %s lists --%s, which is not a flag of toolbox %s", section, r, section)
			}
		}
		for _, f := range flags {
			if !slices.Contains(rows, f) {
				t.Errorf("toolbox %s has --%s, missing from its flag table in docs/commands.md", section, f)
			}
		}
	}
}
