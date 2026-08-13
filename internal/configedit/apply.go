package configedit

import (
	"bytes"
	"fmt"
	"path/filepath"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/configio"
)

// ApplyChecked is the one write path in this package: it renders the candidate
// document in memory, validates it in the config layer the target file
// occupies, and writes only once the validation passes. Every writer — the
// typed helpers in write.go, EnableSDD, and the config ui's Pending Mutation —
// goes through it, so no surface can put a configuration the doctor rejects on
// disk. The guarantee is structural rather than conventional: the only other
// exported write that touches a *config file* is EnsureFileWithHeader, whose
// comment-only output cannot introduce a finding. (The exported SDD writers in
// sdd.go write .gitignore, which the doctor has no opinion about.)
//
// Render-then-validate, not write-then-doctor-then-rollback: the candidate
// never reaches disk before it is known good, so a concurrent `toolbox shell`
// can never observe a transient invalid file (writer commands run in scripts
// and CI), and there is no rollback that can itself fail.
//
// changed reports whether the file was modified, with the same idempotence as
// a plain write: rendering that matches the file byte-for-byte is a no-op, and
// short-circuits before validation because a no-op cannot introduce a finding.
// A non-nil error always comes with changed=false — nothing was written, so
// callers must not report a write.
func ApplyChecked(target, cwd string, mutate Mutator) (changed bool, err error) {
	src, existed, err := configio.ReadMaybe(target)
	if err != nil {
		return false, err
	}
	candidate, err := Render(target, src, existed, mutate)
	if err != nil {
		return false, err
	}
	if bytes.Equal(candidate, src) {
		return false, nil
	}
	if findings := doctorCandidate(cwd, target, candidate); HasErrors(findings) {
		// The finding names the key, not the file it lives in — and the file is
		// what the user has to open to fix it, which is rarely the one they
		// thought they were editing.
		return false, fmt.Errorf("%s: %w", target, firstError(findings))
	}
	if err := configio.AtomicWriteFile(target, candidate, 0o600); err != nil {
		return false, err
	}
	return true, nil
}

// doctorCandidate returns the findings the candidate bytes for target are
// answerable for, touching no file itself. A write is gated on these, so the
// question it answers is deliberately narrow: not "is the resulting
// configuration flawless" but "does *this file* introduce a fault".
//
// Two moves make a finding attributable to one file:
//
//   - The candidate is judged as the top of the stack, over whichever layer it
//     is not. On top, because a value this file gets wrong must be reported
//     even when a higher-precedence layer overrides it today — that masking is
//     what lets the fault survive unnoticed until the overriding layer goes
//     away, and catching it is the whole reason to validate the written file
//     rather than the merged result. Over the other layer, because a file may
//     legitimately declare only part of an entity: `shells set <n> --env …
//     --where local` writes an env overlay for a shell whose path lives in the
//     global file, and that file read alone shows shells.<n>.path as empty.
//   - Whatever the other layer already says on its own is subtracted. Those
//     faults belong to a file this write does not touch and the user cannot fix
//     them from here — a doctor error in a project file must not make `config
//     set --where global` impossible, especially as plain `toolbox shell` runs
//     on that configuration fine.
//
// What is left is a fault the candidate carries. A pre-existing fault in the
// file being written still counts: the write need not have introduced it, only
// be putting it back.
//
// The target's own file is dropped from the stack — the candidate replaces it,
// so leaving its current bytes in would judge the edit against the content it
// overwrites. Which one to drop: the global layer when target is
// ~/.toolbox.yaml, the project layer otherwise (a walked-up project file, or
// the ./.toolbox.yaml a writer is about to create — Resolve produces no other
// target, and when GlobalConfigPath fails there is no resolvable home, so
// --where global could not have produced a target either). The explicit
// `--config` layer is excluded on purpose: writers only ever target the global
// or project file, and those must stay valid for an ordinary `toolbox shell`,
// not for one overridden invocation.
//
// Only error-severity findings matter — they are what gates the write — and
// every lintLayerKeys finding is a warning, so the unknown-key lint stays where
// it can be acted on: `toolbox config doctor`.
func doctorCandidate(cwd, target string, candidate []byte) []Finding {
	global, project, _, _, err := config.LoadLayers(cwd, "")
	if err != nil {
		return []Finding{{SeverityError, err.Error()}}
	}
	other := global
	if globalPath, pathErr := configio.GlobalConfigPath(); pathErr == nil &&
		filepath.Clean(target) == filepath.Clean(globalPath) {
		other = project
	}
	return subtractFindings(lintStack(other, candidate), lintStack(other, nil))
}

// lintStack resolves two config layers — lower, then higher — and returns what
// the doctor's Config-level lints make of the result. A merge failure is itself
// the finding: nothing downstream is resolvable.
//
// The two arguments are precedence positions, not provenance: Merge reads them
// in order and nothing downstream of it asks which file a value came from. That
// is what lets a caller ask "how does this document read with nothing
// overriding it" by putting it second, whichever layer it really belongs to.
// The explicit slot cannot serve here — Merge documents it as short-circuiting
// the other two, so it would discard the stack instead of sitting above it.
func lintStack(lower, higher []byte) []Finding {
	cfg, err := config.Merge(lower, higher, nil)
	if err != nil {
		return []Finding{{SeverityError, err.Error()}}
	}
	return append(lintShellPaths(cfg), lintMounts(cfg)...)
}

// subtractFindings returns the findings of a that do not appear in b, compared
// by message — the doctor's findings are plain strings naming what is wrong, so
// the same message from both stacks is the same fault, and it is not the
// candidate's.
func subtractFindings(a, b []Finding) []Finding {
	inB := make(map[string]bool, len(b))
	for _, f := range b {
		inB[f.Message] = true
	}
	var out []Finding
	for _, f := range a {
		if !inB[f.Message] {
			out = append(out, f)
		}
	}
	return out
}

// firstError returns the first error-severity finding as an error.
func firstError(findings []Finding) error {
	for _, f := range findings {
		if f.Severity == SeverityError {
			return fmt.Errorf("%s", f.Message)
		}
	}
	return fmt.Errorf("configuration invalid")
}
