package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStreamBuildOutput(t *testing.T) {
	input := `{"stream":"Step 1/5 : FROM debian:12-slim\n"}
{"stream":"Step 2/5 : RUN apt-get update\n"}
{"stream":"Successfully built abc123\n"}`

	reader := strings.NewReader(input)
	err := streamBuildOutput(reader)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestStreamBuildOutputError(t *testing.T) {
	input := `{"stream":"Step 1/5 : FROM debian:12-slim\n"}
{"error":"some build error occurred"}`

	reader := strings.NewReader(input)
	err := streamBuildOutput(reader)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "some build error occurred") {
		t.Fatalf("expected error message to contain 'some build error occurred', got: %v", err)
	}
}

func TestReadDockerignore(t *testing.T) {
	dir := t.TempDir()

	content := `.planning/
.git/
*.md`
	err := os.WriteFile(filepath.Join(dir, ".dockerignore"), []byte(content), 0644)
	if err != nil {
		t.Fatalf("failed to write .dockerignore: %v", err)
	}

	patterns := readDockerignore(dir)
	if len(patterns) != 3 {
		t.Fatalf("expected 3 patterns, got %d: %v", len(patterns), patterns)
	}

	expected := []string{".planning/", ".git/", "*.md"}
	for i, p := range patterns {
		if p != expected[i] {
			t.Errorf("pattern[%d]: expected %q, got %q", i, expected[i], p)
		}
	}
}

func TestReadDockerignoreMissing(t *testing.T) {
	dir := t.TempDir()

	patterns := readDockerignore(dir)
	if len(patterns) != 0 {
		t.Fatalf("expected 0 patterns for missing .dockerignore, got %d: %v", len(patterns), patterns)
	}
}

func TestShouldIgnore(t *testing.T) {
	patterns := []string{".planning/", ".git/", "*.md", "CLAUDE.md"}

	tests := []struct {
		path   string
		isDir  bool
		expect bool
	}{
		{".planning", true, true},
		{".planning/STATE.md", false, true},
		{".git", true, true},
		{"README.md", false, true},
		{"docker/Dockerfile", false, false},
		{"cmd/root.go", false, false},
		{"CLAUDE.md", false, true},
	}

	for _, tt := range tests {
		got := shouldIgnore(tt.path, tt.isDir, patterns)
		if got != tt.expect {
			t.Errorf("shouldIgnore(%q, %v) = %v, want %v", tt.path, tt.isDir, got, tt.expect)
		}
	}
}
