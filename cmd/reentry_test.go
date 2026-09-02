package cmd

import (
	"reflect"
	"testing"

	"github.com/spf13/pflag"
)

// shellFlagSet mirrors the `shell` flags on a throwaway set, so a rendering
// assertion can parse a command line without mutating the real command's
// global flag state. The drift risk this creates is covered by
// TestShellReentryClassifiesEveryFlag, which reads the real command.
func shellFlagSet(t *testing.T, argv ...string) *pflag.FlagSet {
	t.Helper()
	fs := pflag.NewFlagSet("shell", pflag.ContinueOnError)
	var (
		publish, oauth, share []string
		bridge, peer, create  bool
		profile, path         string
	)
	fs.StringSliceVarP(&publish, "publish", "p", nil, "")
	fs.StringSliceVar(&oauth, "oauth", nil, "")
	fs.StringSliceVar(&share, "share", nil, "")
	fs.BoolVarP(&bridge, "bridge-loopback", "B", false, "")
	fs.BoolVar(&peer, "peer", false, "")
	fs.BoolVar(&create, "create", false, "")
	fs.StringVar(&profile, "profile", "", "")
	fs.StringVar(&path, "path", "", "")
	if err := fs.Parse(argv); err != nil {
		t.Fatalf("parse %v: %v", argv, err)
	}
	return fs
}

// TestReentryFlagsRendersWhatWasTyped pins the three rendering rules the form
// depends on to parse back: a slice flag becomes one --flag <value> pair per
// element (its String() renders "[a,b]", which does not parse), a bool uses
// the --flag=value form (--peer=false is meaningful and --peer false is not),
// and everything else is a --flag <value> pair.
func TestReentryFlagsRendersWhatWasTyped(t *testing.T) {
	fs := shellFlagSet(t, "-p", "7171", "-p", "8080:80", "-B",
		"--peer=false", "--profile", "work", "--share", "gh", "--create")

	got := reentryFlags(fs, reentryNonIdempotent)
	want := []string{
		"--bridge-loopback=true",
		"--peer=false",
		"--profile", "work",
		"--publish", "7171",
		"--publish", "8080:80",
		"--share", "gh",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("reentryFlags = %q, want %q", got, want)
	}
}

// TestReentryFlagsOmitsWhatWasNotTyped guards the tri-state flags. Emitting a
// flag left at its default would pin that default into the next process:
// --peer=false against a peer_messaging: true config resolves to a different
// container name, so the reload would recreate the session somewhere else.
func TestReentryFlagsOmitsWhatWasNotTyped(t *testing.T) {
	if got := reentryFlags(shellFlagSet(t), reentryNonIdempotent); len(got) != 0 {
		t.Errorf("reentryFlags on an untouched set = %q, want none", got)
	}
}

// TestShellReentryKeepsThePositionalAndTheFlags asserts the whole form: the
// subcommand, the positional argument that names the session, then the typed
// flags. The bootstrap half (--create/--path) is dropped — by the time the
// form is replayed the named shell exists in config, so re-entry resolves it.
func TestShellReentryKeepsThePositionalAndTheFlags(t *testing.T) {
	fs := shellFlagSet(t, "--create", "--path", "/tmp/api", "--profile", "work", "--oauth", "gh")

	got := shellReentry(fs, []string{"api"})
	want := []string{"shell", "api", "--oauth", "gh", "--profile", "work"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("shellReentry = %q, want %q", got, want)
	}
}

// TestShellReentryClassifiesEveryFlag is the drift guard. The first version of
// the re-entry form carried no flags at all, so a reload of a --profile
// session destroyed the profile container named in the payload and created a
// different, non-profile one with different mounts and no published ports. A
// flag added to `shell` later must be classified here: carried, or dropped
// with the rest of the one-shot bootstrap half.
func TestShellReentryClassifiesEveryFlag(t *testing.T) {
	carried := map[string]bool{
		"publish": true, "oauth": true, "bridge-loopback": true,
		"profile": true, "share": true, "peer": true,
	}
	shellCmd.Flags().VisitAll(func(f *pflag.Flag) {
		if carried[f.Name] || reentryNonIdempotent[f.Name] {
			return
		}
		t.Errorf("shell flag --%s is unclassified for the re-entry form: carry it, "+
			"or add it to reentryNonIdempotent if it belongs to the bootstrap half", f.Name)
	})
}

// TestWorktreeReentryPinsTheResolvedAgent covers the other half of the same
// loss: `worktree open <branch>` without --agent falls back to the config
// default, so a session started as --agent codex could resume as claude. The
// agent pinned is the *resolved* one, not the flag as typed, so the form keeps
// naming this session's agent even if the config default changes underneath.
func TestWorktreeReentryPinsTheResolvedAgent(t *testing.T) {
	got := worktreeReentry("fix/thing", "codex")
	want := []string{"worktree", "open", "fix/thing", "--agent", "codex"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("worktreeReentry = %q, want %q", got, want)
	}

	if got := worktreeReentry("fix/thing", ""); !reflect.DeepEqual(got, []string{"worktree", "open", "fix/thing"}) {
		t.Errorf("worktreeReentry with no agent = %q, want the bare open form", got)
	}
}
