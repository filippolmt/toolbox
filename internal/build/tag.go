package build

import "strings"

// DefaultRegistryImage is the canonical reference every toolbox shell lands
// on when no override is configured. The image content is identical across
// users (there is no per-tool opt-out), and the tag is kept in sync with the
// image published by .github/workflows/docker-publish.yml.
const DefaultRegistryImage = "ghcr.io/filippolmt/toolbox:latest"

// ResolveImage applies the image-selection precedence, highest first:
//
//	image           full ref override — used verbatim (e.g. a private mirror
//	                tag, or a pinned digest)
//	registryMirror  registry-host swap — the canonical path+tag is preserved
//	                and only DefaultRegistryImage's host is replaced, so a
//	                pull-through cache / proxy hub (Harbor, Artifactory, Nexus,
//	                ECR pull-through) relocates the *source* without diverging
//	                the image identity
//	(neither)       DefaultRegistryImage
//
// Both inputs are opt-in (zero value = canonical). `toolbox build` still
// overwrites DefaultRegistryImage's local cache, so a full `image` override
// is a pull-source concern: with it set, a local build of the canonical tag
// no longer satisfies the resolved ref (a `registry_mirror` does, since the
// content is identical).
func ResolveImage(image, registryMirror string) string {
	if image != "" {
		return image
	}
	if registryMirror != "" {
		_, rest := SplitRegistryHost(DefaultRegistryImage)
		return strings.TrimRight(registryMirror, "/") + "/" + rest
	}
	return DefaultRegistryImage
}

// SplitRegistryHost splits an image ref into its registry host and the
// remaining path+tag. The first slash-separated segment is treated as the
// registry host only when it looks like one — it contains "." or ":", or is
// exactly "localhost" — matching Docker's own heuristic. Refs without an
// explicit host (e.g. "library/alpine:3") return host="" and rest=ref.
func SplitRegistryHost(ref string) (host, rest string) {
	head, tail, ok := strings.Cut(ref, "/")
	if !ok {
		return "", ref
	}
	if strings.ContainsAny(head, ".:") || head == "localhost" {
		return head, tail
	}
	return "", ref
}
