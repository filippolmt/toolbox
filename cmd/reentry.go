package cmd

import (
	"github.com/spf13/pflag"
)

// reentryNonIdempotent names the `shell` flags the re-entry form drops. Both
// belong to the one-shot bootstrap half of the invocation, already performed
// by the process being replaced: by the time the form is replayed the named
// shell exists in ~/.toolbox.yaml, so re-entry resolves it from config. This
// is the same normalisation that turns `worktree create` into
// `worktree open <branch>`, applied to the other command that has a create half.
var reentryNonIdempotent = map[string]bool{"create": true, "path": true}

// reentryFlags re-renders the flags the developer actually typed, so the
// re-entry form recreates *this* session rather than a default one. The flags
// are not decoration: --profile and --peer feed the container name, --profile
// also moves the mount root, and -p fixes the port bindings at creation, so a
// form that dropped them would have the reloaded process destroy the container
// named in the payload and then create a different one.
//
// Visit walks only the flags marked Changed, in lexical order, so a flag added
// to the command later is carried without anyone remembering a list here — and
// a flag left at its default is never emitted, which is what keeps the
// tri-state --peer resolving against the config key rather than against a
// default this form invented.
func reentryFlags(flags *pflag.FlagSet, skip map[string]bool) []string {
	var out []string
	flags.Visit(func(f *pflag.Flag) {
		if skip[f.Name] {
			return
		}
		name := "--" + f.Name
		if sv, ok := f.Value.(pflag.SliceValue); ok {
			// One pair per element: a slice flag's String() renders "[a,b]",
			// which does not parse back into the same slice.
			for _, v := range sv.GetSlice() {
				out = append(out, name, v)
			}
			return
		}
		if f.Value.Type() == "bool" {
			// --peer=false is meaningful and `--peer false` does not parse:
			// pflag reads the bool from the attached value or not at all.
			out = append(out, name+"="+f.Value.String())
			return
		}
		out = append(out, name, f.Value.String())
	})
	return out
}

// shellReentry renders the re-entry form of a `shell` invocation: the
// subcommand, the positional that names the session, then the typed flags.
// `toolbox shell` and `toolbox shell <name>` are idempotent and promptless, so
// the positional needs no normalisation — only the bootstrap flags do.
func shellReentry(flags *pflag.FlagSet, args []string) []string {
	argv := append([]string{"shell"}, args...)
	return append(argv, reentryFlags(flags, reentryNonIdempotent)...)
}

// worktreeReentry renders the re-entry form of a worktree session. The command
// is normalised to `open`, never replayed as `create`: create would fail on a
// branch that now exists and would re-send a prompt the agent has already
// completed, while open is idempotent and promptless by construction.
//
// The agent carried is the *resolved* one rather than the flag as typed, so a
// session started with no --agent still comes back on the agent it actually
// ran — the resume relaunches `claude --continue` or `codex resume --last`
// against the right lineage even if the config default changes underneath.
func worktreeReentry(branch, agent string) []string {
	argv := []string{"worktree", "open", branch}
	if agent == "" {
		return argv
	}
	return append(argv, "--agent", agent)
}
