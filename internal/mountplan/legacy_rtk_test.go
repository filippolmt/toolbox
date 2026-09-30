package mountplan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/fsx"
)

func TestRTKMountsAreAbsentFromDefaults(t *testing.T) {
	for _, mount := range Defaults() {
		if mount.Name == "rtk" || mount.Name == "rtk-data" {
			t.Errorf("retired mount still present: %s", mount.Name)
		}
	}
}

func TestLegacyRTKMountReferencesAreIgnoredWithAWarning(t *testing.T) {
	home := t.TempDir()
	profile, err := NewProfile("work", []string{"rtk"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := Plan(PlanInput{
		Host:      fsx.Host{Home: home},
		Cfg:       &config.Config{Mounts: []config.Mount{{Name: "rtk", Disabled: true}, {Name: "rtk-data", Disabled: true}}},
		Profile:   profile,
		Workspace: home,
	})
	if err != nil {
		t.Fatalf("Plan rejected retired RTK references: %v", err)
	}
	if got := strings.Join(result.Warnings, "\n"); !strings.Contains(got, "rtk") {
		t.Errorf("Warnings = %q, want retired RTK notice", got)
	}
}

func TestLegacyRTKShareReferenceWarnsWithoutLegacyMounts(t *testing.T) {
	home := t.TempDir()
	profile, err := NewProfile("work", []string{legacyRTKMountName})
	if err != nil {
		t.Fatal(err)
	}
	result, err := Plan(PlanInput{Host: fsx.Host{Home: home}, Cfg: &config.Config{}, Profile: profile, Workspace: home})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(result.Warnings, "\n"); !strings.Contains(got, "share configuration") {
		t.Errorf("Warnings = %q, want retired share notice", got)
	}
}

func TestRemoveLegacyRTKStateRejectsAnUnsetHome(t *testing.T) {
	if err := RemoveLegacyRTKState(fsx.Host{}, &config.Config{}, nil); err == nil {
		t.Fatal("RemoveLegacyRTKState accepted an unset host home")
	}
}

func TestRemoveLegacyRTKStateRejectsAnUnsafeRoot(t *testing.T) {
	home := t.TempDir()
	if err := RemoveLegacyRTKState(fsx.Host{Home: home}, &config.Config{MountsRoot: "."}, nil); err == nil {
		t.Fatal("RemoveLegacyRTKState accepted a relative root")
	}
}

func TestRemoveLegacyRTKStateHonorsLegacyDataOnlyShare(t *testing.T) {
	home := t.TempDir()
	paths := map[string]bool{
		".toolbox/profiles/work/rtk/config/config.toml": false,
		".toolbox/profiles/work/rtk/data/history.db":    true,
		".toolbox/rtk/config/config.toml":               true,
		".toolbox/rtk/data/history.db":                  false,
	}
	for name := range paths {
		path := filepath.Join(home, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	profile, err := NewProfile("work", []string{"rtk-data"})
	if err != nil {
		t.Fatal(err)
	}
	if err := RemoveLegacyRTKState(fsx.Host{Home: home}, &config.Config{}, profile); err != nil {
		t.Fatalf("RemoveLegacyRTKState: %v", err)
	}
	for name, wantExists := range paths {
		_, err := os.Stat(filepath.Join(home, name))
		if gotExists := err == nil; gotExists != wantExists {
			t.Errorf("%s exists = %v, want %v", name, gotExists, wantExists)
		}
	}
}

func TestRemoveLegacyRTKStateHonorsLegacyWholeShare(t *testing.T) {
	home := t.TempDir()
	shared := filepath.Join(home, ".toolbox", "rtk", "data")
	profileState := filepath.Join(home, ".toolbox", "profiles", "work", "rtk", "data")
	for _, dir := range []string{shared, profileState} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	profile, err := NewProfile("work", []string{legacyRTKMountName})
	if err != nil {
		t.Fatal(err)
	}
	if err := RemoveLegacyRTKState(fsx.Host{Home: home}, &config.Config{}, profile); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(shared); !os.IsNotExist(err) {
		t.Errorf("shared RTK state still exists: %v", err)
	}
	if _, err := os.Stat(profileState); err != nil {
		t.Errorf("inactive profile state was touched: %v", err)
	}
}

func TestRemoveLegacyRTKStateRemovesOnlyTheActiveRoot(t *testing.T) {
	home := t.TempDir()
	active := filepath.Join(home, ".toolbox", "profiles", "work", "rtk")
	other := filepath.Join(home, ".toolbox", "profiles", "other", "rtk")
	for _, dir := range []string{active, other} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	profile, err := NewProfile("work", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := RemoveLegacyRTKState(fsx.Host{Home: home}, &config.Config{}, profile); err != nil {
		t.Fatalf("RemoveLegacyRTKState: %v", err)
	}
	if _, err := os.Stat(active); !os.IsNotExist(err) {
		t.Errorf("active RTK state still exists: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("unselected profile was touched: %v", err)
	}
}
