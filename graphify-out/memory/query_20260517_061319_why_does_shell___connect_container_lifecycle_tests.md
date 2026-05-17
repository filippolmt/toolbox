---
type: "query"
date: "2026-05-17T06:13:19.558063+00:00"
question: "Why does Shell() connect Container Lifecycle Tests to CLI Init + Lifecycle, Run Plan State Machine, Session Plan Core, Image Plan Ensure?"
contributor: "graphify"
source_nodes: ["Shell()", "Compute()", "Refresh()", "MissingPublishPorts()", "runShell()"]
---

# Q: Why does Shell() connect Container Lifecycle Tests to CLI Init + Lifecycle, Run Plan State Machine, Session Plan Core, Image Plan Ensure?

## Answer

Shell() at internal/container/lifecycle.go:103 is a coordination-only function (no inline business logic, only typed-seam dispatch). 27 edges across 5 communities: 18 incoming from C3 TestShell* (test SUT), 6 mixed with C0 intra-package (formatPublishMismatch, dispatchOp, OnShellExit, runShell), and 1 outgoing each to C6 Session Plan (MissingPublishPorts), C11 Run Plan (Compute), C15 Image Plan (Refresh). The high betweenness (0.220) exists because C3 test community would otherwise dissolve across 4 plan packages — Shell() is the single entry point where typed pipelines (config → mountplan → sessionplan) become Docker side-effects, matching the public-seam contract documented at lifecycle.go:7-16.

## Source Nodes

- Shell()
- Compute()
- Refresh()
- MissingPublishPorts()
- runShell()