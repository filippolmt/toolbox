# Graph Report - toolbox  (2026-05-17)

## Corpus Check
- 801 files · ~847,248 words
- Verdict: corpus is large enough that graph structure adds value.

## Summary
- 867 nodes · 1292 edges · 94 communities (50 shown, 44 thin omitted)
- Extraction: 76% EXTRACTED · 24% INFERRED · 0% AMBIGUOUS · INFERRED: 308 edges (avg confidence: 0.81)
- Token cost: 0 input · 0 output

## Graph Freshness
- Built from commit: `bbef7bcf`
- Run `git rev-parse HEAD` and compare to check if the graph is stale.
- Run `graphify update .` after code changes (no API cost).

## Community Hubs (Navigation)
- [[_COMMUNITY_CLI Subcommands & Init|CLI Subcommands & Init]]
- [[_COMMUNITY_SDD Env Contract|SDD Env Contract]]
- [[_COMMUNITY_Repo Conventions & Skills|Repo Conventions & Skills]]
- [[_COMMUNITY_Tool Catalog|Tool Catalog]]
- [[_COMMUNITY_Config Init Tests|Config Init Tests]]
- [[_COMMUNITY_Mount Plan Core|Mount Plan Core]]
- [[_COMMUNITY_Container Lifecycle|Container Lifecycle]]
- [[_COMMUNITY_Named Shells|Named Shells]]
- [[_COMMUNITY_Architecture Vocabulary|Architecture Vocabulary]]
- [[_COMMUNITY_Cobra Args Wrapping|Cobra Args Wrapping]]
- [[_COMMUNITY_Image Plan|Image Plan]]
- [[_COMMUNITY_Phase 07 Catalog Decisions|Phase 07 Catalog Decisions]]
- [[_COMMUNITY_toolbox init Scaffold|toolbox init Scaffold]]
- [[_COMMUNITY_golang-cli Skill Examples|golang-cli Skill Examples]]
- [[_COMMUNITY_YAML Upsert Pipeline|YAML Upsert Pipeline]]
- [[_COMMUNITY_Run Plan Pipeline|Run Plan Pipeline]]
- [[_COMMUNITY_Container Attach|Container Attach]]
- [[_COMMUNITY_Mount Resolution|Mount Resolution]]
- [[_COMMUNITY_Project State Decisions|Project State Decisions]]
- [[_COMMUNITY_Verify Pipeline|Verify Pipeline]]
- [[_COMMUNITY_Docker Identity|Docker Identity]]
- [[_COMMUNITY_Phase 10 Init Sequence|Phase 10 Init Sequence]]
- [[_COMMUNITY_Claude Code Plugins|Claude Code Plugins]]
- [[_COMMUNITY_Container Mocks|Container Mocks]]
- [[_COMMUNITY_Bijection Tests|Bijection Tests]]
- [[_COMMUNITY_Init.d Self-Gate Pattern|Init.d Self-Gate Pattern]]
- [[_COMMUNITY_Parent Dir Resolution|Parent Dir Resolution]]
- [[_COMMUNITY_Exit Code Patterns|Exit Code Patterns]]
- [[_COMMUNITY_Completion Examples|Completion Examples]]
- [[_COMMUNITY_Docs Glossary|Docs Glossary]]
- [[_COMMUNITY_Cluster 30|Cluster 30]]
- [[_COMMUNITY_Cluster 31|Cluster 31]]
- [[_COMMUNITY_Cluster 32|Cluster 32]]
- [[_COMMUNITY_Cluster 42|Cluster 42]]
- [[_COMMUNITY_Cluster 43|Cluster 43]]
- [[_COMMUNITY_Cluster 44|Cluster 44]]
- [[_COMMUNITY_Cluster 50|Cluster 50]]
- [[_COMMUNITY_Cluster 51|Cluster 51]]
- [[_COMMUNITY_Cluster 52|Cluster 52]]
- [[_COMMUNITY_Cluster 53|Cluster 53]]
- [[_COMMUNITY_Cluster 54|Cluster 54]]
- [[_COMMUNITY_Cluster 55|Cluster 55]]
- [[_COMMUNITY_Cluster 56|Cluster 56]]
- [[_COMMUNITY_Cluster 57|Cluster 57]]
- [[_COMMUNITY_Cluster 58|Cluster 58]]
- [[_COMMUNITY_Cluster 59|Cluster 59]]
- [[_COMMUNITY_Cluster 60|Cluster 60]]
- [[_COMMUNITY_Cluster 61|Cluster 61]]
- [[_COMMUNITY_Cluster 62|Cluster 62]]
- [[_COMMUNITY_Cluster 63|Cluster 63]]
- [[_COMMUNITY_Cluster 64|Cluster 64]]
- [[_COMMUNITY_Cluster 65|Cluster 65]]
- [[_COMMUNITY_Cluster 66|Cluster 66]]
- [[_COMMUNITY_Cluster 67|Cluster 67]]
- [[_COMMUNITY_Cluster 68|Cluster 68]]
- [[_COMMUNITY_Cluster 69|Cluster 69]]
- [[_COMMUNITY_Cluster 70|Cluster 70]]
- [[_COMMUNITY_Cluster 71|Cluster 71]]
- [[_COMMUNITY_Cluster 72|Cluster 72]]
- [[_COMMUNITY_Cluster 73|Cluster 73]]
- [[_COMMUNITY_Cluster 74|Cluster 74]]
- [[_COMMUNITY_Cluster 75|Cluster 75]]
- [[_COMMUNITY_Cluster 76|Cluster 76]]
- [[_COMMUNITY_Cluster 77|Cluster 77]]
- [[_COMMUNITY_Cluster 78|Cluster 78]]
- [[_COMMUNITY_Cluster 79|Cluster 79]]
- [[_COMMUNITY_Cluster 80|Cluster 80]]
- [[_COMMUNITY_Cluster 81|Cluster 81]]
- [[_COMMUNITY_Cluster 82|Cluster 82]]
- [[_COMMUNITY_Cluster 83|Cluster 83]]
- [[_COMMUNITY_Community 84|Community 84]]
- [[_COMMUNITY_Community 85|Community 85]]
- [[_COMMUNITY_Community 86|Community 86]]
- [[_COMMUNITY_Community 87|Community 87]]
- [[_COMMUNITY_Community 88|Community 88]]
- [[_COMMUNITY_Community 89|Community 89]]
- [[_COMMUNITY_Community 90|Community 90]]
- [[_COMMUNITY_Community 91|Community 91]]
- [[_COMMUNITY_Community 93|Community 93]]

## God Nodes (most connected - your core abstractions)
1. `Shell()` - 29 edges
2. `mkdirAll()` - 28 edges
3. `testConfig()` - 21 edges
4. `LANGUAGE.md (architecture vocabulary)` - 20 edges
5. `stubExecShell()` - 19 edges
6. `resolveShellWorkspace()` - 18 edges
7. `testWorkspace()` - 18 edges
8. `testPlan()` - 17 edges
9. `CLAUDE.md (Claude Code guidance)` - 17 edges
10. `mergeMounts()` - 14 edges

## Surprising Connections (you probably didn't know these)
- `config.go (Viper config example)` --semantically_similar_to--> `Config load order`  [INFERRED] [semantically similar]
  .claude/skills/golang-cli/assets/examples/config.go → CLAUDE.md
- `Module` --semantically_similar_to--> `Mount Plan`  [INFERRED] [semantically similar]
  .agents/skills/improve-codebase-architecture/LANGUAGE.md → CONTEXT.md
- `colorExamples()` --calls--> `Success()`  [INFERRED]
  .claude/skills/golang-cli/assets/examples/output.go → internal/ui/output.go
- `watchConfig()` --calls--> `Info()`  [INFERRED]
  .claude/skills/golang-cli/assets/examples/config.go → internal/ui/output.go
- `cmd/ directory contains only minimal main.go` --semantically_similar_to--> `main()`  [INFERRED] [semantically similar]
  .claude/skills/golang-project-layout/references/directory-layouts.md → .claude/skills/golang-cli/assets/examples/main.go

## Hyperedges (group relationships)
- **Four-seam deepening composition (v1.3 Architecture Deepening)** — concept_mount_plan, concept_tool_catalog, concept_config_plan, concept_session_plan [EXTRACTED 1.00]
- **Architectural vocabulary cluster** — concept_module, concept_interface, concept_depth, concept_seam, concept_adapter, concept_leverage, concept_locality [EXTRACTED 1.00]
- **Release pipeline (tag, goreleaser, homebrew)** — goreleaser_yaml, concept_release_flow, contributing_md, main_go [EXTRACTED 1.00]
- **Minimal Cobra app skeleton (main + root + subcommand)** — examples_main_main, examples_root_rootcmd, examples_serve_servecmd, examples_version_versioncmd [EXTRACTED 1.00]
- **Architecture vocabulary set (module/interface/depth/seam/adapter/leverage/locality)** — improve_codebase_architecture_module, improve_codebase_architecture_interface, improve_codebase_architecture_depth, improve_codebase_architecture_seam, improve_codebase_architecture_adapter, improve_codebase_architecture_leverage, improve_codebase_architecture_locality [EXTRACTED 1.00]
- **Pre-push validation pipeline mirrored from CI** — verify_lint_step, verify_test_step, verify_smoke_step, workflows_ci_lint_job, workflows_ci_test_job, workflows_docker_ci_build_job [INFERRED 0.85]
- **Phase 07 tool-catalog plans (01-06)** — phases_07_01_summary, phases_07_02_summary, phases_07_03_summary, phases_07_04_summary, phases_07_05_summary, phases_07_06_summary [EXTRACTED 1.00]
- **Phase 08 config-plan plans (01-06)** — phases_08_01_summary, phases_08_02_summary, phases_08_03_summary, phases_08_04_summary, phases_08_05_summary, phases_08_06_summary [EXTRACTED 1.00]
- **Phase 10 init.d extractions (rtk/cf/graphify/playwright-cli)** — phases_10_01_summary, phases_10_02_summary, phases_10_03_summary, phases_10_04_summary, phases_10_05_summary [EXTRACTED 1.00]
- **yaml.Node pipeline shared by upsertSDDFlag and upsertShellInUserConfig** — cmd_sdd_upsertsddflag, cmd_shell_named_upsertshellinuserconfig, internal_configio_pkg [EXTRACTED 1.00]
- **build/shell/config subcommands consume cmd.cfg loaded by initConfig** — cmd_build_runbuild, cmd_shell_runshell, cmd_config_writeresolvedconfig [EXTRACTED 1.00]
- **Phase 10 init-sequence plans 06-09 + VERIFICATION form milestone closure** — 10_init_sequence_10_06_summary, 10_init_sequence_10_07_summary, 10_init_sequence_10_verification [EXTRACTED 1.00]
- **Config resolution pipeline (Plan -> Merge -> Validate)** — config_plan_plan, config_plan_merge, config_plan_seedtooldefaults, config_plan_filltooldefaultsbackstop, config_config_validateshell, config_config_validatemountsroot [EXTRACTED 1.00]
- **Catalog declaration + accessors form a single source of truth** — catalog_catalog_entries, catalog_catalog_keys, catalog_catalog_buildarg, catalog_catalog_defaults, catalog_catalog_isdefault, catalog_catalog_writecanonical [EXTRACTED 1.00]
- **configio YAML in-place editing surface** — configio_configio_ensuredocumentmap, configio_configio_ensurechildmap, configio_configio_setmapvalue, configio_configio_setmapbool, configio_configio_findorappendpair, configio_configio_atomicwritefile [EXTRACTED 1.00]
- **mountplan.Plan pipeline subseams** — mountplan_plan_plan, mountplan_defaults_defaults, mountplan_merge_applymountsroot, mountplan_merge_mergemounts, mountplan_resolve_resolveall, mountplan_workspace_workspacemirrorpath [EXTRACTED 1.00]
- **container create-and-start sequence** — container_lifecycle_shell, container_lifecycle_dispatchop, container_lifecycle_createandstart, imageplan_imageplan_ensure, dockeridentity_dockeridentity_resolve, container_attach_execshell [EXTRACTED 1.00]
- **imagepull RefreshIfStale TTL+pull+record** — imagepull_imagepull_refreshifstale, imagepull_imagepull_cached, imagepull_imagepull_pull, imagepull_imagepull_record [EXTRACTED 1.00]
- **SDD env-field contract (per-skill fields)** — sdd_registry_const_envfieldpkg, sdd_registry_const_envfieldversion, sdd_registry_const_envfieldbin, sdd_registry_const_envfieldsteps, sdd_registry_const_envfieldmarker [EXTRACTED 1.00]
- **SDD env envelope (enabled list + workspace hash + prefix)** — sdd_registry_const_envenabled, sdd_registry_const_envworkspacehash, sdd_registry_const_envskillprefix [EXTRACTED 1.00]
- **Container lifecycle decision triad (session plan / run plan / teardown)** — sessionplan_plan_plan_fn, runplan_runplan_compute, teardown_teardown_onshellexit [INFERRED 0.85]

## Communities (94 total, 44 thin omitted)

### Community 0 - "CLI Subcommands & Init"
Cohesion: 0.13
Nodes (14): cmd/shell_named.go — named shell resolution + bootstrap, runShell(), signalCtx(), TestSignalCtxReturnsCancellableContext(), runStop(), cmd/stop.go stopCmd, NewClient(), Stop() (+6 more)

### Community 1 - "SDD Env Contract"
Cohesion: 0.05
Nodes (52): concept: SDD env contract between sessionplan.sddEnv and entrypoint.sh, const EnvEnabled = TOOLBOX_SDD_ENABLED, const EnvSkillPrefix = TOOLBOX_SDD_, const EnvWorkspaceHash = TOOLBOX_SDD_WORKSPACE_HASH, const StepSeparator = ;, rationale: Skills excluded from internal/catalog to keep image hash neutral (per-repo enablement must not force toolbox:local-<hash> rebuild), Keys(), Lookup() (+44 more)

### Community 2 - "Repo Conventions & Skills"
Cohesion: 0.05
Nodes (51): /add-cli skill, AGENTS.md (symlink to CLAUDE.md), CLAUDE.md (Claude Code guidance), cmd package (cobra root), Auth Isolation under ~/.toolbox/, Codex nested sandbox (seccomp=unconfined), Config load order, Config Plan (+43 more)

### Community 3 - "Tool Catalog"
Cohesion: 0.05
Nodes (43): BuildArg(), Defaults(), catalog.Entries (canonical tool table), IsDefault(), Keys(), TestBuildArgLookup(), TestCanonicalEncodingDeterministic(), TestCanonicalEncodingIsNeutralToOptionalFieldPopulation() (+35 more)

### Community 4 - "Config Init Tests"
Cohesion: 0.08
Nodes (37): resetCmdState(), TestInitConfigAppliesDefaults(), TestInitConfigExplicitFileIsRead(), TestInitConfigProjectFileFromCWD(), TestInitConfigProjectFileStopsAtHome(), TestInitConfigProjectFileWalksUpFromSubdir(), TestPlanGlobalUnreadableIsBestEffort(), TestPlanWalksUpFromSubdir() (+29 more)

### Community 5 - "Mount Plan Core"
Cohesion: 0.07
Nodes (37): Bind, defaults(), assertMount(), assertSymlink(), findMount(), TestDefaults(), applyMountsRoot(), mergeMounts() (+29 more)

### Community 6 - "Container Lifecycle"
Cohesion: 0.15
Nodes (33): DefaultTools(), Shell(), captureStderr(), stubExecShell(), testConfig(), testPlan(), testPlanWithCfg(), TestShellAutoBuildsCustomImage() (+25 more)

### Community 7 - "Named Shells"
Cohesion: 0.11
Nodes (34): bootstrapMissingNamedShell(), createHint(), defaultShellPath(), ensureNamedShellPath(), missingPathHint(), missingShellHint(), promptPath(), promptYesNo() (+26 more)

### Community 8 - "Architecture Vocabulary"
Cohesion: 0.07
Nodes (30): Adapter, Deletion test, Depth (leverage at interface), Design It Twice (Ousterhout), Interface (architectural), Leverage, Locality, Ports & Adapters dependency category (+22 more)

### Community 9 - "Cobra Args Wrapping"
Cohesion: 0.09
Nodes (18): TestUsageArgsWraps(), cmd/build.go — toolbox build subcommand, cmd/completion.go — toolbox completion subcommand, cmd/config.go — toolbox config show/example, TestWriteResolvedConfigDeterministic(), TestWriteResolvedConfigEmptyMounts(), TestWriteResolvedConfigNilConfigErrors(), writeResolvedConfig() (+10 more)

### Community 10 - "Image Plan"
Cohesion: 0.11
Nodes (19): Refresh(), TestRefreshNoOpForLocalImage(), mockClient, image readiness two-phase strategy (Refresh+Ensure), cached(), isAuthError(), markerPath(), pull() (+11 more)

### Community 11 - "Phase 07 Catalog Decisions"
Cohesion: 0.07
Nodes (28): D-10 mutation test: optional fields hash-neutral, internal/catalog.Entries (30-row alphabetical), Catalog Entry schema (Key/Default/BuildArg + reserved Description/InitScript/SmokeTest), Module-package shape (mirrors internal/mountplan), Phase 07 Plan 01 — Tool Catalog Foundation, WriteCanonical + WriteCanonicalEntries (parameterised encoder), TestComputeImageHashPinnedDigest pin = a94fa8dacf9e (D-12), Phase 07 Plan 02 — Tag Migration to Catalog (+20 more)

### Community 12 - "toolbox init Scaffold"
Cohesion: 0.15
Nodes (21): runInit(), chdirTemp(), TestInitForceOverwrites(), TestInitRefusesOverwriteWithoutForce(), TestInitWritesAnnotatedYAML(), usageError — flag/arg error wrapper for exit code 2, gitignoreFenceEnd(), gitignoreFenceStart() (+13 more)

### Community 13 - "golang-cli Skill Examples"
Cohesion: 0.09
Nodes (22): flagExamples(), main(), Execute(), initConfig(), rootCmd, serveCmd, versionCmd, Version via ldflags injection (+14 more)

### Community 14 - "YAML Upsert Pipeline"
Cohesion: 0.2
Nodes (20): upsertSDDFlag(), TestUpsertShellInUserConfigPreservesExistingFields(), upsertShellInUserConfig(), AtomicWriteFile(), EnsureChildMap(), EnsureDocumentMap(), findOrAppendPair(), GlobalConfigDir() (+12 more)

### Community 15 - "Run Plan Pipeline"
Cohesion: 0.13
Nodes (14): concept: Plan-family pipeline (Config -> Mount -> Session -> Run), Action, notFoundErr, Op, Action enum (Connect/Start/Create), Compute(), rationale: nil ContainerJSONBase routes to ActionCreate to avoid panicking on half-populated inspect record, Op struct (Action, ExistingID) (+6 more)

### Community 16 - "Container Attach"
Cohesion: 0.5
Nodes (3): Answer, Q: Why does Shell() bridge Container Lifecycle to CLI Subcommands, SDD Env Contract, Image Plan, Run Plan, Container Attach?, Source Nodes

### Community 17 - "Mount Resolution"
Cohesion: 0.23
Nodes (13): ensureSource(), expandHome(), resolveAll(), TestExpandHome(), TestResolveAllCreatesMissingWhenRequested(), TestResolveAllKeepsNonEmptyDirEvenWithSymlinkFrom(), TestResolveAllReadOnlyMode(), TestResolveAllRelativeSourceCreatesUnderCWD() (+5 more)

### Community 18 - "Project State Decisions"
Cohesion: 0.12
Nodes (16): Decision: Cobra + Viper over kong/urfave, Decision: Debian slim over Alpine (musl breaks Claude Code), Decision: Docker socket mount over DinD, STATE.md (project state), Milestone v1.3 Architecture Deepening (shipped 2026-05-07), Phase 01 image-foundation, Phase 02 CLI Go (Cobra+Viper+Docker SDK), Phase 03 CI/CD (+8 more)

### Community 19 - "Verify Pipeline"
Cohesion: 0.16
Nodes (16): internal/build/assets/Dockerfile (runtime image), golangci-lint, internal/build/assets/smoke-test.sh, make go-lint step, verify skill (pre-push validation), Conditional smoke-test step, make go-test step, CI workflow (PR lint+test+renovate) (+8 more)

### Community 20 - "Docker Identity"
Cohesion: 0.2
Nodes (9): dockerSockGroups(), Identity, Resolve(), TestDockerSockGroupsAppendsHostGIDOnLinux(), TestDockerSockGroupsFallbackWhenStatFails(), TestDockerSockGroupsIncludesRootForDesktopCase(), TestDockerSockGroupsMatchesOnTargetNotSource(), TestDockerSockGroupsReturnsNilWhenSockNotMounted() (+1 more)

### Community 21 - "Phase 10 Init Sequence"
Cohesion: 0.22
Nodes (11): Plan 10-06 SUMMARY: extract MCP plugin auto-build to 50-mcp-plugins.sh, Plan 10-07 SUMMARY: init.d iterator wiring + smoke-test bijection (KEYSTONE), Plan 10-08 SUMMARY: Init Sequence glossary entry in CONTEXT.md, Plan 10-09 SUMMARY: CLAUDE.md milestone-wide final pass (DOCS-02), Phase 10 VERIFICATION: 6/7 truths verified, 3 human-needed items, D-04: single-binary self-gate at script top (npm is the owner), init.d bijection: catalog InitScript <-> embed.FS <-> in-image /usr/local/lib/toolbox/init.d, Marker log path ~/.toolbox-state/init/<name>.log (Pitfall 1) (+3 more)

### Community 22 - "Claude Code Plugins"
Cohesion: 0.18
Nodes (10): enabledPlugins, claude-code-setup@claude-plugins-official, code-review@claude-plugins-official, code-simplifier@claude-plugins-official, commit-commands@claude-plugins-official, gopls-lsp@claude-plugins-official, pr-review-toolkit@claude-plugins-official, security-guidance@claude-plugins-official (+2 more)

### Community 24 - "Bijection Tests"
Cohesion: 0.29
Nodes (7): TestCatalogDockerfileBijection (set-equality, both directions), External test package `catalog_test` to avoid build→catalog cycle, Phase 07 Plan 04 — Catalog ↔ Dockerfile Bijection Test (CAT-04), TestCatalogInitDBijection (INIT-04 set-equality), Phase 10 Plan 01 — Init sequence bootstrap (catalog InitScript + embed.FS), Tar mode 0755 force for init.d/* (embed.FS strips exec bits), fs.WalkDir migration of tarEmbeddedContext + computeImageHashFromFS

### Community 25 - "Init.d Self-Gate Pattern"
Cohesion: 0.29
Nodes (7): Single-binary self-gate pattern (D-04): `command -v <tool> || exit 0`, Phase 10 Plan 02 — Extract rtk init to 10-rtk.sh, Inner gate `command -v claude && [ -d $HOME/.claude ]` preserved (Pitfall 5), Phase 10 Plan 03 — Extract cf SKILL seed to 20-cf.sh, Phase 10 Plan 04 — Extract graphify install to 30-graphify.sh, Subshell wrapper `(cd "$HOME" && playwright-cli install --skills claude)` (Pitfall 4), Phase 10 Plan 05 — Extract playwright-cli install to 40-playwright-cli.sh

### Community 26 - "Parent Dir Resolution"
Cohesion: 0.47
Nodes (4): ParentDirs(), TestParentDirsDeduplicatesAndSorts(), TestParentDirsExcludesHomeRoot(), TestParentDirsSkipsTargetsOutsideHome()

### Community 29 - "Docs Glossary"
Cohesion: 0.5
Nodes (4): DOCS-01: ### Tool Catalog glossary entry in CONTEXT.md, Phase 07 Plan 06 — Tool Catalog glossary + CLAUDE.md patches, DOCS-01: ### Config Plan glossary entry in CONTEXT.md, Phase 08 Plan 06 — Config Plan glossary + CLAUDE.md patches

### Community 31 - "Cluster 31"
Cohesion: 0.67
Nodes (3): Six section banners (Seam/Walk-Up/Defaults/File Load/Validation/Helpers), Phase 08 Plan 01 — Scaffold internal/config Plan + Merge seams, internal/config.walkUp (relocated verbatim from cmd/root.go::findProjectConfig)

### Community 32 - "Cluster 32"
Cohesion: 0.67
Nodes (3): goreleaser/goreleaser-action@v7, HOMEBREW_TAP_TOKEN secret for homebrew tap, Release workflow (GoReleaser on v* tag)

### Community 84 - "Community 84"
Cohesion: 0.5
Nodes (3): Answer, Q: Why does Lookup() bridge SDD Env Contract (c1) to toolbox init Scaffold (c12)?, Source Nodes

### Community 85 - "Community 85"
Cohesion: 0.5
Nodes (3): Answer, Q: Are 199 weakly-connected nodes a documentation gap or AST noise?, Source Nodes

### Community 86 - "Community 86"
Cohesion: 0.12
Nodes (12): execShell(), TestExecShell_ContainerExecAttachError(), TestExecShell_ContainerExecCreateError(), TestExecShell_NonTTYStdin(), attachMock, colorExamples(), isTerminal(), outputExample() (+4 more)

### Community 87 - "Community 87"
Cohesion: 0.23
Nodes (13): createAndStart(), dispatchOp(), formatPublishMismatch(), StopAll(), Ensure, Info(), Success(), captureStderr() (+5 more)

### Community 88 - "Community 88"
Cohesion: 0.24
Nodes (12): HasActiveExecs(), OnShellExit(), rationale: sibling exec gate — leave container running if another shell is attached, otherwise stop+remove, StopOne(), TestHasActiveExecsFalseOnInspectError(), TestHasActiveExecsFalseOnNilContainerJSONBase(), TestHasActiveExecsTrueOnRunningSibling(), TestOnShellExitSkipsStopWhenSiblingExecRunning() (+4 more)

### Community 89 - "Community 89"
Cohesion: 0.33
Nodes (4): runBuild(), internal/build — image build + ResolveImage + BuildArgs, internal/container — Docker SDK client + Shell driver, internal/ui — Info/Warn user-facing output

## Knowledge Gaps
- **181 isolated node(s):** `Answer`, `Source Nodes`, `Answer`, `Source Nodes`, `Answer` (+176 more)
  These have ≤1 connection - possible missing edges or undocumented components.
- **44 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `Shell()` connect `Container Lifecycle` to `CLI Subcommands & Init`, `SDD Env Contract`, `Image Plan`, `Run Plan Pipeline`, `Community 86`, `Community 87`, `Community 88`?**
  _High betweenness centrality (0.152) - this node is a cross-community bridge._
- **Why does `Lookup()` connect `SDD Env Contract` to `toolbox init Scaffold`?**
  _High betweenness centrality (0.109) - this node is a cross-community bridge._
- **Are the 24 inferred relationships involving `Shell()` (e.g. with `runShell()` and `Warning()`) actually correct?**
  _`Shell()` has 24 INFERRED edges - model-reasoned connections that need verification._
- **Are the 13 inferred relationships involving `mkdirAll()` (e.g. with `ensureNamedShellPath()` and `TestEnsureNamedShellPathRejectsSymlink()`) actually correct?**
  _`mkdirAll()` has 13 INFERRED edges - model-reasoned connections that need verification._
- **What connects `Answer`, `Source Nodes`, `Answer` to the rest of the system?**
  _205 weakly-connected nodes found - possible documentation gaps or missing edges._
- **Should `CLI Subcommands & Init` be split into smaller, more focused modules?**
  _Cohesion score 0.13 - nodes in this community are weakly interconnected._
- **Should `SDD Env Contract` be split into smaller, more focused modules?**
  _Cohesion score 0.05 - nodes in this community are weakly interconnected._