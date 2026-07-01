package config

import (
	"strings"
	"testing"
)

func TestValidateWorktreeSeedAcceptsRelativePaths(t *testing.T) {
	err := ValidateWorktreeSeed([]string{
		".env.local",
		"config/local.yaml",
		"nested/dir",
	})
	if err != nil {
		t.Fatalf("ValidateWorktreeSeed = %v, want nil", err)
	}
}

func TestValidateWorktreeSeedRejectsEmpty(t *testing.T) {
	err := ValidateWorktreeSeed([]string{""})
	if err == nil || !strings.Contains(err.Error(), "empty path") {
		t.Fatalf("ValidateWorktreeSeed = %v, want empty-path error", err)
	}
}

func TestValidateWorktreeSeedRejectsAbsolute(t *testing.T) {
	err := ValidateWorktreeSeed([]string{"/etc/passwd"})
	if err == nil || !strings.Contains(err.Error(), "relative") {
		t.Fatalf("ValidateWorktreeSeed = %v, want relative-path error", err)
	}
}

func TestValidateWorktreeSeedRejectsTraversal(t *testing.T) {
	for _, p := range []string{"..", "../secrets", "a/../../b"} {
		if err := ValidateWorktreeSeed([]string{p}); err == nil || !strings.Contains(err.Error(), "'..'") {
			t.Errorf("ValidateWorktreeSeed(%q) = %v, want '..' error", p, err)
		}
	}
}
