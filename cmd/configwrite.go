package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/filippolmt/toolbox/internal/configedit"
)

// The edge every config-writing subcommand (`toolbox shells|mounts|config …`)
// shares: which file it targets, whether it writes or only shows, and what it
// prints afterwards. One place, because the alternative is the same handful of
// lines in every writer — and a writer added later that copies all of them but
// one.

// dryRunFlagUsage documents the --dry-run flag registered alongside --where.
const dryRunFlagUsage = "render the write and print it instead of touching any file"

// registerWriteFlags registers the pair of flags a config-writing subcommand
// carries: --where picks the file, --dry-run decides whether that file is
// written or only shown. Both from one call, so a writer added later cannot
// take the targeting and leave the preview behind —
// TestEveryConfigWriterCommandOffersDryRun fails the moment the two come apart.
func registerWriteFlags(cmd *cobra.Command, where *string, dryRun *bool) {
	cmd.Flags().StringVar(where, "where", "global", whereFlagUsage)
	cmd.Flags().BoolVar(dryRun, "dry-run", false, dryRunFlagUsage)
}

// applyOrPreview commits one Pending Mutation, or shows it. Every writer
// command ends here, which is what makes --dry-run a property of the surface
// rather than of each command: a mutation that can be rendered can be
// previewed, and every edit these commands perform is one.
//
// The preview is not a second rendering of the edit but the write's own
// (configedit.Preview), doctor verdict included — so a dry run of an edit the
// gate would reject reports that rejection rather than printing a candidate
// that could never land. What it prints is the whole candidate file, so it
// pipes: `toolbox config set --pull never --dry-run > candidate.yaml`.
func applyOrPreview(out io.Writer, target, cwd string, dryRun bool, mutate configedit.Mutator) error {
	if dryRun {
		candidate, _, err := configedit.Preview(target, cwd, mutate)
		if err != nil {
			return err
		}
		_, err = out.Write(candidate)
		return err
	}
	changed, existed, err := configedit.ApplyChecked(target, cwd, mutate)
	if err != nil {
		return err
	}
	reportWrite(out, target, existed, changed)
	return nil
}

// reportWrite prints the per-file result line shared by every writer
// command: created (file did not exist), updated, or unchanged.
//
// existedBefore is always the bit ApplyChecked returns, never a stat of the
// caller's own: the write answered that question when it read the file, and a
// second look could disagree with the one the write acted on.
func reportWrite(out io.Writer, path string, existedBefore, changed bool) {
	state := "unchanged"
	switch {
	case changed && !existedBefore:
		state = "created"
	case changed:
		state = "updated"
	}
	_, _ = fmt.Fprintf(out, "  %s: %s\n", path, state)
}

// reportSkippedSideEffect announces a host-side effect a dry run did not
// perform — the directory `shells add --create-dir` would create, the one
// `shells remove --purge-dir` would delete. It goes to stderr on purpose:
// stdout carries the candidate document and nothing else, so a dry run stays
// pipeable while still saying what the real run would touch beyond the file.
func reportSkippedSideEffect(err io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(err, "dry run: "+format+"\n", args...)
}

// resolveWriteTarget maps a --where flag value onto the config file path a
// writer should patch, and returns the cwd it was resolved from — the writers
// need that same cwd to validate the candidate document against the layers a
// load would see. Shared by the shells, mounts and sdd groups.
func resolveWriteTarget(where string) (target, cwd string, err error) {
	w, err := configedit.ParseWhere(where)
	if err != nil {
		return "", "", &usageError{err: err}
	}
	cwd, err = os.Getwd()
	if err != nil {
		return "", "", fmt.Errorf("resolve cwd: %w", err)
	}
	target, err = configedit.Resolve(w, cwd)
	return target, cwd, err
}
