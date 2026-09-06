// Package configexample renders the annotated .toolbox.yaml template
// printed by `toolbox config example` and written by `toolbox init`.
package configexample

import (
	"fmt"
	"strings"

	"github.com/filippolmt/toolbox/internal/catalog"
	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/mountplan"
)

// header is the preamble every template opens with: where the file is read
// from and in which order. It documents the load path, not any single key, so
// it is the one block that is not a key row's Example.
const header = `# .toolbox.yaml — toolbox configuration.
#
# Precedence (highest first):
#   1. --config <file>
#   2. nearest .toolbox.yaml walking up from CWD (project)
#   3. ~/.toolbox.yaml (global)
#   4. TOOLBOX_* env vars
#   5. built-in defaults
#
# Every field below is optional. Uncomment only what you want to override.
`

// listings supplies the live lists two Example blocks leave a
// config.ExampleListing marker for: the catalog CLIs eligible for host-auth
// inheritance and the canonical default mounts. They are generated rather than
// written down so a new catalog entry or a new mount in
// internal/mountplan/defaults.go shows up here without a second edit, and they
// live here rather than in the key row because internal/config cannot import
// mountplan (mountplan imports config).
var listings = map[string]func() string{
	"inherit_host_auth": hostAuthListing,
	"mounts":            mountListing,
}

// Render returns an annotated `.toolbox.yaml` template covering every config
// key: the header, then each key's own Example block in config.Keys() order.
// A deprecated alias carries no Example and is skipped — the live key it folds
// into is what the template documents.
func Render() string {
	var b strings.Builder
	b.WriteString(header)

	for _, k := range config.Keys() {
		if k.Example == "" {
			continue
		}
		block := k.Example
		if fill, ok := listings[k.Name]; ok {
			block = strings.Replace(block, config.ExampleListing, fill(), 1)
		}
		b.WriteString("\n")
		b.WriteString(block)
	}
	return b.String()
}

// hostAuthListing names every eligible CLI with the host path it would read.
func hostAuthListing() string {
	var b strings.Builder
	for _, k := range catalog.HostAuthEligibleKeys() {
		entry, ok := catalog.Find(k)
		if !ok || entry.HostAuthMount == nil {
			continue
		}
		fmt.Fprintf(&b, "#   - %-7s  %s -> %s\n", k, entry.HostAuthMount.HostPath, entry.HostAuthMount.ContainerPath)
	}
	return b.String()
}

// mountListing names every default mount — the patch/replace targets.
func mountListing() string {
	var b strings.Builder
	for _, m := range mountplan.Defaults() {
		fmt.Fprintf(&b, "#   %-18s %s -> %s\n", m.Name, m.Source, m.Target)
	}
	return b.String()
}
