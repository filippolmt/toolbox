---
type: "query"
date: "2026-05-17T07:57:51.334044+00:00"
question: "Why does Lookup() bridge SDD Env Contract (c1) to toolbox init Scaffold (c12)?"
contributor: "graphify"
source_nodes: ["Lookup()", "runSDDInit()", "chdirTemp()", "runInit()", "upsertSDDFlag()"]
---

# Q: Why does Lookup() bridge SDD Env Contract (c1) to toolbox init Scaffold (c12)?

## Answer

Lookup() in internal/sdd/registry.go:107 has degree 4 but betweenness 0.113 — high because it's the SINGLE arc that crosses community 1 (SDD Env Contract) and community 12 (toolbox init Scaffold). Direction: Lookup() <-- runSDDInit() [INFERRED].

Why different communities: Lookup lives in c1 because its source siblings (Skill struct, registry.go, resolveEnabledSkills) cluster with the env-contract constants (EnvEnabled, EnvField*, SkillEnvKey). runSDDInit() lives in c12 because its companions are cmd/init_test.go::chdirTemp (shared test helper, degree 10), upsertSDDFlag, upsertGitignoreFence, usageError, and the 6 TestSDDInit* tests — same neighborhood as cmd/init.go::runInit.

Two overlapping bridges:

1. Runtime bridge: cmd/sdd.go::runSDDInit calls sdd.Lookup(name) to validate the skill key in 'toolbox sdd init <name>'. Sole CLI-side call-site of Lookup — the only other is resolveEnabledSkills internal to sdd.

2. Test infrastructure bridge: chdirTemp() defined in cmd/init_test.go for 'toolbox init' is now reused by 6 TestSDDInit* in cmd/sdd_test.go. Cement that sdd init is the conceptual sibling of init — both cobra subcommands scaffold by editing cwd files.

Structurally healthy: one entry-edge runSDDInit -> Lookup cleanly separates registry from CLI. Changing Lookup signature requires updating runSDDInit only. The chdirTemp test bridge is a side-effect of the /simplify cleanup commit (reusing existing helper instead of duplicating) — exactly the desired outcome.

5-hop path Lookup -> runSDDInit -> TestSDDInitAddsKeyToExistingBlock -> chdirTemp <- TestInitWritesAnnotatedYAML -> runInit shows the symmetry: same scaffold helper, two cobra sibling subcommands, registry consumed by one.

## Source Nodes

- Lookup()
- runSDDInit()
- chdirTemp()
- runInit()
- upsertSDDFlag()