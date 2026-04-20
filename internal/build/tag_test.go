package build

import (
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/config"
)

func TestResolveImageDefaultsToRegistry(t *testing.T) {
	cfg := &config.Config{Tools: config.DefaultTools()}
	ref, isLocal := ResolveImage(cfg, "dev")

	if isLocal {
		t.Error("default tools config should resolve to the registry image (isLocal=false)")
	}
	if ref != DefaultRegistryImage {
		t.Errorf("ref = %q, want %q", ref, DefaultRegistryImage)
	}
}

func TestResolveImageReturnsLocalHashForOptOut(t *testing.T) {
	cfg := &config.Config{Tools: config.DefaultTools()}
	cfg.Tools["gcloud"] = false

	ref, isLocal := ResolveImage(cfg, "dev")
	if !isLocal {
		t.Error("opted-out tools config should resolve to a local image (isLocal=true)")
	}
	if !strings.HasPrefix(ref, "toolbox:local-") {
		t.Errorf("ref = %q, want prefix 'toolbox:local-'", ref)
	}
	if len(ref) != len("toolbox:local-")+12 {
		t.Errorf("expected 12-char hash suffix, got ref = %q", ref)
	}
}

func TestResolveImageStableAcrossCalls(t *testing.T) {
	cfg := &config.Config{Tools: config.DefaultTools()}
	cfg.Tools["gcloud"] = false

	ref1, _ := ResolveImage(cfg, "dev")
	ref2, _ := ResolveImage(cfg, "dev")
	if ref1 != ref2 {
		t.Errorf("ResolveImage not stable: %q vs %q", ref1, ref2)
	}
}

func TestResolveImageChangesWithVersion(t *testing.T) {
	cfg := &config.Config{Tools: config.DefaultTools()}
	cfg.Tools["gcloud"] = false

	refA, _ := ResolveImage(cfg, "v1.0.0")
	refB, _ := ResolveImage(cfg, "v1.0.1")
	if refA == refB {
		t.Error("ref should change when CLI version changes (Dockerfile may have shifted)")
	}
}

func TestResolveImageChangesWithToolsFlip(t *testing.T) {
	cfgGcloud := &config.Config{Tools: config.DefaultTools()}
	cfgGcloud.Tools["gcloud"] = false

	cfgUv := &config.Config{Tools: config.DefaultTools()}
	cfgUv.Tools["uv"] = false

	refA, _ := ResolveImage(cfgGcloud, "dev")
	refB, _ := ResolveImage(cfgUv, "dev")
	if refA == refB {
		t.Error("disabling different tools must produce different refs")
	}
}

func TestBuildArgsFromToolsOnlyEmitsDisabled(t *testing.T) {
	tools := config.DefaultTools()
	tools["gcloud"] = false
	tools["nosuchtool"] = false // unknown key, should be skipped

	args := BuildArgsFromTools(tools)

	// Only gcloud should produce an arg — every other tool is still enabled
	// (default) and the unknown key has no Dockerfile ARG mapping.
	if len(args) != 1 {
		t.Errorf("expected 1 build arg, got %d: %v", len(args), args)
	}
	v, ok := args["INSTALL_GCLOUD"]
	if !ok {
		t.Fatal("expected INSTALL_GCLOUD in build args")
	}
	if v == nil || *v != "false" {
		t.Errorf("INSTALL_GCLOUD = %v, want pointer to \"false\"", v)
	}
}

func TestBuildArgsFromToolsEmptyWhenAllDefault(t *testing.T) {
	args := BuildArgsFromTools(config.DefaultTools())
	if len(args) != 0 {
		t.Errorf("default tools should produce no build args, got %v", args)
	}
}
