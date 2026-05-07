package build

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	"github.com/filippolmt/toolbox/internal/catalog"
	"github.com/filippolmt/toolbox/internal/config"
)

// DefaultRegistryImage is the reference pulled when the user's tools config
// matches the defaults. It is kept in sync with the image published by
// .github/workflows/docker-publish.yml.
const DefaultRegistryImage = "ghcr.io/filippolmt/toolbox:latest"

// ResolveImage returns the image reference the CLI should use for this
// configuration and whether it refers to a locally built image.
//
// When the tools config matches the defaults, it returns the registry image —
// the caller should `docker pull` it. Otherwise the returned reference is
// `toolbox:local-<hash>`, a tag derived from the CLI version, the embedded
// build context, and the opt-out selection; the caller should build it locally
// if it is not already present.
func ResolveImage(cfg *config.Config, cliVersion string) (ref string, isLocal bool) {
	if catalog.IsDefault(cfg.Tools) {
		return DefaultRegistryImage, false
	}
	h, err := computeImageHash(cliVersion, cfg.Tools)
	if err != nil {
		// Embed read can only fail if the build-time go:embed is wrong, which
		// is a programmer error. Surface a stable fallback to avoid panicking
		// in user code paths.
		h = "unknown"
	}
	return "toolbox:local-" + h, true
}

// BuildArgsFromTools turns the tools map into Docker build args. Only entries
// whose resolved enabled-bool DIFFERS from their catalog Default are emitted —
// matches-default entries rely on the Dockerfile's baked-in `ARG INSTALL_X=<default>`,
// which keeps the local-image hash stable when a user explicitly writes the
// default value in their config (it has no effect vs. omitting the key).
//
// A missing key is treated as the catalog Default (the catalog is the source
// of truth, so the same rule applies whether a tool is opt-in or opt-out).
//
// When the catalog ships an entry with Default:false, the Dockerfile's
// `ARG INSTALL_X=` directive must match (`=false`) for the no-emit-on-match
// invariant to hold; the CAT-04 bijection test enforces ARG-name parity but
// does not assert default-value parity, so add a Dockerfile-side default
// alignment when introducing any opt-in tool.
func BuildArgsFromTools(tools map[string]bool) map[string]*string {
	out := map[string]*string{}
	for _, e := range catalog.Entries {
		resolved, ok := tools[e.Key]
		if !ok {
			// Missing key → catalog Default; matches the Dockerfile ARG default.
			continue
		}
		if resolved == e.Default {
			// User-supplied value matches catalog Default → no override needed.
			continue
		}
		v := strconv.FormatBool(resolved)
		out[e.BuildArg] = &v
	}
	return out
}

// computeImageHash produces a 12-hex-char identifier deterministic in:
//   - CLI version (invalidates stale images after a brew upgrade)
//   - embedded build context bytes (any Dockerfile / script change → new tag)
//   - opt-out tools map (any config flip → new tag)
func computeImageHash(cliVersion string, tools map[string]bool) (string, error) {
	return computeImageHashFromFS(Assets, AssetDir, cliVersion, tools)
}

// computeImageHashFromFS is the core hashing routine, parameterised on the
// asset filesystem so tests can swap the embedded build context for a
// fixture and verify that asset edits produce a different hash without
// rebuilding the binary.
//
// Phase 10: walks the asset tree recursively (fs.WalkDir) so the new
// assets/init.d/ subtree contributes to the hash. The walk emits asset
// records keyed by the path relative to dir (e.g. "Dockerfile",
// "init.d/10-rtk.sh"), so a fstest.MapFS with only flat files at the dir
// root produces the same record stream as the prior ReadDir loop —
// preserving the pinned digest in TestComputeImageHashPinnedDigest.
func computeImageHashFromFS(assets fs.FS, dir, cliVersion string, tools map[string]bool) (string, error) {
	h := sha256.New()

	_, _ = fmt.Fprintf(h, "version:%s\n", cliVersion)

	// Collect every non-directory entry under dir, then iterate in a stable
	// alphabetical order so the digest is deterministic regardless of the
	// underlying fs.FS walk order.
	type asset struct {
		rel  string
		data []byte
	}
	var assetsList []asset
	walkErr := fs.WalkDir(assets, dir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(assets, p)
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", p, err)
		}
		rel := p
		if dir != "" {
			rel = strings.TrimPrefix(p, dir+"/")
		}
		assetsList = append(assetsList, asset{rel: rel, data: data})
		return nil
	})
	if walkErr != nil {
		return "", fmt.Errorf("read embedded assets: %w", walkErr)
	}
	sort.Slice(assetsList, func(i, j int) bool { return assetsList[i].rel < assetsList[j].rel })
	for _, a := range assetsList {
		_, _ = fmt.Fprintf(h, "asset:%s:%d\n", a.rel, len(a.data))
		_, _ = h.Write(a.data)
		_, _ = h.Write([]byte("\n"))
	}

	// Tools section — encoded by the Tool Catalog's canonical encoder (D-09, CAT-05).
	// Optional Entry fields (Description, InitScript, SmokeTest) are EXCLUDED from
	// the encoding (D-10) so Phase 10 populating them is hash-neutral for users.
	// The structural guarantee for D-10 lives at the catalog layer
	// (TestCanonicalEncodingIsNeutralToOptionalFieldPopulation in
	// internal/catalog/catalog_test.go); this site only needs to delegate.
	if err := catalog.WriteCanonical(h, tools); err != nil {
		return "", err
	}

	sum := h.Sum(nil)
	return hex.EncodeToString(sum)[:12], nil
}
