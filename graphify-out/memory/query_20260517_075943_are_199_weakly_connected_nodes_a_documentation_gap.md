---
type: "query"
date: "2026-05-17T07:59:43.342691+00:00"
question: "Are 199 weakly-connected nodes a documentation gap or AST noise?"
contributor: "graphify"
source_nodes: ["internal_version_version_go", "sessionplan_plan_sessionplan_struct", "sdd_registry_const_envfieldpkg", "cmd_version_versioncmd", "cmd_root"]
---

# Q: Are 199 weakly-connected nodes a documentation gap or AST noise?

## Answer

Mix 60/40 — noise dominant, real gap minority.

Structure: 850 nodes -> 66 components, 1 giant (643 nodes = 75%), 39 isolated (degree 0), 270 degree-1 leaves, 54 small components (<=3 nodes).

Categories of the 39 isolated:

NOISE (~24, no fix needed):
- file-level *_pkg AST nodes (sessionplan_shell_pkg, runplan, teardown, version, workspace) — package-level synthetic nodes with no outgoing
- *_test.go file nodes — AST emits the file node but doesn't link file -> contained test functions
- golang-cli skill examples (signal.go, args.go, evals.json) + 4 golang_cli_* concept nodes — not part of the real codebase, just skill content

REAL EDGE GAP (~15, fixable):
1. Public types isolated: SessionPlan, MergedSessionPlan, Image, Bind, Mount, Entry (catalog), Result (mountplan). Missing edges 'caller_function -> type_name' for construction/param/return. Fix: enrich semantic extractor with 'function signature mentions type X' relation.
2. SDD env field constants (5 nodes: envfieldpkg/version/bin/steps/marker). SkillEnvKey(key, field) composes them at runtime but AST doesn't emit edges. Quasi-enum, low priority.
3. Cobra command vars: versionCmd, signalctx pkg, cmd_root file. rootCmd.AddCommand wiring not resolved by AST — disconnects entire subcommand registration from main graph. Highest-value fix: instrument semantic extractor for cobra patterns.

270 degree-1 leaves: mostly TestXxx functions called only by Go test framework (external to graph). Normal pattern, not a gap.

Verdict: no intervention needed now. 75% giant component is healthy for 90 Go files. If cleanup desired, fix cobra AddCommand recognition — disconnects subcommand registration from main graph.

## Source Nodes

- internal_version_version_go
- sessionplan_plan_sessionplan_struct
- sdd_registry_const_envfieldpkg
- cmd_version_versioncmd
- cmd_root