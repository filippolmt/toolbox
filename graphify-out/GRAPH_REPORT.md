# Graph Report - .  (2026-05-17)

## Corpus Check
- 68 files · ~42,052 words
- Verdict: corpus is large enough that graph structure adds value.

## Summary
- 449 nodes · 811 edges · 29 communities (27 shown, 2 thin omitted)
- Extraction: 68% EXTRACTED · 32% INFERRED · 0% AMBIGUOUS · INFERRED: 260 edges (avg confidence: 0.8)
- Token cost: 0 input · 0 output

## Community Hubs (Navigation)
- [[_COMMUNITY_CLI Init + Lifecycle|CLI Init + Lifecycle]]
- [[_COMMUNITY_Tool Catalog + Config|Tool Catalog + Config]]
- [[_COMMUNITY_Named Shell Bootstrap|Named Shell Bootstrap]]
- [[_COMMUNITY_Container Lifecycle Tests|Container Lifecycle Tests]]
- [[_COMMUNITY_Mount Merge|Mount Merge]]
- [[_COMMUNITY_Config Plan Tests|Config Plan Tests]]
- [[_COMMUNITY_Session Plan Core|Session Plan Core]]
- [[_COMMUNITY_CLI Args Validation|CLI Args Validation]]
- [[_COMMUNITY_Image Pull Cache|Image Pull Cache]]
- [[_COMMUNITY_Config IO Atomic Write|Config IO Atomic Write]]
- [[_COMMUNITY_Mount Path Resolution|Mount Path Resolution]]
- [[_COMMUNITY_Run Plan State Machine|Run Plan State Machine]]
- [[_COMMUNITY_Mount Plan + Workspace|Mount Plan + Workspace]]
- [[_COMMUNITY_Docker Identity Resolution|Docker Identity Resolution]]
- [[_COMMUNITY_Toolbox Init Command|Toolbox Init Command]]
- [[_COMMUNITY_Image Plan Ensure|Image Plan Ensure]]
- [[_COMMUNITY_Container Mock Client|Container Mock Client]]
- [[_COMMUNITY_Container Exec Shell|Container Exec Shell]]
- [[_COMMUNITY_Config Subcommand|Config Subcommand]]
- [[_COMMUNITY_Teardown Mock Client|Teardown Mock Client]]
- [[_COMMUNITY_Parent Dir Materialization|Parent Dir Materialization]]

## God Nodes (most connected - your core abstractions)
1. `Shell()` - 27 edges
2. `mkdirAll()` - 25 edges
3. `stubExecShell()` - 19 edges
4. `testConfig()` - 18 edges
5. `testWorkspace()` - 18 edges
6. `testPlan()` - 17 edges
7. `resolveShellWorkspace()` - 17 edges
8. `resolveAll()` - 13 edges
9. `mergeMounts()` - 13 edges
10. `findMount()` - 13 edges

## Surprising Connections (you probably didn't know these)
- `TestInitConfigExplicitFileIsRead()` --calls--> `Keys()`  [INFERRED]
  cmd/config_test.go → internal/catalog/catalog.go
- `TestInitConfigAppliesDefaults()` --calls--> `Keys()`  [INFERRED]
  cmd/config_test.go → internal/catalog/catalog.go
- `runStop()` --calls--> `ResolveExplicit()`  [INFERRED]
  cmd/stop.go → internal/workspace/workspace.go
- `ensureNamedShellPath()` --calls--> `mkdirAll()`  [INFERRED]
  cmd/shell_named.go → internal/sessionplan/plan_test.go
- `TestEnsureNamedShellPathRejectsSymlink()` --calls--> `mkdirAll()`  [INFERRED]
  cmd/shell_named_test.go → internal/sessionplan/plan_test.go

## Communities (29 total, 2 thin omitted)

### Community 0 - "CLI Init + Lifecycle"
Cohesion: 0.07
Nodes (36): runBuild(), runShell(), signalCtx(), TestSignalCtxReturnsCancellableContext(), runStop(), createAndStart(), dispatchOp(), formatPublishMismatch() (+28 more)

### Community 1 - "Tool Catalog + Config"
Cohesion: 0.06
Nodes (33): BuildArg(), IsDefault(), Keys(), TestBuildArgLookup(), TestCanonicalEncodingDeterministic(), TestCanonicalEncodingIsNeutralToOptionalFieldPopulation(), TestIsDefaultMatchesLegacy(), TestKeysReturnsAllEntries() (+25 more)

### Community 2 - "Named Shell Bootstrap"
Cohesion: 0.12
Nodes (32): bootstrapMissingNamedShell(), createHint(), defaultShellPath(), ensureNamedShellPath(), missingPathHint(), missingShellHint(), promptPath(), promptYesNo() (+24 more)

### Community 3 - "Container Lifecycle Tests"
Cohesion: 0.18
Nodes (30): DefaultTools(), Shell(), captureStderr(), stubExecShell(), testConfig(), testPlan(), testPlanWithCfg(), TestShellAutoBuildsCustomImage() (+22 more)

### Community 4 - "Mount Merge"
Cohesion: 0.11
Nodes (28): assertMount(), assertSymlink(), findMount(), TestDefaults(), applyMountsRoot(), mergeMounts(), TestApplyMountsRootDoesNotMutateBase(), TestMergeAnonymousAppend() (+20 more)

### Community 5 - "Config Plan Tests"
Cohesion: 0.14
Nodes (26): TestPlanGlobalUnreadableIsBestEffort(), TestPlanWalksUpFromSubdir(), TestWalkUpHomeUnsetContinuesToRoot(), TestWalkUpIgnoresDirectoryNamedToolboxYaml(), TestWalkUpReturnsClosestMatch(), TestWalkUpStopsAtFilesystemRoot(), TestWalkUpStopsAtHome(), walkUp() (+18 more)

### Community 6 - "Session Plan Core"
Cohesion: 0.15
Nodes (19): Image, MergedSessionPlan, ContainerNameFor(), Merge(), MissingPublishPorts(), normalizeWorkspace(), parsePublishSpecs(), Plan() (+11 more)

### Community 7 - "CLI Args Validation"
Cohesion: 0.16
Nodes (11): TestUsageArgsWraps(), resetCmdState(), TestInitConfigAppliesDefaults(), TestInitConfigExplicitFileIsRead(), TestInitConfigProjectFileFromCWD(), TestInitConfigProjectFileStopsAtHome(), TestInitConfigProjectFileWalksUpFromSubdir(), Execute() (+3 more)

### Community 8 - "Image Pull Cache"
Cohesion: 0.24
Nodes (15): cached(), isAuthError(), markerPath(), pull(), record(), RefreshIfStale(), registryOf(), TestCachedFreshMarker() (+7 more)

### Community 9 - "Config IO Atomic Write"
Cohesion: 0.26
Nodes (14): TestUpsertShellInUserConfigPreservesExistingFields(), upsertShellInUserConfig(), AtomicWriteFile(), EnsureChildMap(), EnsureDocumentMap(), findOrAppendPair(), GlobalConfigDir(), GlobalConfigPath() (+6 more)

### Community 10 - "Mount Path Resolution"
Cohesion: 0.23
Nodes (13): ensureSource(), expandHome(), resolveAll(), TestExpandHome(), TestResolveAllCreatesMissingWhenRequested(), TestResolveAllKeepsNonEmptyDirEvenWithSymlinkFrom(), TestResolveAllReadOnlyMode(), TestResolveAllRelativeSourceCreatesUnderCWD() (+5 more)

### Community 11 - "Run Plan State Machine"
Cohesion: 0.17
Nodes (10): Action, notFoundErr, Op, Compute(), TestActionString(), TestComputeActionConnectWhenRunning(), TestComputeActionCreateWhenNilContainerJSONBase(), TestComputeActionCreateWhenNotFound() (+2 more)

### Community 12 - "Mount Plan + Workspace"
Cohesion: 0.14
Nodes (8): Bind, Defaults(), Merge(), Plan(), TestPlanEndToEnd(), Result, TestWorkspaceMirrorPath(), WorkspaceMirrorPath()

### Community 13 - "Docker Identity Resolution"
Cohesion: 0.22
Nodes (8): dockerSockGroups(), Resolve(), TestDockerSockGroupsAppendsHostGIDOnLinux(), TestDockerSockGroupsFallbackWhenStatFails(), TestDockerSockGroupsIncludesRootForDesktopCase(), TestDockerSockGroupsMatchesOnTargetNotSource(), TestDockerSockGroupsReturnsNilWhenSockNotMounted(), Identity

### Community 14 - "Toolbox Init Command"
Cohesion: 0.24
Nodes (7): runInit(), chdirTemp(), TestInitForceOverwrites(), TestInitRefusesOverwriteWithoutForce(), TestInitWritesAnnotatedYAML(), Render(), TestRenderContainsAllToolsAndMounts()

### Community 15 - "Image Plan Ensure"
Cohesion: 0.17
Nodes (3): Refresh(), TestRefreshNoOpForLocalImage(), mockClient

### Community 17 - "Container Exec Shell"
Cohesion: 0.24
Nodes (5): execShell(), TestExecShell_ContainerExecAttachError(), TestExecShell_ContainerExecCreateError(), TestExecShell_NonTTYStdin(), attachMock

### Community 18 - "Config Subcommand"
Cohesion: 0.38
Nodes (4): TestWriteResolvedConfigDeterministic(), TestWriteResolvedConfigEmptyMounts(), TestWriteResolvedConfigNilConfigErrors(), writeResolvedConfig()

### Community 20 - "Parent Dir Materialization"
Cohesion: 0.47
Nodes (4): ParentDirs(), TestParentDirsDeduplicatesAndSorts(), TestParentDirsExcludesHomeRoot(), TestParentDirsSkipsTargetsOutsideHome()

## Knowledge Gaps
- **10 isolated node(s):** `Result`, `Identity`, `Config`, `NamedShell`, `Mount` (+5 more)
  These have ≤1 connection - possible missing edges or undocumented components.
- **2 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `mkdirAll()` connect `Config Plan Tests` to `Named Shell Bootstrap`, `CLI Args Validation`, `Image Pull Cache`, `Mount Path Resolution`, `Mount Plan + Workspace`?**
  _High betweenness centrality (0.255) - this node is a cross-community bridge._
- **Why does `Shell()` connect `Container Lifecycle Tests` to `CLI Init + Lifecycle`, `Run Plan State Machine`, `Session Plan Core`, `Image Plan Ensure`?**
  _High betweenness centrality (0.220) - this node is a cross-community bridge._
- **Why does `WorkspaceMirrorPath()` connect `Mount Plan + Workspace` to `Container Lifecycle Tests`, `Config Plan Tests`, `Session Plan Core`?**
  _High betweenness centrality (0.154) - this node is a cross-community bridge._
- **Are the 24 inferred relationships involving `Shell()` (e.g. with `Warning()` and `Refresh()`) actually correct?**
  _`Shell()` has 24 INFERRED edges - model-reasoned connections that need verification._
- **Are the 13 inferred relationships involving `mkdirAll()` (e.g. with `ensureSource()` and `TestPlanEndToEnd()`) actually correct?**
  _`mkdirAll()` has 13 INFERRED edges - model-reasoned connections that need verification._
- **What connects `Result`, `Identity`, `Config` to the rest of the system?**
  _10 weakly-connected nodes found - possible documentation gaps or missing edges._
- **Should `CLI Init + Lifecycle` be split into smaller, more focused modules?**
  _Cohesion score 0.07 - nodes in this community are weakly interconnected._