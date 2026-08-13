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
// That is the intersection of two views, because each one alone is wrong in a
// direction that makes the CLI unusable:
//
//   - The candidate on its own catches a fault a higher layer would mask — the
//     reason to validate the written file at all — but flags every value it
//     legitimately inherits. `shells set <n> --env … --where local` writes an
//     env overlay for a shell whose path lives in the global file, and a
//     project-only view of that file sees shells.<n>.path as empty.
//   - The resolved configuration (candidate substituted into its own layer)
//     knows what is inherited, but carries every *other* layer's faults. A
//     doctor error in a project file would block `config set --where global` —
//     a file the user cannot fix from there, and one plain `toolbox shell` runs
//     on fine.
//
// A finding present in both is the candidate's own, and it is real. Findings
// only in the first are inherited values; findings only in the second belong to
// a file this write does not touch. The common case (candidate clean on its
// own) short-circuits before the second view is computed.
//
// Layer placement: global when target is ~/.toolbox.yaml, project otherwise (a
// walked-up project file, or the ./.toolbox.yaml a writer is about to create —
// Resolve produces no other target, and when GlobalConfigPath fails there is no
// resolvable home, so --where global could not have produced a target either).
// The explicit `--config` layer is deliberately excluded: writers only ever
// target the global or project file, and those must stay valid for an ordinary
// `toolbox shell`, not for one overridden invocation.
//
// Only error-severity findings matter — they are what gates the write — and
// every lintLayerKeys finding is a warning, so the unknown-key lint stays where
// it can be acted on: `toolbox config doctor`.
func doctorCandidate(cwd, target string, candidate []byte) []Finding {
	alone := lintLayers(nil, candidate)
	if !HasErrors(alone) {
		return nil
	}
	global, project, _, _, err := config.LoadLayers(cwd, "")
	if err != nil {
		return []Finding{{SeverityError, err.Error()}}
	}
	globalPath, pathErr := configio.GlobalConfigPath()
	if pathErr == nil && filepath.Clean(target) == filepath.Clean(globalPath) {
		global = candidate
	} else {
		project = candidate
	}
	return intersectFindings(alone, lintLayers(global, project))
}

// lintLayers resolves two config layers and returns what the doctor's
// Config-level lints make of the result. A merge failure is itself the finding:
// nothing downstream is resolvable.
func lintLayers(global, project []byte) []Finding {
	cfg, err := config.Merge(global, project, nil)
	if err != nil {
		return []Finding{{SeverityError, err.Error()}}
	}
	return append(lintShellPaths(cfg), lintMounts(cfg)...)
}

// intersectFindings returns the findings of a that also appear in b, compared by
// message — the doctor's findings are plain strings naming what is wrong, so an
// identical message from both views is the same fault seen twice.
func intersectFindings(a, b []Finding) []Finding {
	inB := make(map[string]bool, len(b))
	for _, f := range b {
		inB[f.Message] = true
	}
	var out []Finding
	for _, f := range a {
		if inB[f.Message] {
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
