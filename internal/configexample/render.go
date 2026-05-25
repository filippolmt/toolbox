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

	b.WriteString("# Login shell inside the container. Only zsh is supported.\n")
	b.WriteString("# shell: zsh\n")
	b.WriteString("\n")

	b.WriteString("# sdd — repo-local Spec-Driven-Development skill packs.\n")
	b.WriteString("# Each key flips one integration on; default is false (no install).\n")
	b.WriteString("# On the next `toolbox shell` the entrypoint installs the pinned npm\n")
	b.WriteString("# package and runs the upstream initialiser inside /workspace.\n")
	b.WriteString("# Use `toolbox sdd init <name>` to flip a key on AND patch .gitignore.\n")
	b.WriteString("# Supported keys come from internal/sdd.Skills (Renovate-bumped).\n")
	b.WriteString("# sdd:\n")
	b.WriteString("#   gsd: true        # @opengsd/get-shit-done-redux --claude --local (unconditional)\n")
	b.WriteString("#   bmad: true       # bmad-method install --yes (ONLY when _bmad/ exists)\n")
	b.WriteString("#   openspec: true   # openspec init --tools=claude,codex --force, then openspec update\n")
	b.WriteString("# bmad bootstrap requires a one-time manual `npx bmad-method install`\n")
	b.WriteString("# (interactive). After committing _bmad/, the entrypoint auto-upgrades on\n")
	b.WriteString("# every shell. Missing _bmad/ logs a skip message instead of aborting.\n")
	b.WriteString("\n")

	b.WriteString("# shells — reusable named workspaces for `toolbox shell <name>`.\n")
	b.WriteString("# Each path must be absolute. toolbox bind-mounts path -> path and starts\n")
	b.WriteString("# the shell in that directory.\n")
	b.WriteString("# shells:\n")
	b.WriteString("#   infra:\n")
	b.WriteString("#     path: /tmp/infra\n")
	b.WriteString("\n")

	b.WriteString("# Retarget every default mount whose source lives under ~/.toolbox/ to the\n")
	b.WriteString("# given prefix. Must be absolute (/path) or strictly home-relative (~/sub).\n")
	b.WriteString("# Bare \"~\" is rejected — it would defeat credential isolation.\n")
	b.WriteString("# mounts_root: ~/work-toolbox\n")
	b.WriteString("\n")

	b.WriteString("# inherit_host_auth — share host credentials with the container (read-only).\n")
	b.WriteString("# Listed CLIs read the host's standard credential path instead of the\n")
	b.WriteString("# isolated ~/.toolbox/<key>/ default. Default: [] (fully isolated).\n")
	b.WriteString("# Eligible keys (have a stable host credential path):\n")
	for _, k := range catalog.HostAuthEligibleKeys() {
		entry, ok := catalog.Find(k)
		if !ok || entry.HostAuthMount == nil {
			continue
		}
		fmt.Fprintf(&b, "#   - %-7s  %s -> %s\n", k, entry.HostAuthMount.HostPath, entry.HostAuthMount.ContainerPath)
	}
	b.WriteString("# inherit_host_auth: [gh, gcloud]\n")
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
