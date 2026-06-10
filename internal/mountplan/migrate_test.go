package mountplan

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateLegacyToolboxState_MovesLegacyDir(t *testing.T) {
	home := t.TempDir()
	legacy := filepath.Join(home, ".toolbox", "state", "pull-cache")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "marker"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := MigrateLegacyToolboxState(home); err != nil {
		t.Fatalf("MigrateLegacyToolboxState: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".toolbox", "toolbox", "state", "pull-cache", "marker")); err != nil {
		t.Errorf("migrated marker missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".toolbox", "state")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("legacy state dir still present after migration")
	}
}

func TestMigrateLegacyToolboxState_NoopWhenNewExists(t *testing.T) {
	home := t.TempDir()
	newDir := filepath.Join(home, ".toolbox", "toolbox", "state")
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(home, ".toolbox", "state")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := MigrateLegacyToolboxState(home); err != nil {
		t.Fatalf("MigrateLegacyToolboxState: %v", err)
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Errorf("stale legacy dir must be left alone when the new dir exists: %v", err)
	}
}

func TestMigrateLegacyToolboxState_NoopWithoutLegacy(t *testing.T) {
	home := t.TempDir()
	if err := MigrateLegacyToolboxState(home); err != nil {
		t.Fatalf("MigrateLegacyToolboxState: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".toolbox", "toolbox")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("migration must not create dirs when there is nothing to migrate")
	}
}
