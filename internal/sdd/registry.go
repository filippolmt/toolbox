// Package sdd is the canonical registry of Spec-Driven-Development skill
// packs that toolbox can bootstrap repo-locally on `toolbox shell`.
//
// Skills lives entirely outside internal/catalog so adding or removing a
// row never invalidates the `toolbox:local-<hash>` tag: per-repo skill
// enablement must not force a rebuild.
//
// Adding a new skill is a three-step contract:
//  1. Append a Skill row below (alphabetical by Key).
//  2. Add a Renovate customManager entry in renovate.json keyed on the
//     same npm package name.
//  3. Choose ONE of the gitignore strategies and populate the matching
//     fields. The two strategies are mutually exclusive per skill — set
//     GitignoreEntries OR ManifestPaths, never both.
//
// Gitignore strategies:
//
//   - Static (GitignoreEntries): set when the upstream installer writes a
//     stable, enumerable list of paths on every bootstrap. cmd/sdd.go
//     `upsertGitignoreFence` writes the fenced block on `toolbox sdd
//     init <name>` at the host level. Used by skills with small,
//     predictable output.
//
//   - Manifest-driven (ManifestPaths + ExtraGitignoreEntries): set when
//     the installer materialises a manifest JSON file listing every
//     generated path. The entrypoint regen function consumes the
//     manifest at install/upgrade time and rewrites the fenced block
//     inside the container. ExtraGitignoreEntries covers sibling files
//     the installer writes but does not list in its own manifest
//     (typically state/profile files prefixed with the skill key).
//     Used by skills whose output is large or evolves across versions
//     (e.g. gsd), where static enumeration would drift.
//
// Skills that produce user-authored content meant to be committed leave
// all three fields nil; the host-side `sdd init` then skips .gitignore
// entirely.
package sdd

import "strings"

// Env keys consumed by internal/build/assets/entrypoint.sh. Centralised
// here so the encode site (sessionplan.sddEnv) and the decode site (bash
// loop in entrypoint.sh) cannot drift independently.
const (
	EnvEnabled       = "TOOLBOX_SDD_ENABLED"
	EnvWorkspaceHash = "TOOLBOX_SDD_WORKSPACE_HASH"
	EnvSkillPrefix   = "TOOLBOX_SDD_"

	EnvFieldPkg       = "_PKG"
	EnvFieldVersion   = "_VERSION"
	EnvFieldBin       = "_BIN"
	EnvFieldSteps     = "_STEPS"
	EnvFieldMarker    = "_MARKER"
	EnvFieldManifests = "_MANIFESTS"
	EnvFieldExtras    = "_EXTRAS"

	StepSeparator     = ";"
	ManifestSeparator = ","
	ExtraSeparator    = "\n"
)

// SkillEnvKey returns the env var name for a given skill field, e.g.
// SkillEnvKey("gsd", EnvFieldPkg) -> "TOOLBOX_SDD_GSD_PKG".
func SkillEnvKey(key, field string) string {
	return EnvSkillPrefix + strings.ToUpper(key) + field
}

// Skill describes one upstream SDD integration the entrypoint can bootstrap.
//
// InstallSteps tokens MUST be whitespace-free: the bash bootstrap splits a
// step on whitespace and the step list on StepSeparator.
//
// RequiresMarker (when non-empty) is a path under /workspace that must
// exist before the bootstrap runs. Used when first-run init is interactive
// or produces scaffolding the user wants to author manually; subsequent
// shells trigger a non-interactive upgrade.
//
// Gitignore strategies are mutually exclusive:
//
//   - GitignoreEntries (static): exact lines written into the fenced
//     `.gitignore` block by cmd/sdd.go on host. Used when the installer's
//     output is small and stable.
//
//   - ManifestPaths (manifest-driven): workspace-relative paths of JSON
//     manifest files the installer materialises after each bootstrap.
//     The entrypoint reads them post-install, derives one gitignore
//     entry per `files` key, and rewrites the fenced block from inside
//     the container. ExtraGitignoreEntries lists sibling files the
//     installer writes but does not include in its own manifest
//     (typically `.gsd-profile`, `gsd-install-state.json`, etc.).
//
// At most one of GitignoreEntries / ManifestPaths must be non-nil per
// skill (enforced by TestSkillFieldsMutex).
type Skill struct {
	Key                   string
	NpmPackage            string
	Version               string
	BinName               string
	InstallSteps          [][]string
	GitignoreEntries      []string
	ManifestPaths         []string
	ExtraGitignoreEntries []string
	RequiresMarker        string
}

// IsManifestManaged reports whether the skill delegates its `.gitignore`
// fence content to the entrypoint manifest reader, rather than to the
// host-side static fence writer in cmd/sdd.go.
func (s Skill) IsManifestManaged() bool {
	return len(s.ManifestPaths) > 0
}

var Skills = []Skill{
	{
		// bmad-method first-run is interactive (module/tool prompts):
		// the user runs `npx bmad-method install` manually once and
		// commits _bmad/. With the marker present, `bmad-method install
		// --yes` triggers the non-interactive Quick Update path.
		Key:            "bmad",
		NpmPackage:     "bmad-method",
		Version:        "6.6.0",
		BinName:        "bmad-method",
		InstallSteps:   [][]string{{"install", "--directory", ".", "--yes"}},
		RequiresMarker: "_bmad",
	},
	{
		// gsd ships per-runtime entry points and the USER-GUIDE does not
		// promise stacking runtime flags. Each call writes to a runtime-
		// scoped path (.claude/skills/gsd-* vs .codex/skills/gsd-*), so
		// the two steps never collide.
		Key:        "gsd",
		NpmPackage: "get-shit-done-cc",
		Version:    "1.42.3",
		BinName:    "get-shit-done-cc",
		InstallSteps: [][]string{
			{"--claude", "--local"},
			{"--codex", "--local"},
		},
		ManifestPaths: []string{
			".claude/gsd-file-manifest.json",
			".codex/gsd-file-manifest.json",
		},
		ExtraGitignoreEntries: []string{
			".claude/.gsd-profile",
			".claude/gsd-install-state.json",
			".claude/gsd-file-manifest.json",
			".claude/gsd-migration-journal/",
			".claude/skills/gsd-*/",
			".codex/.gsd-profile",
			".codex/gsd-install-state.json",
			".codex/gsd-file-manifest.json",
			".codex/skills/gsd-*/",
			// codex CLI rewrites agents/<key>.toml siblings of the
			// upstream .md prompts after each gsd install — these are
			// regenerated locally, not shipped in the manifest.
			".codex/agents/*.toml",
			// codex hooks + top-level config patched by gsd-install on
			// every bootstrap; outside the manifest root so the
			// generated fence would otherwise miss them.
			".codex/hooks/gsd-*",
			".codex/config.toml",
			".codex/hooks.json",
			// runtime spec output (gsd:spec-phase, gsd:sketch, etc.).
			".codex/specs/",
			"docs/superpowers/specs/",
		},
	},
	{
		// openspec init is interactive on a fresh project; the user runs
		// it manually with the desired --tools list, commits openspec/,
		// and the entrypoint takes over with the dedicated `openspec
		// update` subcommand.
		Key:            "openspec",
		NpmPackage:     "@fission-ai/openspec",
		Version:        "1.3.1",
		BinName:        "openspec",
		InstallSteps:   [][]string{{"update"}},
		RequiresMarker: "openspec",
	},
}

func Lookup(key string) (Skill, bool) {
	for _, s := range Skills {
		if s.Key == key {
			return s, true
		}
	}
	return Skill{}, false
}

func Keys() []string {
	out := make([]string, len(Skills))
	for i, s := range Skills {
		out[i] = s.Key
	}
	return out
}
