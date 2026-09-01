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
	return []string{shell, "-i", "-c", agentCommand(wt.Agent, wt.Prompt, wt.Resume) + "; exec " + shell + " -i"}
}

// agentCommand composes the shell fragment that launches agent in one of three
// modes: resume, prompt, or bare.
//
// Resume is the one that has to branch per agent, and this is the spot the
// doc comment below always reserved for it: the two supported agents spell it
// differently — claude takes a flag, codex a subcommand — so there is no
// shared shape to factor out. Most-recent rather than by session id on both,
// which is what keeps toolbox out of an agent's on-disk conversation store.
// Resume outranks a prompt because the only caller that sets it is a reload,
// which has already dropped the prompt as spent.
//
// An empty prompt launches the agent bare; otherwise the prompt is passed as a
// single positional argument — the convention both agents follow. An agent
// needing different ergonomics (e.g. a --task flag) would branch here too.
func agentCommand(agent, prompt string, resume bool) string {
	if resume {
		switch agent {
		case "codex":
			return agent + " resume --last"
		default:
			return agent + " --continue"
		}
	}
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
