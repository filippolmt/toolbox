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
// A `### toolbox <cmd> <sub>` subsection is a section of its own for rows: its
// table belongs to the subcommand, its text to the top-level section.
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
	return parseCommandSections(string(raw))
}

func parseCommandSections(raw string) map[string]*commandSection {
	sections := map[string]*commandSection{}
	var cur, rowsTo *commandSection
	fenced := false
	for line := range strings.SplitSeq(raw, "\n") {
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
		} else if !fenced && strings.HasPrefix(line, "## ") {
			cur = nil
			name, ok := strings.CutPrefix(line, "## toolbox ")
			if strings.HasPrefix(line, "## Global flag") {
				name, ok = "", true // the root command's own flags
			}
			if ok {
				cur = &commandSection{}
				sections[name] = cur
			}
			rowsTo = cur
			continue
		} else if !fenced && cur != nil && strings.HasPrefix(line, "### ") {
			rowsTo = cur // a plain ### subsection's table is the command's
			if name, ok := strings.CutPrefix(line, "### toolbox "); ok {
				rowsTo = &commandSection{}
				sections[name] = rowsTo
			}
		}
		if cur == nil {
			continue
		}
		// A code example counts as a mention; only a table row outside a
		// fence counts as a row.
		cur.text += line + "\n"
		if m := flagRow.FindStringSubmatch(line); m != nil && !fenced {
			rowsTo.rows = append(rowsTo.rows, m[1])
		}
	}
	return sections
}

// ownFlags returns the visible flags c defines itself. Executing a command
// (other tests do) copies its parents' persistent flags into c.Flags(), and so
// do the merged views (LocalFlags, NonInheritedFlags), so an ancestor's
// persistent flag is filtered out by walking the parents read-only.
func ownFlags(c *cobra.Command) []string {
	inherited := func(name string) bool {
		for p := c.Parent(); p != nil; p = p.Parent() {
			if p.PersistentFlags().Lookup(name) != nil {
				return true
			}
		}
		return false
	}
	var flags []string
	visit := func(f *pflag.Flag) {
		if !f.Hidden && f.Name != "help" && !inherited(f.Name) && !slices.Contains(flags, f.Name) {
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
func TestCommandsDocFlagTablesMatchTheirCommand(t *testing.T) {
	t.Run("a ### subsection table belongs to its subcommand", func(t *testing.T) {
		got := parseCommandSections("## toolbox config\n\n### toolbox config set\n\n| `--image` | x |\n\n### Notes\n\n| `--origin` | x |\n\n## toolbox stop\n\n| `--all` | x |\n")
		if rows := got["config set"]; rows == nil || !slices.Equal(rows.rows, []string{"image"}) {
			t.Errorf("config set rows = %v, want [image]", rows)
		}
		if rows := got["config"].rows; !slices.Equal(rows, []string{"origin"}) {
			t.Errorf("config rows = %v, want [origin] (the plain ### subsection's)", rows)
		}
		if rows := got["stop"].rows; !slices.Equal(rows, []string{"all"}) {
			t.Errorf("stop rows = %v, want [all]", rows)
		}
	})

	tables := 0
	for name, s := range commandSections(t) {
		if len(s.rows) == 0 || name == "" {
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

// namesWord reports whether text names word as a whole token: "--path" is not
// named by "--path-x", nor "set" by "reset".
func namesWord(text, word string) bool {
	for i := 0; ; {
		j := strings.Index(text[i:], word)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(word)
		before := start == 0 || !isTokenChar(text[start-1])
		after := end == len(text) || !isTokenChar(text[end])
		if before && after {
			return true
		}
		i = start + 1
	}
}

func isTokenChar(b byte) bool {
	return b == '-' || b >= 'a' && b <= 'z' || b >= '0' && b <= '9'
}

// flagNamed reports whether docs/commands.md names flag f of c, given c's
// top-level section name ("" for the root). A top-level command's flag (and
// the root's, under "## Global flag") counts anywhere in its section; a
// subcommand's in its own `### toolbox <cmd> <sub>` table or on a line of the
// top-level section that also names the subcommand, so `config show --where`
// is not vouched for by a `set [--where]` row.
func flagNamed(sections map[string]*commandSection, top string, c *cobra.Command, f string) bool {
	s, ok := sections[top]
	flag := "--" + f
	if !ok || !namesWord(s.text, flag) {
		return false
	}
	if c.Parent() == nil || c.Parent() == rootCmd {
		return true
	}
	if own, ok := sections[strings.TrimPrefix(c.CommandPath(), rootCmd.Name()+" ")]; ok && slices.Contains(own.rows, f) {
		return true
	}
	return slices.ContainsFunc(strings.Split(s.text, "\n"), func(line string) bool {
		return namesWord(line, flag) && namesWord(line, c.Name())
	})
}

// TestCommandsDocMentionsEveryFlag covers the commands a table does not: every
// visible flag of every command is named for it in docs/commands.md
// (flagNamed).
func TestCommandsDocMentionsEveryFlag(t *testing.T) {
	t.Run("a subcommand's ### table names its flags", func(t *testing.T) {
		set, _, err := rootCmd.Find([]string{"config", "set"})
		if err != nil {
			t.Fatalf("find config set: %v", err)
		}
		got := parseCommandSections("## toolbox config\n\n### toolbox config set\n\n| `--image` | x |\n")
		if !flagNamed(got, "config", set, "image") {
			t.Error("--image in the ### toolbox config set table: not named, want named")
		}
		if flagNamed(got, "config", set, "pull") {
			t.Error("--pull, absent from the section: named, want not named")
		}
	})

	sections := commandSections(t)
	check := func(top string, c *cobra.Command) {
		heading := "## toolbox " + top
		if top == "" {
			heading = "## Global flag"
		}
		for _, f := range ownFlags(c) {
			flag := "--" + f
			if !flagNamed(sections, top, c, f) {
				t.Errorf("%s has %s, not named for it under %s in docs/commands.md", c.CommandPath(), flag, heading)
			}
		}
	}
	var walk func(top string, c *cobra.Command)
	walk = func(top string, c *cobra.Command) {
		check(top, c)
		for _, sub := range c.Commands() {
			if !sub.Hidden {
				walk(top, sub)
			}
		}
	}
	check("", rootCmd)
	for _, c := range rootCmd.Commands() {
		if !c.Hidden && c.Name() != "help" {
			walk(c.Name(), c)
		}
	}
}
