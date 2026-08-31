package sessionplan

import "strings"

// worktreeExecCmd returns the command for the attached interactive session of
// a `toolbox worktree` run: launch the agent, then fall back to an interactive
// shell when it exits. Nil for every other session, which reuses Cmd.
//
// The container's main process stays the idle shell (Cmd), so the agent does
// not also run headless in PID 1. shellCmd is the resolved Cmd, so the
// `/bin/<shell>` form is written once, by ResolveShellCmd, and validated once.
func worktreeExecCmd(shellCmd []string, wt *WorktreeSession) []string {
	if wt == nil || len(shellCmd) == 0 {
		return nil
	}
	shell := shellCmd[0]
	return []string{shell, "-i", "-c", agentCommand(wt.Agent, wt.Prompt) + "; exec " + shell + " -i"}
}

// agentCommand composes the shell fragment that launches agent, optionally with
// an initial prompt. An empty prompt launches the agent bare; otherwise the
// prompt is passed as a single positional argument — the convention both
// supported agents (claude, codex, pi) follow. An agent needing different
// ergonomics (e.g. a --task flag) would branch on agent here.
func agentCommand(agent, prompt string) string {
	if prompt == "" {
		return agent
	}
	return agent + " " + shellSingleQuote(prompt)
}

// shellSingleQuote wraps s in single quotes for safe inclusion in a shell -c
// string: everything inside single quotes is literal, so command substitution,
// backticks and semicolons in a user-supplied prompt cannot expand or inject
// commands into the session wrapper. The only quoting concern is an embedded
// single quote, escaped the standard way by closing the quote, adding an
// escaped quote, then reopening.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
