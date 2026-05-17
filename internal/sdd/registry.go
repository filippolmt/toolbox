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
//  3. List GitignoreEntries when the upstream installer regenerates
//     files in the repo on every bootstrap (so Renovate version bumps
//     stay out of the diff). Leave nil when the installer produces
//     user-authored content meant to be committed.
package sdd

import "strings"

// Env keys consumed by internal/build/assets/entrypoint.sh. Centralised
// here so the encode site (sessionplan.sddEnv) and the decode site (bash
// loop in entrypoint.sh) cannot drift independently.
const (
	EnvEnabled       = "TOOLBOX_SDD_ENABLED"
	EnvWorkspaceHash = "TOOLBOX_SDD_WORKSPACE_HASH"
	EnvSkillPrefix   = "TOOLBOX_SDD_"

	EnvFieldPkg     = "_PKG"
	EnvFieldVersion = "_VERSION"
	EnvFieldBin     = "_BIN"
	EnvFieldSteps   = "_STEPS"
	EnvFieldMarker  = "_MARKER"

	StepSeparator = ";"
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
type Skill struct {
	Key              string
	NpmPackage       string
	Version          string
	BinName          string
	InstallSteps     [][]string
	GitignoreEntries []string
	RequiresMarker   string
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
		GitignoreEntries: []string{
			".claude/skills/gsd-*/",
			".codex/skills/gsd-*/",
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
