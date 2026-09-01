// Package catalog owns the canonical declaration of every bundled tool: the
// Tool Catalog. One typed table (Entries) lists every CLI baked into the
// runtime image.
//
// All catalog entries are installed unconditionally — there is no per-tool
// opt-out. The image content is identical for every user, so the canonical
// image (`ghcr.io/filippolmt/toolbox:latest`) is what every shell pulls.
//
// Adding a CLI: append a row to Entries (alphabetical by Key). Populate
// InitScript when the tool needs a runtime init.d/ script. Populate
// HostAuthMount when the tool has a stable host-side credential path users
// may want to inherit via `inherit_host_auth:` in .toolbox.yaml.
package catalog

import "sort"

// Entry is a single bundled tool declaration.
//
// InitScript is the relative path under internal/build/assets/init.d/ when
// the tool needs runtime initialisation; otherwise "". The
// TestCatalogInitDBijection test enforces strict set-equality between
// populated InitScript values and the *.sh files shipped under init.d/.
//
// HostAuthMount is non-nil iff the CLI has a stable host credential
// location that users may opt into inheriting via `inherit_host_auth:`. A
// nil pointer means the CLI is not eligible for host inheritance — config
// validation rejects ineligible keys.
type Entry struct {
	Key           string         // tool key, also the inherit_host_auth value
	InitScript    string         // relative path under init.d/, or "" if none
	HostAuthMount *HostAuthMount // non-nil iff eligible for inherit_host_auth
}

// HostAuthMount declares the host → container credential path mapping used
// when a CLI key is listed in inherit_host_auth. Always read-only: the
// container reads the host's auth but must not mutate it.
type HostAuthMount struct {
	HostPath      string // ~/-relative or absolute (e.g. "~/.config/gh")
	ContainerPath string // absolute container path (e.g. "/home/toolbox/.config/gh")
}

// Entries is the canonical, alphabetical-by-Key declaration of every
// bundled tool. Slice ordering is part of the public contract — iterators
// get deterministic order without re-sorting.
var Entries = []Entry{
	{Key: "atuin", InitScript: "65-atuin.sh", HostAuthMount: &HostAuthMount{HostPath: "~/.local/share/atuin", ContainerPath: "/home/toolbox/.local/share/atuin"}},
	{Key: "azure", InitScript: "06-azure-creds.sh", HostAuthMount: &HostAuthMount{HostPath: "~/.azure", ContainerPath: "/home/toolbox/.azure"}},
	{Key: "bat"},
	{Key: "brew"},
	{Key: "bun"},
	{Key: "cf", InitScript: "20-cf.sh"},
	{Key: "claude", InitScript: "50-mcp-plugins.sh", HostAuthMount: &HostAuthMount{HostPath: "~/.claude", ContainerPath: "/home/toolbox/.claude"}},
	{Key: "codegraph", InitScript: "31-codegraph.sh"},
	{Key: "codex", InitScript: "25-codex.sh", HostAuthMount: &HostAuthMount{HostPath: "~/.codex", ContainerPath: "/home/toolbox/.codex"}},
	{Key: "compose"},
	{Key: "docker", HostAuthMount: &HostAuthMount{HostPath: "~/.docker", ContainerPath: "/home/toolbox/.docker"}},
	{Key: "eza"},
	{Key: "fd"},
	{Key: "gcloud", InitScript: "04-gcloud-creds.sh", HostAuthMount: &HostAuthMount{HostPath: "~/.config/gcloud", ContainerPath: "/home/toolbox/.config/gcloud"}},
	{Key: "gh", InitScript: "02-gh-creds.sh", HostAuthMount: &HostAuthMount{HostPath: "~/.config/gh", ContainerPath: "/home/toolbox/.config/gh"}},
	{Key: "glab", InitScript: "60-glab.sh", HostAuthMount: &HostAuthMount{HostPath: "~/.config/glab-cli", ContainerPath: "/home/toolbox/.config/glab-cli"}},
	{Key: "go"},
	{Key: "goimports"},
	{Key: "gopls"},
	{Key: "graphify", InitScript: "30-graphify.sh"},
	{Key: "gws"},
	{Key: "helm"},
	{Key: "herdr", InitScript: "61-herdr.sh"},
	{Key: "jq"},
	{Key: "kubectl"},
	{Key: "oci", InitScript: "08-oci-creds.sh", HostAuthMount: &HostAuthMount{HostPath: "~/.oci", ContainerPath: "/home/toolbox/.oci"}},
	{Key: "playwright"},
	{Key: "playwright_cli", InitScript: "40-playwright-cli.sh"},
	{Key: "pnpm"},
	{Key: "pyright"},
	{Key: "rtk", InitScript: "10-rtk.sh"},
	{Key: "shellcheck"},
	{Key: "shfmt"},
	{Key: "sonar"},
	{Key: "starship"},
	{Key: "tmux"},
	{Key: "tofu"},
	{Key: "typescript"},
	{Key: "typescript_language_server"},
	{Key: "uv"},
	{Key: "wrangler"},
	{Key: "yq"},
	{Key: "zsh"},
}

// Keys returns one string per Entry, in catalog (alphabetical) order. A
// fresh slice is allocated on each call so callers cannot alias the
// internal table.
func Keys() []string {
	out := make([]string, len(Entries))
	for i, e := range Entries {
		out[i] = e.Key
	}
	return out
}

// Find returns the Entry with matching Key and true, or the zero Entry and
// false. Linear scan — acceptable at the current catalog size.
func Find(key string) (Entry, bool) {
	for _, e := range Entries {
		if e.Key == key {
			return e, true
		}
	}
	return Entry{}, false
}

// HostAuthEligibleKeys returns the sorted list of Entry keys with a
// non-nil HostAuthMount. Used by config validation to enumerate valid
// inherit_host_auth values in error messages.
func HostAuthEligibleKeys() []string {
	var out []string
	for _, e := range Entries {
		if e.HostAuthMount != nil {
			out = append(out, e.Key)
		}
	}
	sort.Strings(out)
	return out
}
