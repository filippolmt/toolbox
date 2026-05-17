package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestSubcommandsRejectPositionalArgs locks in the NoArgs contract on
// commands that take no positional arguments. Regression guard: dropping the
// validator would silently accept typos like `toolbox shell foo` instead of
// surfacing a usage error. Also verifies the failure is wrapped in
// *usageError so Execute() maps it to exit code 2.
//
// shell and stop take an optional [name] positional, so for them the
// rejection cases mix a plausible-looking first arg with an extra trailing
// arg — this catches a future regression where MaximumNArgs is bumped to 2
// (or the validator is dropped entirely), which a `{"unexpected", "extra"}`
// pair could not distinguish from "any two unknown strings are refused".
func TestSubcommandsRejectPositionalArgs(t *testing.T) {
	cases := []struct {
		name string
		cmd  *cobra.Command
		args []string
	}{
		{"shell", shellCmd, []string{"infra", "extra"}},
		{"build", buildCmd, nil},
		{"stop", stopCmd, []string{"infra", "extra"}},
		{"version", versionCmd, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.cmd.Args == nil {
				t.Fatalf("%s: Args validator must be set", tc.name)
			}
			args := tc.args
			if args == nil {
				args = []string{"unexpected"}
			}
			err := tc.cmd.Args(tc.cmd, args)
			if err == nil {
				t.Fatalf("%s: Args should reject invalid positional args %v", tc.name, args)
			}
			var uerr *usageError
			if !errors.As(err, &uerr) {
				t.Errorf("%s: Args error should be wrapped in *usageError, got %T: %v", tc.name, err, err)
			}
			okArgs := []string{}
			if tc.name == "shell" || tc.name == "stop" {
				okArgs = []string{"name"}
			}
			if err := tc.cmd.Args(tc.cmd, okArgs); err != nil {
				t.Errorf("%s: Args should accept %v, got %v", tc.name, okArgs, err)
			}
		})
	}
}

// TestCompletionRejectsUnknownShell: the `completion` command accepts exactly
// one of bash/zsh/fish. Anything else (or zero / multiple args) is a usage
// error — wrapped in *usageError for exit-code mapping.
func TestCompletionRejectsUnknownShell(t *testing.T) {
	bad := [][]string{{"powershell"}, {}, {"bash", "zsh"}}
	for _, args := range bad {
		err := completionCmd.Args(completionCmd, args)
		if err == nil {
			t.Errorf("completion should reject %v", args)
			continue
		}
		var uerr *usageError
		if !errors.As(err, &uerr) {
			t.Errorf("completion Args(%v) should be wrapped in *usageError, got %T", args, err)
		}
	}
	for _, sh := range []string{"bash", "zsh", "fish"} {
		if err := completionCmd.Args(completionCmd, []string{sh}); err != nil {
			t.Errorf("completion should accept %q, got %v", sh, err)
		}
	}
}

// TestUsageArgsWraps verifies the helper: a validator that fails becomes
// *usageError; a validator that passes returns nil; a nil validator is a
// no-op that accepts any input.
func TestUsageArgsWraps(t *testing.T) {
	stub := &cobra.Command{Use: "stub"}
	wrapped := usageArgs(cobra.NoArgs)
	err := wrapped(stub, []string{"extra"})
	if err == nil {
		t.Fatal("NoArgs wrapped should fail on extra arg")
	}
	var uerr *usageError
	if !errors.As(err, &uerr) {
		t.Errorf("wrapped error should be *usageError, got %T", err)
	}
	if wrapped(stub, nil) != nil {
		t.Error("NoArgs wrapped should accept zero args")
	}
	if usageArgs(nil)(stub, []string{"whatever"}) != nil {
		t.Error("usageArgs(nil) must be a no-op accepting any args")
	}
}

// TestFlagErrorFuncWrapsInUsageError verifies the rootCmd.FlagErrorFunc
// wiring directly — no call to Execute() so the global cobra.OnInitialize
// chain is not invoked and no test side-effects leak into other tests.
func TestFlagErrorFuncWrapsInUsageError(t *testing.T) {
	fe := rootCmd.FlagErrorFunc()
	if fe == nil {
		t.Fatal("rootCmd.FlagErrorFunc must be set in init()")
	}
	src := errors.New("unknown flag: --foo")
	err := fe(rootCmd, src)
	var uerr *usageError
	if !errors.As(err, &uerr) {
		t.Fatalf("FlagErrorFunc should wrap in *usageError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "unknown flag") {
		t.Errorf("wrapped error should preserve the original message, got %q", err)
	}
	if !errors.Is(err, src) {
		t.Error("Unwrap chain should reach the original error")
	}
}
