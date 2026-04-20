package build

import (
	"archive/tar"
	"io"
	"io/fs"
	"runtime"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/config"
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

// TestMergeBuildArgsInjectsTargetArch protects against a regression the
// real Docker build surfaced: the classic Docker builder (what the Go SDK
// uses) does not auto-populate TARGETARCH like BuildKit does, so every
// `${TARGETARCH}` reference in the Dockerfile would expand to empty.
func TestMergeBuildArgsInjectsTargetArch(t *testing.T) {
	out := mergeBuildArgs(nil)
	v, ok := out["TARGETARCH"]
	if !ok {
		t.Fatal("mergeBuildArgs must always emit TARGETARCH")
	}
	if v == nil || *v != runtime.GOARCH {
		t.Errorf("TARGETARCH = %v, want pointer to %q", v, runtime.GOARCH)
	}
}

func TestMergeBuildArgsPreservesCaller(t *testing.T) {
	disabled := "false"
	out := mergeBuildArgs(map[string]*string{"INSTALL_GCLOUD": &disabled})
	v, ok := out["INSTALL_GCLOUD"]
	if !ok || v == nil || *v != "false" {
		t.Errorf("caller INSTALL_GCLOUD lost during merge: %v", v)
	}
	if _, ok := out["TARGETARCH"]; !ok {
		t.Error("TARGETARCH should still be injected alongside caller args")
	}
}

func TestMergeBuildArgsCallerWinsOverride(t *testing.T) {
	// A caller explicitly setting TARGETARCH (e.g. cross-build scenarios)
	// must be honoured. The injected host arch is just a fallback.
	custom := "amd64"
	out := mergeBuildArgs(map[string]*string{"TARGETARCH": &custom})
	if v := out["TARGETARCH"]; v == nil || *v != "amd64" {
		t.Errorf("caller-provided TARGETARCH was overwritten: %v", v)
	}
}

// TestDockerfilePreCreatesMountParents enforces that every parent dir of a
// DefaultMounts target under /home/toolbox/ is pre-created by the Dockerfile.
// When Docker auto-creates a mount-target parent at runtime it owns it as
// root:root 0755 — locking out the non-root runtime user and breaking tools
// that write siblings (e.g. helm under ~/.config, starship under ~/.cache).
// The fix lives in Dockerfile Layer 21; this test catches the case where a
// new default mount introduces a parent the Dockerfile has forgotten to list.
func TestDockerfilePreCreatesMountParents(t *testing.T) {
	data, err := fs.ReadFile(Assets, AssetDir+"/Dockerfile")
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	content := string(data)

	for _, parent := range config.MountParentDirs(config.DefaultMounts()) {
		if !strings.Contains(content, parent) {
			t.Errorf("Dockerfile must pre-create %q (parent of a DefaultMounts target; otherwise Docker creates it as root:root 0755 at runtime)", parent)
		}
	}
}
