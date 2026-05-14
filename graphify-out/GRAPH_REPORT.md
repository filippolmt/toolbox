# Graph Report - toolbox  (2026-05-14)

## Corpus Check
- 65 files · ~40,184 words
- Verdict: corpus is large enough that graph structure adds value.

## Summary
- 584 nodes · 918 edges · 42 communities (38 shown, 4 thin omitted)
- Extraction: 72% EXTRACTED · 28% INFERRED · 0% AMBIGUOUS · INFERRED: 256 edges (avg confidence: 0.8)
- Token cost: 0 input · 0 output

## Graph Freshness
- Built from commit: `de7795a9`
- Run `git rev-parse HEAD` and compare to check if the graph is stale.
- Run `graphify update .` after code changes (no API cost).

## Community Hubs (Navigation)
- [[_COMMUNITY_Community 0|Community 0]]
- [[_COMMUNITY_Community 1|Community 1]]
- [[_COMMUNITY_Community 2|Community 2]]
- [[_COMMUNITY_Community 3|Community 3]]
- [[_COMMUNITY_Community 4|Community 4]]
- [[_COMMUNITY_Community 5|Community 5]]
- [[_COMMUNITY_Community 6|Community 6]]
- [[_COMMUNITY_Community 7|Community 7]]
- [[_COMMUNITY_Community 8|Community 8]]
- [[_COMMUNITY_Community 9|Community 9]]
- [[_COMMUNITY_Community 10|Community 10]]
- [[_COMMUNITY_Community 11|Community 11]]
- [[_COMMUNITY_Community 12|Community 12]]
- [[_COMMUNITY_Community 13|Community 13]]
- [[_COMMUNITY_Community 14|Community 14]]
- [[_COMMUNITY_Community 15|Community 15]]
- [[_COMMUNITY_Community 16|Community 16]]
- [[_COMMUNITY_Community 17|Community 17]]
- [[_COMMUNITY_Community 18|Community 18]]
- [[_COMMUNITY_Community 19|Community 19]]
- [[_COMMUNITY_Community 20|Community 20]]
- [[_COMMUNITY_Community 21|Community 21]]
- [[_COMMUNITY_Community 22|Community 22]]
- [[_COMMUNITY_Community 23|Community 23]]
- [[_COMMUNITY_Community 24|Community 24]]
- [[_COMMUNITY_Community 25|Community 25]]
- [[_COMMUNITY_Community 32|Community 32]]
- [[_COMMUNITY_Community 34|Community 34]]
- [[_COMMUNITY_Community 39|Community 39]]
- [[_COMMUNITY_Community 41|Community 41]]

## God Nodes (most connected - your core abstractions)
1. `Shell()` - 33 edges
2. `mkdirAll()` - 24 edges
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
- `runBuild()` --calls--> `Info()`  [INFERRED]
  cmd/build.go → internal/ui/output.go
- `runShell()` --calls--> `Shell()`  [INFERRED]
  cmd/shell.go → internal/container/lifecycle.go
- `TestInitConfigExplicitFileIsRead()` --calls--> `Keys()`  [INFERRED]
  cmd/config_test.go → internal/catalog/catalog.go
- `TestInitConfigProjectFileWalksUpFromSubdir()` --calls--> `mkdirAll()`  [INFERRED]
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

## Communities (42 total, 4 thin omitted)

### Community 0 - "Community 0"
Cohesion: 0.09
Nodes (23): runBuild(), resolveWorkspace(), runShell(), validateWorkspacePath(), TestSignalCtxReturnsCancellableContext(), TestValidateWorkspacePathAcceptsCommonPaths(), TestValidateWorkspacePathRejectsColon(), signalCtx() (+15 more)

### Community 1 - "Community 1"
Cohesion: 0.07
Nodes (28): BuildArg(), IsDefault(), Keys(), TestBuildArgLookup(), TestCanonicalEncodingDeterministic(), TestCanonicalEncodingIsNeutralToOptionalFieldPopulation(), TestIsDefaultMatchesLegacy(), TestKeysReturnsAllEntries() (+20 more)

### Community 2 - "Community 2"
Cohesion: 0.19
Nodes (29): Shell(), captureStderr(), stubExecShell(), testConfig(), testPlan(), testPlanWithCfg(), TestShellAutoBuildsCustomImage(), TestShellContainerNaming() (+21 more)

### Community 3 - "Community 3"
Cohesion: 0.11
Nodes (28): assertMount(), assertSymlink(), findMount(), TestDefaults(), applyMountsRoot(), mergeMounts(), TestApplyMountsRootDoesNotMutateBase(), TestMergeAnonymousAppend() (+20 more)

### Community 4 - "Community 4"
Cohesion: 0.08
Nodes (32): Config, HomeMountParents, Load, Mount, SupportedShells, ValidateMountsRoot, ValidateShell, defaults (+24 more)

### Community 5 - "Community 5"
Cohesion: 0.08
Nodes (26): Docker Sock GroupAdd Strategy, Pull Cache TTL, Session Pipeline, Container State Machine running/stopped/notfound, TTY Raw Mode and Signal Forwarding, dockerSockGroups, ensureImage, execShell (+18 more)

### Community 6 - "Community 6"
Cohesion: 0.14
Nodes (26): TestPlanGlobalUnreadableIsBestEffort(), TestPlanWalksUpFromSubdir(), TestWalkUpHomeUnsetContinuesToRoot(), TestWalkUpIgnoresDirectoryNamedToolboxYaml(), TestWalkUpReturnsClosestMatch(), TestWalkUpStopsAtFilesystemRoot(), TestWalkUpStopsAtHome(), walkUp() (+18 more)

### Community 7 - "Community 7"
Cohesion: 0.06
Nodes (32): CLI commands, code:bash (brew install filippolmt/tap/toolbox), code:bash (# Forward host port 7171 to the same port in the container.), code:bash (toolbox shell -p 8976:8976), code:bash (brew upgrade toolbox), code:bash (docker pull ghcr.io/filippolmt/toolbox:latest), code:bash (go install github.com/filippolmt/toolbox@latest), code:bash (toolbox version) (+24 more)

### Community 8 - "Community 8"
Cohesion: 0.07
Nodes (26): Auth isolation under `~/.toolbox/`, Catalog entry → image hash, `cf` Cloudflare CLI skill auto-install, Claude Code env-var matrix, Codex nested sandbox, Container lifecycle, Docker CLI checksum, Host UID mapping (+18 more)

### Community 9 - "Community 9"
Cohesion: 0.1
Nodes (27): buildCmd, buildNoCache, runBuild, completionCmd, configCmd, configExampleCmd, configShowCmd, renderExampleYAML (+19 more)

### Community 10 - "Community 10"
Cohesion: 0.1
Nodes (17): TestCatalogDockerfileBijection(), renderExampleYAML(), TestRenderExampleYAMLContainsAllToolsAndMounts(), TestWriteResolvedConfigDeterministic(), TestWriteResolvedConfigEmptyMounts(), TestWriteResolvedConfigNilConfigErrors(), writeResolvedConfig(), runInit() (+9 more)

### Community 11 - "Community 11"
Cohesion: 0.14
Nodes (12): TestUsageArgsWraps(), resetCmdState(), TestInitConfigAppliesDefaults(), TestInitConfigExplicitFileIsRead(), TestInitConfigProjectFileFromCWD(), TestInitConfigProjectFileStopsAtHome(), TestInitConfigProjectFileWalksUpFromSubdir(), Execute() (+4 more)

### Community 12 - "Community 12"
Cohesion: 0.11
Nodes (25): TestIsDefaultTools(), DefaultTools(), IsDefaultTools(), ContainerNameFor(), Merge(), normalizeWorkspace(), parsePublishSpecs(), Plan() (+17 more)

### Community 13 - "Community 13"
Cohesion: 0.1
Nodes (14): BuildArg, Defaults, Entries, IsDefault, Keys, WriteCanonical, WriteCanonicalEntries, D-09/D-10 Optional Field Hash Neutrality (+6 more)

### Community 14 - "Community 14"
Cohesion: 0.23
Nodes (13): ensureSource(), expandHome(), resolveAll(), TestExpandHome(), TestResolveAllCreatesMissingWhenRequested(), TestResolveAllKeepsNonEmptyDirEvenWithSymlinkFrom(), TestResolveAllReadOnlyMode(), TestResolveAllRelativeSourceCreatesUnderCWD() (+5 more)

### Community 15 - "Community 15"
Cohesion: 0.15
Nodes (8): Bind, Defaults(), Merge(), Plan(), TestPlanEndToEnd(), Result, TestWorkspaceMirrorPath(), WorkspaceMirrorPath()

### Community 17 - "Community 17"
Cohesion: 0.24
Nodes (5): execShell(), TestExecShell_ContainerExecAttachError(), TestExecShell_ContainerExecCreateError(), TestExecShell_NonTTYStdin(), attachMock

### Community 18 - "Community 18"
Cohesion: 0.2
Nodes (9): code:block1 (~/.toolbox/startup.d/), code:block2 (mv ~/.toolbox/startup.d/gsd.sh ~/.toolbox/startup.d/gsd.sh.o), code:bash (#!/usr/bin/env bash), Disabling a hook, Examples, [`gsd.sh`](./gsd.sh) — Get-Shit-Done, How the hook runs, Startup hooks (+1 more)

### Community 19 - "Community 19"
Cohesion: 0.25
Nodes (7): Config Plan, Context, Glossary, Init Sequence, Mount Plan, Session Plan, Tool Catalog

### Community 20 - "Community 20"
Cohesion: 0.48
Nodes (6): dockerSockGroups(), TestDockerSockGroupsAppendsHostGIDOnLinux(), TestDockerSockGroupsFallbackWhenStatFails(), TestDockerSockGroupsIncludesRootForDesktopCase(), TestDockerSockGroupsMatchesOnTargetNotSource(), TestDockerSockGroupsReturnsNilWhenSockNotMounted()

### Community 21 - "Community 21"
Cohesion: 0.29
Nodes (5): Architecture, Code & language, Dev commands, Gotchas — backstory in [`docs/runtime-notes.md`](docs/runtime-notes.md), Project

### Community 22 - "Community 22"
Cohesion: 0.29
Nodes (6): Architecture, CLAUDE.md, Code & language, Dev commands, Gotchas — backstory in [`docs/runtime-notes.md`](docs/runtime-notes.md), Project

### Community 23 - "Community 23"
Cohesion: 0.47
Nodes (4): ParentDirs(), TestParentDirsDeduplicatesAndSorts(), TestParentDirsExcludesHomeRoot(), TestParentDirsSkipsTargetsOutsideHome()

### Community 24 - "Community 24"
Cohesion: 0.5
Nodes (3): Commits & PRs, Contributing, Release flow

### Community 25 - "Community 25"
Cohesion: 0.5
Nodes (4): main, Execute, usageArgs, usageError

### Community 41 - "Community 41"
Cohesion: 0.16
Nodes (24): pullImage(), cached(), isAuthError(), markerPath(), pull(), record(), RefreshIfStale(), registryOf() (+16 more)

## Knowledge Gaps
- **102 isolated node(s):** `Result`, `Mount Plan`, `Tool Catalog`, `Config Plan`, `Session Plan` (+97 more)
  These have ≤1 connection - possible missing edges or undocumented components.
- **4 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `Shell()` connect `Community 2` to `Community 0`, `Community 41`, `Community 10`, `Community 20`?**
  _High betweenness centrality (0.127) - this node is a cross-community bridge._
- **Why does `mkdirAll()` connect `Community 6` to `Community 0`, `Community 41`, `Community 11`, `Community 14`, `Community 15`?**
  _High betweenness centrality (0.122) - this node is a cross-community bridge._
- **Why does `Merge()` connect `Community 15` to `Community 1`, `Community 3`?**
  _High betweenness centrality (0.093) - this node is a cross-community bridge._
- **Are the 24 inferred relationships involving `Shell()` (e.g. with `runShell()` and `Warning()`) actually correct?**
  _`Shell()` has 24 INFERRED edges - model-reasoned connections that need verification._
- **Are the 12 inferred relationships involving `mkdirAll()` (e.g. with `TestInitConfigProjectFileWalksUpFromSubdir()` and `TestInitConfigProjectFileStopsAtHome()`) actually correct?**
  _`mkdirAll()` has 12 INFERRED edges - model-reasoned connections that need verification._
- **What connects `Result`, `Mount Plan`, `Tool Catalog` to the rest of the system?**
  _102 weakly-connected nodes found - possible documentation gaps or missing edges._
- **Should `Community 0` be split into smaller, more focused modules?**
  _Cohesion score 0.09 - nodes in this community are weakly interconnected._