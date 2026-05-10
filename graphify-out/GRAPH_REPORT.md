# Graph Report - toolbox  (2026-05-10)

## Corpus Check
- 60 files · ~38,820 words
- Verdict: corpus is large enough that graph structure adds value.

## Summary
- 542 nodes · 840 edges · 40 communities (36 shown, 4 thin omitted)
- Extraction: 74% EXTRACTED · 26% INFERRED · 0% AMBIGUOUS · INFERRED: 221 edges (avg confidence: 0.8)
- Token cost: 0 input · 0 output

## Graph Freshness
- Built from commit: `96bec50f`
- Run `git rev-parse HEAD` and compare to check if the graph is stale.
- Run `graphify update .` after code changes (no API cost).

## Community Hubs (Navigation)
- [[_COMMUNITY_Tool Catalog & Build Args|Tool Catalog & Build Args]]
- [[_COMMUNITY_CLI Commands (buildshell)|CLI Commands (build/shell)]]
- [[_COMMUNITY_Shell Exec & Test Stubs|Shell Exec & Test Stubs]]
- [[_COMMUNITY_Mount Defaults & Merge Tests|Mount Defaults & Merge Tests]]
- [[_COMMUNITY_Config Plan Domain|Config Plan Domain]]
- [[_COMMUNITY_Container Runtime Architecture|Container Runtime Architecture]]
- [[_COMMUNITY_Config Plan Tests|Config Plan Tests]]
- [[_COMMUNITY_Cobra Command Registry|Cobra Command Registry]]
- [[_COMMUNITY_Config Example Rendering|Config Example Rendering]]
- [[_COMMUNITY_Args Validation Tests|Args Validation Tests]]
- [[_COMMUNITY_Tool Defaults & Shell Cmd|Tool Defaults & Shell Cmd]]
- [[_COMMUNITY_Catalog Bijection Tests|Catalog Bijection Tests]]
- [[_COMMUNITY_Mount Resolve|Mount Resolve]]
- [[_COMMUNITY_Docker Client Mock|Docker Client Mock]]
- [[_COMMUNITY_Exec Shell & Attach|Exec Shell & Attach]]
- [[_COMMUNITY_Mount Plan E2E|Mount Plan E2E]]
- [[_COMMUNITY_Docker Sock Groups|Docker Sock Groups]]
- [[_COMMUNITY_Mount Parent Dirs|Mount Parent Dirs]]
- [[_COMMUNITY_Main Entry & Usage|Main Entry & Usage]]
- [[_COMMUNITY_Shell Completion|Shell Completion]]
- [[_COMMUNITY_Version Cmd Init|Version Cmd Init]]
- [[_COMMUNITY_Completion Test|Completion Test]]
- [[_COMMUNITY_Version Test|Version Test]]
- [[_COMMUNITY_Mount Defaults|Mount Defaults]]
- [[_COMMUNITY_Init.d Bijection|Init.d Bijection]]
- [[_COMMUNITY_Lifecycle File|Lifecycle File]]
- [[_COMMUNITY_Version Const|Version Const]]
- [[_COMMUNITY_Community 38|Community 38]]

## God Nodes (most connected - your core abstractions)
1. `Shell()` - 32 edges
2. `mkdirAll()` - 23 edges
3. `stubExecShell()` - 19 edges
4. `testConfig()` - 18 edges
5. `testWorkspace()` - 18 edges
6. `testPlan()` - 17 edges
7. `resolveAll()` - 13 edges
8. `mergeMounts()` - 13 edges
9. `findMount()` - 13 edges
10. `Shell` - 12 edges

## Surprising Connections (you probably didn't know these)
- `examples/startup.d/README.md` --references--> `init.d Bijection`  [INFERRED]
  examples/startup.d/README.md → internal/catalog/init_d_bijection_test.go
- `runShell()` --calls--> `Shell()`  [INFERRED]
  cmd/shell.go → internal/container/lifecycle.go
- `TestInitConfigExplicitFileIsRead()` --calls--> `Keys()`  [INFERRED]
  cmd/config_test.go → internal/catalog/catalog.go
- `TestInitConfigProjectFileWalksUpFromSubdir()` --calls--> `mkdirAll()`  [INFERRED]
  cmd/config_test.go → internal/sessionplan/plan_test.go
- `TestInitConfigProjectFileStopsAtHome()` --calls--> `mkdirAll()`  [INFERRED]
  cmd/config_test.go → internal/sessionplan/plan_test.go

## Hyperedges (group relationships)
- **cobra_command_tree** — root_rootcmd, shell_shellcmd, build_buildcmd, stop_stopcmd, init_initcmd, config_configcmd, version_versioncmd, completion_completioncmd [EXTRACTED 1.00]
- **docker_command_signal_context_pattern** — shell_runshell, build_runbuild, stop_runstop, signalctx_signalctx [EXTRACTED 1.00]
- **shared_global_cfg_var** — root_initconfig, root_cfg, shell_runshell, build_runbuild, config_configshowcmd [EXTRACTED 1.00]
- **config_resolution_pipeline** — plan_plan_config, plan_merge_config, plan_walkup, plan_seedtooldefaults, plan_applyvalidationtail, config_config [EXTRACTED 1.00]
- **mount_pipeline_stages** — plan_plan_mountplan, merge_applymountsroot, merge_mergemounts, resolve_resolveall, workspace_workspacemirrorpath [EXTRACTED 1.00]
- **validation_tail** — plan_applyvalidationtail, config_validateshell, config_validatemountsroot [EXTRACTED 1.00]
- **** — catalog_entries, concept_dockerfile_bijection, concept_init_d_bijection [EXTRACTED 1.00]
- **** — sessionplan_plan, container_shell, container_execshell [EXTRACTED 1.00]
- **** — catalog_entries, concept_image_hash_invalidation, catalog_writecanonicalentries [EXTRACTED 1.00]

## Communities (40 total, 4 thin omitted)

### Community 0 - "Tool Catalog & Build Args"
Cohesion: 0.06
Nodes (33): BuildArg(), IsDefault(), Keys(), TestBuildArgLookup(), TestCanonicalEncodingDeterministic(), TestCanonicalEncodingIsNeutralToOptionalFieldPopulation(), TestIsDefaultMatchesLegacy(), TestKeysReturnsAllEntries() (+25 more)

### Community 1 - "CLI Commands (build/shell)"
Cohesion: 0.1
Nodes (29): TestPlanGlobalUnreadableIsBestEffort(), TestPlanWalksUpFromSubdir(), TestWalkUpHomeUnsetContinuesToRoot(), TestWalkUpIgnoresDirectoryNamedToolboxYaml(), TestWalkUpReturnsClosestMatch(), TestWalkUpStopsAtFilesystemRoot(), TestWalkUpStopsAtHome(), walkUp() (+21 more)

### Community 2 - "Shell Exec & Test Stubs"
Cohesion: 0.09
Nodes (28): runBuild(), resolveWorkspace(), runShell(), validateWorkspacePath(), TestSignalCtxReturnsCancellableContext(), TestValidateWorkspacePathAcceptsCommonPaths(), TestValidateWorkspacePathRejectsColon(), signalCtx() (+20 more)

### Community 3 - "Mount Defaults & Merge Tests"
Cohesion: 0.19
Nodes (29): Shell(), captureStderr(), stubExecShell(), testConfig(), testPlan(), testPlanWithCfg(), TestShellAutoBuildsCustomImage(), TestShellContainerNaming() (+21 more)

### Community 4 - "Config Plan Domain"
Cohesion: 0.11
Nodes (28): assertMount(), assertSymlink(), findMount(), TestDefaults(), applyMountsRoot(), mergeMounts(), TestApplyMountsRootDoesNotMutateBase(), TestMergeAnonymousAppend() (+20 more)

### Community 5 - "Container Runtime Architecture"
Cohesion: 0.08
Nodes (32): Config, HomeMountParents, Load, Mount, SupportedShells, ValidateMountsRoot, ValidateShell, defaults (+24 more)

### Community 6 - "Config Plan Tests"
Cohesion: 0.08
Nodes (26): Docker Sock GroupAdd Strategy, Pull Cache TTL, Session Pipeline, Container State Machine running/stopped/notfound, TTY Raw Mode and Signal Forwarding, dockerSockGroups, ensureImage, execShell (+18 more)

### Community 7 - "Cobra Command Registry"
Cohesion: 0.06
Nodes (30): CLI commands, code:bash (brew install filippolmt/tap/toolbox), code:bash (# Forward host port 7171 to the same port in the container.), code:bash (brew upgrade toolbox), code:bash (docker pull ghcr.io/filippolmt/toolbox:latest), code:bash (go install github.com/filippolmt/toolbox@latest), code:bash (toolbox version), code:bash (# Pull the toolbox image from GHCR) (+22 more)

### Community 8 - "Config Example Rendering"
Cohesion: 0.07
Nodes (26): Auth isolation under `~/.toolbox/`, Catalog entry → image hash, `cf` Cloudflare CLI skill auto-install, Claude Code env-var matrix, Codex nested sandbox, Container lifecycle, Docker CLI checksum, Host UID mapping (+18 more)

### Community 9 - "Args Validation Tests"
Cohesion: 0.1
Nodes (27): buildCmd, buildNoCache, runBuild, completionCmd, configCmd, configExampleCmd, configShowCmd, renderExampleYAML (+19 more)

### Community 10 - "Tool Defaults & Shell Cmd"
Cohesion: 0.12
Nodes (14): TestCatalogDockerfileBijection(), renderExampleYAML(), TestRenderExampleYAMLContainsAllToolsAndMounts(), TestWriteResolvedConfigDeterministic(), TestWriteResolvedConfigEmptyMounts(), TestWriteResolvedConfigNilConfigErrors(), writeResolvedConfig(), runInit() (+6 more)

### Community 11 - "Catalog Bijection Tests"
Cohesion: 0.14
Nodes (12): TestUsageArgsWraps(), resetCmdState(), TestInitConfigAppliesDefaults(), TestInitConfigExplicitFileIsRead(), TestInitConfigProjectFileFromCWD(), TestInitConfigProjectFileStopsAtHome(), TestInitConfigProjectFileWalksUpFromSubdir(), Execute() (+4 more)

### Community 12 - "Mount Resolve"
Cohesion: 0.17
Nodes (17): TestIsDefaultTools(), DefaultTools(), IsDefaultTools(), ContainerNameFor(), Merge(), normalizeWorkspace(), parsePublishSpecs(), Plan() (+9 more)

### Community 13 - "Docker Client Mock"
Cohesion: 0.1
Nodes (14): BuildArg, Defaults, Entries, IsDefault, Keys, WriteCanonical, WriteCanonicalEntries, D-09/D-10 Optional Field Hash Neutrality (+6 more)

### Community 14 - "Exec Shell & Attach"
Cohesion: 0.23
Nodes (13): ensureSource(), expandHome(), resolveAll(), TestExpandHome(), TestResolveAllCreatesMissingWhenRequested(), TestResolveAllKeepsNonEmptyDirEvenWithSymlinkFrom(), TestResolveAllReadOnlyMode(), TestResolveAllRelativeSourceCreatesUnderCWD() (+5 more)

### Community 16 - "Docker Sock Groups"
Cohesion: 0.24
Nodes (5): execShell(), TestExecShell_ContainerExecAttachError(), TestExecShell_ContainerExecCreateError(), TestExecShell_NonTTYStdin(), attachMock

### Community 17 - "Mount Parent Dirs"
Cohesion: 0.2
Nodes (9): code:block1 (~/.toolbox/startup.d/), code:block2 (mv ~/.toolbox/startup.d/gsd.sh ~/.toolbox/startup.d/gsd.sh.o), code:bash (#!/usr/bin/env bash), Disabling a hook, Examples, [`gsd.sh`](./gsd.sh) — Get-Shit-Done, How the hook runs, Startup hooks (+1 more)

### Community 18 - "Main Entry & Usage"
Cohesion: 0.25
Nodes (7): Config Plan, Context, Glossary, Init Sequence, Mount Plan, Session Plan, Tool Catalog

### Community 19 - "Shell Completion"
Cohesion: 0.48
Nodes (6): dockerSockGroups(), TestDockerSockGroupsAppendsHostGIDOnLinux(), TestDockerSockGroupsFallbackWhenStatFails(), TestDockerSockGroupsIncludesRootForDesktopCase(), TestDockerSockGroupsMatchesOnTargetNotSource(), TestDockerSockGroupsReturnsNilWhenSockNotMounted()

### Community 20 - "Version Cmd Init"
Cohesion: 0.29
Nodes (6): Architecture, CLAUDE.md, Code & language, Dev commands, Gotchas — backstory in [`docs/runtime-notes.md`](docs/runtime-notes.md), Project

### Community 21 - "Completion Test"
Cohesion: 0.29
Nodes (5): Architecture, Code & language, Dev commands, Gotchas — backstory in [`docs/runtime-notes.md`](docs/runtime-notes.md), Project

### Community 22 - "Version Test"
Cohesion: 0.47
Nodes (4): ParentDirs(), TestParentDirsDeduplicatesAndSorts(), TestParentDirsExcludesHomeRoot(), TestParentDirsSkipsTargetsOutsideHome()

### Community 23 - "Mount Defaults"
Cohesion: 0.5
Nodes (3): Commits & PRs, Contributing, Release flow

### Community 24 - "Init.d Bijection"
Cohesion: 0.5
Nodes (4): main, Execute, usageArgs, usageError

## Knowledge Gaps
- **102 isolated node(s):** `Result`, `Mount Plan`, `Tool Catalog`, `Config Plan`, `Session Plan` (+97 more)
  These have ≤1 connection - possible missing edges or undocumented components.
- **4 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `Shell()` connect `Mount Defaults & Merge Tests` to `Shell Completion`, `Shell Exec & Test Stubs`, `Tool Defaults & Shell Cmd`?**
  _High betweenness centrality (0.118) - this node is a cross-community bridge._
- **Why does `mkdirAll()` connect `CLI Commands (build/shell)` to `Shell Exec & Test Stubs`, `Catalog Bijection Tests`, `Exec Shell & Attach`?**
  _High betweenness centrality (0.108) - this node is a cross-community bridge._
- **Why does `Merge()` connect `Tool Catalog & Build Args` to `Config Plan Domain`?**
  _High betweenness centrality (0.098) - this node is a cross-community bridge._
- **Are the 23 inferred relationships involving `Shell()` (e.g. with `runShell()` and `Success()`) actually correct?**
  _`Shell()` has 23 INFERRED edges - model-reasoned connections that need verification._
- **Are the 11 inferred relationships involving `mkdirAll()` (e.g. with `TestInitConfigProjectFileWalksUpFromSubdir()` and `TestInitConfigProjectFileStopsAtHome()`) actually correct?**
  _`mkdirAll()` has 11 INFERRED edges - model-reasoned connections that need verification._
- **What connects `Result`, `Mount Plan`, `Tool Catalog` to the rest of the system?**
  _102 weakly-connected nodes found - possible documentation gaps or missing edges._
- **Should `Tool Catalog & Build Args` be split into smaller, more focused modules?**
  _Cohesion score 0.06 - nodes in this community are weakly interconnected._