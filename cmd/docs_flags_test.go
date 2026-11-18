package cmd

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// flagRow matches a flag-table row in docs/commands.md and captures the long
// flag name: "| `-p, --publish <spec>` |" → "publish".
var flagRow = regexp.MustCompile("^\\| `(?:-\\w, )?--([a-z0-9-]+)")

// commandSection is one `## toolbox <cmd>` section of docs/commands.md: its
// text, `###` subsections included, and the long flags its flag table lists.
type commandSection struct {
	text string
	rows []string
}

func commandSections(t *testing.T) map[string]*commandSection {
	t.Helper()
	raw, err := os.ReadFile("../docs/commands.md")
	if err != nil {
		t.Fatalf("read docs/commands.md: %v", err)
	}
	sections := map[string]*commandSection{}
	var cur *commandSection
	fenced := false
	for line := range strings.SplitSeq(string(raw), "\n") {
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
		} else if !fenced && strings.HasPrefix(line, "## ") {
			cur = nil
			if name, ok := strings.CutPrefix(line, "## toolbox "); ok {
				cur = &commandSection{}
				sections[name] = cur
			}
			continue
		}
		if cur == nil {
			continue
		}
		cur.text += line + "\n"
		if m := flagRow.FindStringSubmatch(line); m != nil && !fenced {
			cur.rows = append(cur.rows, m[1])
		}
	}
	return sections
}

// ownFlags returns the visible flags c defines itself. Flags() and
// PersistentFlags() hold those; the merged views (LocalFlags,
// NonInheritedFlags) copy the parents' persistent flags into c and leak into
// every later test that walks it.
func ownFlags(c *cobra.Command) []string {
	var flags []string
	visit := func(f *pflag.Flag) {
		if !f.Hidden && f.Name != "help" && !slices.Contains(flags, f.Name) {
			flags = append(flags, f.Name)
		}
	}
	c.Flags().VisitAll(visit)
	c.PersistentFlags().VisitAll(visit)
	return flags
}

// TestCommandsDocFlagTablesMatchTheirCommand holds each flag table in
// docs/commands.md to the command its section names: every row is a flag of
// that command, and every flag of that command has a row. A row pasted under
// the wrong command (a merge resolving two sections) fails here, where no link
// check can see it.
//
// shortcut: a section without a table is skipped, so its command's flags go
// unchecked (init, config, mounts, shells document theirs in prose); require a
// table per command if those drift.
func TestCommandsDocFlagTablesMatchTheirCommand(t *testing.T) {
	tables := 0
	for name, s := range commandSections(t) {
		if len(s.rows) == 0 {
			continue
		}
		tables++
		c, _, err := rootCmd.Find(strings.Fields(name))
		if err != nil || c == rootCmd {
			t.Errorf("## toolbox %s: no such command", name)
			continue
		}
		flags := ownFlags(c)
		for _, r := range s.rows {
			if !slices.Contains(flags, r) {
				t.Errorf("## toolbox %s lists --%s, which is not a flag of toolbox %s", name, r, name)
			}
		}
		for _, f := range flags {
			if !slices.Contains(s.rows, f) {
				t.Errorf("toolbox %s has --%s, missing from its flag table in docs/commands.md", name, f)
			}
		}
	}
	if tables == 0 {
		t.Fatal("docs/commands.md has no flag table: the row pattern no longer matches")
	}
}
