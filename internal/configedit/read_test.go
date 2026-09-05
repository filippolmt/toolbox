package configedit

import (
	"os"
	"path/filepath"
	"testing"
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
