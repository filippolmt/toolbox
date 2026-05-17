---
type: "query"
date: "2026-05-17T07:56:31.164501+00:00"
question: "Why does Shell() bridge Container Lifecycle to CLI Subcommands, SDD Env Contract, Image Plan, Run Plan, Container Attach?"
contributor: "graphify"
source_nodes: ["Shell()", "Refresh()", "Compute()", "execShell()", "sddEnv()", "runShell()"]
---

# Q: Why does Shell() bridge Container Lifecycle to CLI Subcommands, SDD Env Contract, Image Plan, Run Plan, Container Attach?

## Answer

Shell() in internal/container/lifecycle.go:103 is the ContainerCreate/Start/Attach orchestrator for 'toolbox shell'. It bridges five communities because it sits at the seam where the CLI hands off to the container runtime — each community contributes one piece of what Shell() needs at create time, all flowing in through a single sessionplan.SessionPlan.

CLI Subcommands (c0): cmd/shell.go runShell() builds the *config.Config via initConfig, calls sessionplan.Plan(cfg, ws, ports, version) which returns the full bind/env/cmd/securityOpt set, then invokes container.Shell(ctx, cli, plan). One INFERRED reverse edge: runShell -> Shell. Plus container.Stop, StopByName, StopAll, OnShellExit live in the same package and are called by other cmd/ subcommands (stop.go), reinforcing the bridge.

SDD Env Contract (c1): sessionplan.shellEnv now appends sddEnv(workspace, cfg.SDD) entries (TOOLBOX_SDD_ENABLED, TOOLBOX_SDD_WORKSPACE_HASH, per-skill TOOLBOX_SDD_<KEY>_{PKG,VERSION,BIN,STEPS,MARKER}) onto SessionPlan.Env. Shell() consumes that slice unchanged when it calls ContainerCreate, so Shell is the runtime sink that materializes the SDD bootstrap contract. The 3-hop path Shell -> MissingPublishPorts -> plan.go -> sddEnv reflects that they share the same plan.go file even though sddEnv is package-private.

Image Plan (c10): direct EXTRACTED edge Shell -> Refresh (imageplan.Refresh, which fans out to imagepull.RefreshIfStale -> record). Shell calls Refresh before ContainerCreate to guarantee the image tag exists locally, with cached/pull/record TTL logic.

Run Plan Pipeline (c15): direct INFERRED edge Shell -> Compute (runplan.Compute returns the Action enum: Reuse, RestartExisting, CreateNew, EnsureLocalImage). dispatchOp at lifecycle.go:146 switches on that Action and only then calls createAndStart. Compute is the decision seam Shell delegates to before any side effect.

Container Attach (c16): direct EXTRACTED edge Shell -> execShell (container.attach.execShell, the docker exec ATTACH for the interactive TTY). This is the terminal handoff once createAndStart has placed the container in 'running' state.

Why this matters: Shell() has the highest betweenness centrality in the graph (0.158) precisely because every 'toolbox shell' invocation traverses CLI -> sessionplan -> imageplan -> runplan -> container -> attach, and Shell is the single pivot stitching them. The 24 INFERRED edges around it are mostly TestShell* call-sites + cross-package fan-out — verifiable by reading lifecycle_test.go. The one INFERRED edge worth a closer look is Shell -> Compute: the call is real (lifecycle.go:158 region) but the AST didn't catch it, so semantic extraction filled the gap.

## Source Nodes

- Shell()
- Refresh()
- Compute()
- execShell()
- sddEnv()
- runShell()