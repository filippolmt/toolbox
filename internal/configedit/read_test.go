package configedit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/filippolmt/toolbox/internal/config"
)

// The two single-file readers answer "what does this file itself declare" —
// the candidate set the CLI's suggestions and existence checks are drawn from.
// They are tested on fixtures rather than on a file a write produced: a reader
// that only ever sees writer output cannot testify about the hand-written file
// it will meet in the wild.

func writeFixture(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".toolbox.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestUserShells(t *testing.T) {
	missing := filepath.Join(t.TempDir(), ".toolbox.yaml")
	shells, err := UserShells(missing)
	if err != nil {
		t.Fatalf("UserShells on missing file: %v", err)
	}
	if len(shells) != 0 {
		t.Errorf("missing file must yield no shells, got %v", shells)
	}

	shells, err = UserShells(writeFixture(t, "shells:\n  infra:\n    path: /tmp/infra\n"))
	if err != nil {
		t.Fatalf("UserShells: %v", err)
	}
	if len(shells) != 1 || shells["infra"] != "/tmp/infra" {
		t.Errorf("UserShells = %v, want map[infra:/tmp/infra]", shells)
	}
}

func TestUserMountNames(t *testing.T) {
	missing := filepath.Join(t.TempDir(), ".toolbox.yaml")
	names, err := UserMountNames(missing)
	if err != nil {
		t.Fatalf("UserMountNames on missing file: %v", err)
	}
	if len(names) != 0 {
		t.Errorf("missing file must yield no names, got %v", names)
	}

	// Both shapes the mounts: list carries — a full entry and a bare disable
	// patch — are named entries and both must be offered as candidates.
	src := "mounts:\n  - name: scratch\n    source: ~/s\n    target: /s\n  - name: gh\n    disabled: true\n"
	names, err = UserMountNames(writeFixture(t, src))
	if err != nil {
		t.Fatalf("UserMountNames: %v", err)
	}
	want := []string{"scratch", "gh"}
	if len(names) != len(want) || names[0] != want[0] || names[1] != want[1] {
		t.Errorf("UserMountNames = %v, want %v", names, want)
	}
}

func TestUserShellsReportsAnUnparseableFile(t *testing.T) {
	path := writeFixture(t, "shells: [unclosed\n")
	if _, err := UserShells(path); err == nil {
		t.Fatal("an unparseable file must be reported, not read as empty")
	}
}

// FileValues is the third single-file reader, and the one that answers a
// provenance question: which keys does *this layer's file* set. It is
// structural on purpose — a file that spells out a default still sets it — so
// the fixtures below assert on presence, not on a diff against the defaults.

func TestFileValuesReportsOnlyWhatTheFileSets(t *testing.T) {
	got, err := FileValues(writeFixture(t, "pull: never\n"))
	if err != nil {
		t.Fatalf("FileValues: %v", err)
	}
	if v, ok := got["pull"]; !ok || v.Scalar != "never" {
		t.Errorf("file must set pull=never, got %+v (present: %v)", v, ok)
	}
	if _, ok := got["agent"]; ok {
		t.Errorf("a key the file does not mention must be absent, got %+v", got["agent"])
	}
}

// A value equal to the built-in default is still a value this file writes: the
// per-scope line reports what the layer says, not whether it differs from the
// defaults, so the reader must not answer by diffing against them.
func TestFileValuesReportsAKeySetToItsDefault(t *testing.T) {
	got, err := FileValues(writeFixture(t, "pull: auto\n"))
	if err != nil {
		t.Fatalf("FileValues: %v", err)
	}
	if _, ok := got["pull"]; !ok {
		t.Error("a key written at its default value still counts as set by the file")
	}
}

func TestFileValuesMissingFileSetsNothing(t *testing.T) {
	got, err := FileValues(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("FileValues on missing file: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("a missing file must set nothing, got %+v", got)
	}
}

// A file that holds no mapping — empty, or only comments — reads as setting
// nothing rather than failing the layer it describes.
func TestFileValuesCommentOnlyFileSetsNothing(t *testing.T) {
	got, err := FileValues(writeFixture(t, "# nothing here yet\n"))
	if err != nil {
		t.Fatalf("FileValues: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("a comment-only file must set nothing, got %+v", got)
	}
}

// Collections report how many entries the file holds — mapping pairs and
// sequence items alike — and nested containers are reachable by dotted path, so
// a caller can ask about a field inside one without walking nodes itself.
func TestFileValuesCountsCollectionEntries(t *testing.T) {
	path := writeFixture(t, `env:
  REGION: eu
  TIER: dev
inherit_host_auth: [claude, gh, git]
worktree:
  seed: [.env.local]
`)
	got, err := FileValues(path)
	if err != nil {
		t.Fatalf("FileValues: %v", err)
	}
	for _, tc := range []struct {
		key  string
		want int
	}{
		{"env", 2},
		{"inherit_host_auth", 3},
		{"worktree", 1},
		{"worktree.seed", 1},
	} {
		if got[tc.key].Entries != tc.want {
			t.Errorf("%s holds %d entries, want %d", tc.key, got[tc.key].Entries, tc.want)
		}
	}
	if got["env"].Scalar != "" {
		t.Errorf("a collection has no scalar value, got %q", got["env"].Scalar)
	}
}

// A deprecated alias folds into its live key here the same way config.Merge
// folds it on load, so a file that only sets browser_bridge counts as setting
// bridge. This is the fold's single implementation for the "does this file set
// K" question — configui used to carry its own copy.
func TestFileValuesFoldsADeprecatedAliasIntoItsLiveKey(t *testing.T) {
	aliases := config.DeprecatedAliases()
	if len(aliases) == 0 {
		t.Skip("no deprecated aliases to fold")
	}
	for alias, live := range aliases {
		got, err := FileValues(writeFixture(t, alias+": false\n"))
		if err != nil {
			t.Fatalf("FileValues: %v", err)
		}
		if _, ok := got[live]; !ok {
			t.Errorf("%q in a file must count as setting %q, got %+v", alias, live, got)
		}
	}
}

// The live spelling wins when a file carries both, matching the load path where
// the alias is a backstop and never an override.
func TestFileValuesPrefersTheLiveKeyOverItsAlias(t *testing.T) {
	for alias, live := range config.DeprecatedAliases() {
		got, err := FileValues(writeFixture(t, alias+": false\n"+live+": true\n"))
		if err != nil {
			t.Fatalf("FileValues: %v", err)
		}
		if got[live].Scalar != "true" {
			t.Errorf("%q must keep its own value over %q's, got %q", live, alias, got[live].Scalar)
		}
	}
}

func TestFileValuesReportsAnUnparseableFile(t *testing.T) {
	if _, err := FileValues(writeFixture(t, "pull: [unclosed\n")); err == nil {
		t.Fatal("an unparseable file must be reported, not read as setting nothing")
	}
}
