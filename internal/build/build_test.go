package build

import (
	"archive/tar"
	"io"
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

// TestTarEmbeddedContext verifies the tar produced from the embedded assets
// contains the Dockerfile and companion scripts at the root (no nested dirs),
// so the Dockerfile's `COPY bashrc.sh …` resolves against the build context.
func TestTarEmbeddedContext(t *testing.T) {
	r, err := tarEmbeddedContext()
	if err != nil {
		t.Fatalf("tarEmbeddedContext: %v", err)
	}

	tr := tar.NewReader(r)
	got := map[string]bool{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar read: %v", err)
		}
		if strings.Contains(h.Name, "/") {
			t.Errorf("unexpected nested path in tar: %q — expected flat layout", h.Name)
		}
		got[h.Name] = true
	}

	for _, want := range []string{"Dockerfile", "bashrc.sh", "entrypoint.sh"} {
		if !got[want] {
			t.Errorf("tar missing entry %q (got %v)", want, got)
		}
	}
}
