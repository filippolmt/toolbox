package build

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"testing/fstest"

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

// TestComputeImageHashStableForSameAssets locks the determinism contract:
// identical asset filesystem + identical inputs must yield identical hashes
// across calls. Without it, ResolveImage would request a fresh build on
// every invocation even when nothing changed.
func TestComputeImageHashStableForSameAssets(t *testing.T) {
	assets := fstest.MapFS{
		"a/Dockerfile": &fstest.MapFile{Data: []byte("FROM scratch\n")},
		"a/entrypoint": &fstest.MapFile{Data: []byte("#!/bin/sh\nexec \"$@\"\n")},
	}
	tools := map[string]bool{"go": true, "rtk": false}

	h1, err := computeImageHashFromFS(assets, "a", "v1.0.0", tools)
	if err != nil {
		t.Fatalf("computeImageHashFromFS #1: %v", err)
	}
	h2, err := computeImageHashFromFS(assets, "a", "v1.0.0", tools)
	if err != nil {
		t.Fatalf("computeImageHashFromFS #2: %v", err)
	}
	if h1 != h2 {
		t.Errorf("hash not stable across calls with identical inputs: %q vs %q", h1, h2)
	}
	if len(h1) != 12 {
		t.Errorf("hash length = %d, want 12", len(h1))
	}
}

// TestComputeImageHashChangesOnAssetEdit guards the cache-invalidation
// contract: any byte-level change to an embedded asset (Dockerfile,
// entrypoint, …) must produce a fresh hash so users on a stale
// toolbox:local-<hash> image get rebuilt automatically.
func TestComputeImageHashChangesOnAssetEdit(t *testing.T) {
	tools := map[string]bool{"go": true}

	before := fstest.MapFS{
		"a/Dockerfile": &fstest.MapFile{Data: []byte("FROM debian:bookworm\n")},
	}
	after := fstest.MapFS{
		"a/Dockerfile": &fstest.MapFile{Data: []byte("FROM debian:trixie\n")},
	}

	hBefore, err := computeImageHashFromFS(before, "a", "v1.0.0", tools)
	if err != nil {
		t.Fatalf("hash before: %v", err)
	}
	hAfter, err := computeImageHashFromFS(after, "a", "v1.0.0", tools)
	if err != nil {
		t.Fatalf("hash after: %v", err)
	}
	if hBefore == hAfter {
		t.Errorf("editing an embedded asset must change the hash, got %q for both", hBefore)
	}
}

// TestComputeImageHashChangesOnAssetAdd guards the case where a new asset
// file is added to the embedded set (e.g. a new shell rc file): it must
// invalidate the hash even if no existing file changed.
func TestComputeImageHashChangesOnAssetAdd(t *testing.T) {
	tools := map[string]bool{"go": true}

	before := fstest.MapFS{
		"a/Dockerfile": &fstest.MapFile{Data: []byte("FROM scratch\n")},
	}
	after := fstest.MapFS{
		"a/Dockerfile": &fstest.MapFile{Data: []byte("FROM scratch\n")},
		"a/zshrc.sh":   &fstest.MapFile{Data: []byte("# new file\n")},
	}

	hBefore, _ := computeImageHashFromFS(before, "a", "v1.0.0", tools)
	hAfter, _ := computeImageHashFromFS(after, "a", "v1.0.0", tools)
	if hBefore == hAfter {
		t.Errorf("adding a new asset must change the hash, got %q for both", hBefore)
	}
}

// TestComputeImageHashPinnedDigest pins the canonical encoding (CAT-05, D-12).
// Any future change to internal/catalog.WriteCanonical's byte format, the
// catalog's Entry list, or the asset-section hash logic will trip this test.
// When the encoding intentionally changes, update the literal AFTER confirming
// the new digest is reproducible across two `make go-test` runs.
//
// The fixture below is a synthetic fstest.MapFS, intentionally decoupled from
// the real internal/build/assets/Dockerfile so single-line Dockerfile cleanups
// (e.g. Plan 07-05's ARG INSTALL_RTK dedupe) don't churn this pin. Confirmed
// reproducible across two `make go-test` runs after Plan 07-05.
func TestComputeImageHashPinnedDigest(t *testing.T) {
	assets := fstest.MapFS{
		"a/Dockerfile": &fstest.MapFile{Data: []byte("FROM scratch\n")},
		"a/entrypoint": &fstest.MapFile{Data: []byte("#!/bin/sh\nexec \"$@\"\n")},
	}
	// Use a fixed tools subset (NOT catalog.Defaults() — that would re-couple the
	// pin to the catalog table size; this fixture is decoupled).
	tools := map[string]bool{
		"azure": true,
		"go":    false,
		"rtk":   true,
	}
	got, err := computeImageHashFromFS(assets, "a", "v1.2.3-pin", tools)
	if err != nil {
		t.Fatalf("computeImageHashFromFS: %v", err)
	}
	const want = "a94fa8dacf9e"
	if got != want {
		t.Errorf("pinned digest changed: got %q, want %q\n"+
			"If this is intentional (catalog encoding changed), update `want` "+
			"to %q after confirming reproducibility.", got, want, got)
	}
}

// TestComputeImageHashUsesCatalogCanonicalEncoder verifies that the build-layer
// hash function delegates the tools-section encoding to internal/catalog.
// The D-10 invariant — populating Description / InitScript / SmokeTest must
// not shift the canonical bytes — is enforced at the catalog layer by
// TestCanonicalEncodingIsNeutralToOptionalFieldPopulation (in
// internal/catalog/catalog_test.go). This test is the wiring assertion that
// ensures computeImageHashFromFS consumes that encoder, so the catalog-layer
// guarantee transitively applies to every user's `toolbox:local-<hash>`.
//
// If a future contributor inlines the tools-section encoding (skipping
// catalog.WriteCanonical), this test fails and the D-10 chain breaks.
func TestComputeImageHashUsesCatalogCanonicalEncoder(t *testing.T) {
	src, err := os.ReadFile("tag.go")
	if err != nil {
		t.Fatalf("read tag.go: %v", err)
	}
	// Locate the computeImageHashFromFS function body.
	const fnSig = "func computeImageHashFromFS("
	start := bytes.Index(src, []byte(fnSig))
	if start < 0 {
		t.Fatalf("computeImageHashFromFS not found in tag.go")
	}
	// Extract from the function start to the next top-level `func ` keyword
	// (or to EOF if it's the last function in the file).
	rest := src[start:]
	nextFn := bytes.Index(rest[len(fnSig):], []byte("\nfunc "))
	var body []byte
	if nextFn < 0 {
		body = rest
	} else {
		body = rest[:len(fnSig)+nextFn]
	}
	if !bytes.Contains(body, []byte("catalog.WriteCanonical")) {
		t.Errorf("computeImageHashFromFS does not call catalog.WriteCanonical "+
			"(or catalog.WriteCanonicalEntries); D-10 chain is broken.\n"+
			"Function body:\n%s", body)
	}
}
