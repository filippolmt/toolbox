package build

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"
	"strconv"

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
	if config.IsDefaultTools(cfg.Tools) {
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

// BuildArgsFromTools turns the tools map into Docker build args. Only disabled
// tools are emitted — enabled tools rely on the Dockerfile's `ARG …=true` defaults,
// which keeps the tag hash stable when a user explicitly writes `foo: true` in
// their config (it has no effect vs. omitting the key).
func BuildArgsFromTools(tools map[string]bool) map[string]*string {
	out := map[string]*string{}
	for _, k := range config.KnownTools {
		enabled, ok := tools[k]
		if !ok {
			// Missing key means default-true.
			continue
		}
		if enabled {
			continue
		}
		arg, ok := config.ToolBuildArg[k]
		if !ok {
			continue
		}
		v := "false"
		out[arg] = &v
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
func computeImageHashFromFS(assets fs.FS, dir, cliVersion string, tools map[string]bool) (string, error) {
	h := sha256.New()

	_, _ = fmt.Fprintf(h, "version:%s\n", cliVersion)

	// Embedded assets — iterate in a stable order.
	entries, err := fs.ReadDir(assets, dir)
	if err != nil {
		return "", fmt.Errorf("read embedded assets: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		data, err := fs.ReadFile(assets, dir+"/"+name)
		if err != nil {
			return "", fmt.Errorf("read embedded %s: %w", name, err)
		}
		_, _ = fmt.Fprintf(h, "asset:%s:%d\n", name, len(data))
		_, _ = h.Write(data)
		_, _ = h.Write([]byte("\n"))
	}

	// Tools map — sorted by key for determinism.
	keys := make([]string, 0, len(tools))
	for k := range tools {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		_, _ = fmt.Fprintf(h, "tool:%s=%s\n", k, strconv.FormatBool(tools[k]))
	}

	sum := h.Sum(nil)
	return hex.EncodeToString(sum)[:12], nil
}
