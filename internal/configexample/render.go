// Package configexample renders the annotated .toolbox.yaml template
// printed by `toolbox config example` and written by `toolbox init`.
package configexample

import (
	"fmt"
	"strings"

	"github.com/filippolmt/toolbox/internal/catalog"
	"github.com/filippolmt/toolbox/internal/mountplan"
)

// Render returns an annotated `.toolbox.yaml` template covering every
// supported field. The output is generated from the live catalog and
// the canonical mount defaults so a new tool added to internal/catalog
// or a new mount added to internal/mountplan/defaults.go shows up here
// without a second edit.
func Render() string {
	var b strings.Builder

	b.WriteString("# .toolbox.yaml — toolbox configuration.\n")
	b.WriteString("#\n")
	b.WriteString("# Precedence (highest first):\n")
	b.WriteString("#   1. --config <file>\n")
	b.WriteString("#   2. nearest .toolbox.yaml walking up from CWD (project)\n")
	b.WriteString("#   3. ~/.toolbox.yaml (global)\n")
	b.WriteString("#   4. TOOLBOX_* env vars\n")
	b.WriteString("#   5. built-in defaults\n")
	b.WriteString("#\n")
	b.WriteString("# Every field below is optional. Uncomment only what you want to override.\n")
	b.WriteString("\n")

	b.WriteString("# Login shell inside the container. One of: zsh (default), bash.\n")
	b.WriteString("# shell: zsh\n")
	b.WriteString("\n")

	b.WriteString("# Retarget every default mount whose source lives under ~/.toolbox/ to the\n")
	b.WriteString("# given prefix. Must be absolute (/path) or strictly home-relative (~/sub).\n")
	b.WriteString("# Bare \"~\" is rejected — it would defeat credential isolation.\n")
	b.WriteString("# mounts_root: ~/work-toolbox\n")
	b.WriteString("\n")

	b.WriteString("# tools — bake CLIs into the image. Default: every entry true.\n")
	b.WriteString("# Setting any value to false rebuilds a local image (toolbox:local-<hash>)\n")
	b.WriteString("# without that tool. Adding/removing entries is hash-invalidating.\n")
	b.WriteString("tools:\n")
	for _, e := range catalog.Entries {
		fmt.Fprintf(&b, "  # %s: %t  # build arg: %s\n", e.Key, e.Default, e.BuildArg)
	}
	b.WriteString("\n")

	b.WriteString("# mounts — patch / replace / append host -> container binds.\n")
	b.WriteString("# Behaviour by `name`:\n")
	b.WriteString("#   - name matches a default + target empty  -> patch (only set fields override)\n")
	b.WriteString("#   - name matches a default + target set    -> replace (whole entry swapped)\n")
	b.WriteString("#   - name does NOT match a default          -> appended after defaults\n")
	b.WriteString("# Use `disabled: true` to opt a default out without redeclaring it.\n")
	b.WriteString("#\n")
	b.WriteString("# Canonical default-mount names (patch/replace targets):\n")
	for _, m := range mountplan.Defaults() {
		fmt.Fprintf(&b, "#   %-18s %s -> %s\n", m.Name, m.Source, m.Target)
	}
	b.WriteString("# mounts:\n")
	b.WriteString("#   - name: gh\n")
	b.WriteString("#     disabled: true              # drop the default gh mount\n")
	b.WriteString("#   - name: claude\n")
	b.WriteString("#     source: ~/work/.toolbox/.claude   # patch: retarget source only\n")
	b.WriteString("#   - name: extra-cache\n")
	b.WriteString("#     source: ~/work/cache               # append: brand new mount\n")
	b.WriteString("#     target: /home/toolbox/.cache/extra\n")
	b.WriteString("#     readonly: false\n")
	b.WriteString("#     create_if_missing: true\n")

	return b.String()
}
