package build

// DefaultRegistryImage is the canonical reference for every toolbox shell.
// All users land on this tag — the image content is identical across users
// since there is no per-tool opt-out. Kept in sync with the image published
// by .github/workflows/docker-publish.yml.
const DefaultRegistryImage = "ghcr.io/filippolmt/toolbox:latest"

// ResolveImage returns the canonical registry image reference. `toolbox
// build` overwrites this tag in the local cache, so callers that want a
// locally-built image run `toolbox build` first; subsequent shells pick it
// up automatically.
func ResolveImage() string {
	return DefaultRegistryImage
}
