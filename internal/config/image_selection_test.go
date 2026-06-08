package config

import (
	"strings"
	"testing"
)

func TestValidatePull(t *testing.T) {
	for _, ok := range []string{"", "auto", "always", "never"} {
		if err := ValidatePull(ok); err != nil {
			t.Errorf("ValidatePull(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"Always", "yes", "on", "stale"} {
		if err := ValidatePull(bad); err == nil {
			t.Errorf("ValidatePull(%q) = nil, want error", bad)
		}
	}
}

func TestValidateImageRef(t *testing.T) {
	if err := ValidateImageRef(""); err != nil {
		t.Errorf("empty image must be allowed: %v", err)
	}
	if err := ValidateImageRef("ghcr.io/x/y:1"); err != nil {
		t.Errorf("plain ref must be allowed: %v", err)
	}
	if err := ValidateImageRef("ghcr.io/x y:1"); err == nil {
		t.Error("whitespace in image must be rejected")
	}
	if err := ValidateImageRef("https://ghcr.io/x:1"); err == nil {
		t.Error("URL-form image must be rejected")
	}
}

func TestValidateRegistryMirror(t *testing.T) {
	if err := ValidateRegistryMirror(""); err != nil {
		t.Errorf("empty mirror must be allowed: %v", err)
	}
	if err := ValidateRegistryMirror("harbor.corp.io/ghcr-proxy"); err != nil {
		t.Errorf("bare host/path must be allowed: %v", err)
	}
	if err := ValidateRegistryMirror("https://harbor.corp.io"); err == nil {
		t.Error("URL-form mirror must be rejected")
	}
	if err := ValidateRegistryMirror("harbor corp"); err == nil {
		t.Error("whitespace in mirror must be rejected")
	}
	// Leading-slash values splice into a hostless, malformed ref — must be
	// caught up front, not at docker-pull time.
	for _, bad := range []string{"/", "//", "/harbor.corp.io"} {
		if err := ValidateRegistryMirror(bad); err == nil {
			t.Errorf("leading-slash mirror %q must be rejected", bad)
		}
	}
	// Trailing slash is fine — build.ResolveImage trims it.
	if err := ValidateRegistryMirror("harbor.corp.io/proxy/"); err != nil {
		t.Errorf("trailing slash must be allowed: %v", err)
	}
}

// TestMergeImageSelectionFromFile asserts the three keys decode from YAML and
// that pull defaults to "auto" when absent.
func TestMergeImageSelectionFromFile(t *testing.T) {
	cfg, err := Merge(nil,
		[]byte("image: ghcr.io/x/y:1\nregistry_mirror: harbor.corp.io/ghcr-proxy\npull: never\n"),
		nil)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if cfg.Image != "ghcr.io/x/y:1" {
		t.Errorf("Image = %q", cfg.Image)
	}
	if cfg.RegistryMirror != "harbor.corp.io/ghcr-proxy" {
		t.Errorf("RegistryMirror = %q", cfg.RegistryMirror)
	}
	if cfg.Pull != "never" {
		t.Errorf("Pull = %q", cfg.Pull)
	}

	def, err := Merge(nil, nil, nil)
	if err != nil {
		t.Fatalf("Merge defaults: %v", err)
	}
	if def.Pull != "auto" {
		t.Errorf("default Pull = %q, want auto", def.Pull)
	}
}

// TestMergeImageSelectionProjectOverridesGlobal locks the file precedence
// (project wins over global) for the new keys.
func TestMergeImageSelectionProjectOverridesGlobal(t *testing.T) {
	cfg, err := Merge(
		[]byte("registry_mirror: global.example/proxy\n"),
		[]byte("registry_mirror: project.example/proxy\n"),
		nil)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if cfg.RegistryMirror != "project.example/proxy" {
		t.Errorf("RegistryMirror = %q, want project to win", cfg.RegistryMirror)
	}
}

// TestMergeImageSelectionEnvOverride asserts TOOLBOX_* env reaches the new
// keys (the SetDefault seeds register them with viper's AutomaticEnv).
func TestMergeImageSelectionEnvOverride(t *testing.T) {
	t.Setenv("TOOLBOX_REGISTRY_MIRROR", "env.example/proxy")
	t.Setenv("TOOLBOX_PULL", "always")
	cfg, err := Merge(nil, []byte("registry_mirror: file.example/proxy\n"), nil)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if cfg.RegistryMirror != "env.example/proxy" {
		t.Errorf("RegistryMirror = %q, want env to win over file", cfg.RegistryMirror)
	}
	if cfg.Pull != "always" {
		t.Errorf("Pull = %q, want always from env", cfg.Pull)
	}
}

// TestMergeRejectsBadPull surfaces validation errors through Merge.
func TestMergeRejectsBadPull(t *testing.T) {
	_, err := Merge(nil, []byte("pull: sometimes\n"), nil)
	if err == nil || !strings.Contains(err.Error(), "pull policy") {
		t.Fatalf("Merge = %v, want pull-policy error", err)
	}
}
