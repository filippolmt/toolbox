package configedit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func findingWith(findings []Finding, substr string) *Finding {
	for i := range findings {
		if strings.Contains(findings[i].Message, substr) {
			return &findings[i]
		}
	}
	return nil
}

func TestDoctorCleanConfig(t *testing.T) {
	dir := t.TempDir()
	host, cwd := writeLayeredFixture(t,
		"", "mounts_root: "+dir+"\n")

	if findings := Doctor(host, cwd, ""); len(findings) != 0 {
		t.Errorf("clean config must yield no findings, got %v", findings)
	}
}

func TestDoctorUnknownKeySuggestion(t *testing.T) {
	host, cwd := writeLayeredFixture(t, "", "mont_root: /tmp/x\n")

	findings := Doctor(host, cwd, "")
	f := findingWith(findings, `did you mean "mounts_root"?`)
	if f == nil {
		t.Fatalf("expected unknown-key suggestion, got %v", findings)
	}
	if f.Severity != SeverityWarning {
		t.Errorf("unknown key severity = %v, want warning", f.Severity)
	}
}

func TestDoctorLegacyToolsBlock(t *testing.T) {
	host, cwd := writeLayeredFixture(t, "", "tools:\n  gh: true\n")

	findings := Doctor(host, cwd, "")
	f := findingWith(findings, "tools-removal")
	if f == nil {
		t.Fatalf("expected legacy tools warning referencing tools-removal, got %v", findings)
	}
	if f.Severity != SeverityWarning {
		t.Errorf("legacy tools severity = %v, want warning", f.Severity)
	}
}

func TestDoctorEmptyShellPathIsError(t *testing.T) {
	host, cwd := writeLayeredFixture(t, "", "shells:\n  infra:\n    path: \"\"\n")

	findings := Doctor(host, cwd, "")
	f := findingWith(findings, "shells.infra.path is empty")
	if f == nil {
		t.Fatalf("expected empty-path error, got %v", findings)
	}
	if f.Severity != SeverityError {
		t.Errorf("empty path severity = %v, want error", f.Severity)
	}
	if !HasErrors(findings) {
		t.Error("HasErrors must be true with an error finding")
	}
}

func TestDoctorMissingShellDirIsWarning(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-there")
	host, cwd := writeLayeredFixture(t, "", "shells:\n  infra:\n    path: "+missing+"\n")

	findings := Doctor(host, cwd, "")
	f := findingWith(findings, "does not exist")
	if f == nil {
		t.Fatalf("expected missing-dir warning, got %v", findings)
	}
	if f.Severity != SeverityWarning {
		t.Errorf("missing dir severity = %v, want warning", f.Severity)
	}
	if HasErrors(findings) {
		t.Error("warnings alone must not flip HasErrors")
	}
}

func TestDoctorSurfacesMergeError(t *testing.T) {
	host, cwd := writeLayeredFixture(t, "", "shell: fish\n")

	findings := Doctor(host, cwd, "")
	f := findingWith(findings, "fish")
	if f == nil {
		t.Fatalf("expected Plan/Merge error finding, got %v", findings)
	}
	if f.Severity != SeverityError {
		t.Errorf("merge error severity = %v, want error", f.Severity)
	}
}

func TestDoctorMountMergeError(t *testing.T) {
	// Patch form (no target) referencing a name absent from the defaults.
	host, cwd := writeLayeredFixture(t, "", "mounts:\n  - name: no-such-default\n    readonly: true\n")

	findings := Doctor(host, cwd, "")
	f := findingWith(findings, "unknown mount name")
	if f == nil {
		t.Fatalf("expected mount-merge error, got %v", findings)
	}
	if f.Severity != SeverityError {
		t.Errorf("mount merge severity = %v, want error", f.Severity)
	}
}

func TestDoctorDuplicateTargets(t *testing.T) {
	host, cwd := writeLayeredFixture(t, "",
		"mounts:\n"+
			"  - name: a\n    source: ~/a\n    target: /dup\n"+
			"  - name: b\n    source: ~/b\n    target: /dup\n")

	findings := Doctor(host, cwd, "")
	f := findingWith(findings, "duplicate target /dup")
	if f == nil {
		t.Fatalf("expected duplicate-target warning, got %v", findings)
	}
	if f.Severity != SeverityWarning {
		t.Errorf("duplicate target severity = %v, want warning", f.Severity)
	}
}

func TestDoctorUnreadableExplicitConfig(t *testing.T) {
	host, cwd := writeLayeredFixture(t, "", "")

	findings := Doctor(host, cwd, filepath.Join(t.TempDir(), "missing.yaml"))
	if !HasErrors(findings) {
		t.Fatalf("unreadable --config must be an error, got %v", findings)
	}
}

func TestDoctorChecksExplicitLayerKeys(t *testing.T) {
	host, cwd := writeLayeredFixture(t, "", "")
	explicit := filepath.Join(t.TempDir(), "custom.yaml")
	if err := os.WriteFile(explicit, []byte("shel: zsh\n"), 0o600); err != nil {
		t.Fatalf("write explicit: %v", err)
	}

	findings := Doctor(host, cwd, explicit)
	if f := findingWith(findings, `did you mean "shell"?`); f == nil {
		t.Fatalf("expected suggestion for explicit layer key, got %v", findings)
	}
}
