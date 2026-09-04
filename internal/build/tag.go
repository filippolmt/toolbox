package build

import (
	"context"
	"strings"

	"github.com/moby/moby/client"
)

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

// RepoDigest picks the `sha256:...` content digest of ref out of a Docker
// image's RepoDigests list (each entry is `repo@sha256:...`), matching the
// entry whose repo equals ref's repo (registry path minus any tag/digest).
// Returns "" when no entry matches — e.g. a locally built image carries no
// repo digest — so callers treat an unresolvable digest as "unknown" rather
// than guessing. Two host-side consumers, both in session-reload: it
// stamps TOOLBOX_IMAGE_DIGEST at container creation, and the update prefetch
// reads it back off the local store to decide whether to pull.
func RepoDigest(ref string, repoDigests []string) string {
	want := repoOf(ref)
	for _, rd := range repoDigests {
		repo, digest, ok := strings.Cut(rd, "@")
		if !ok {
			continue
		}
		if repoOf(repo) == want {
			return digest
		}
	}
	return ""
}

// repoOf strips any tag and digest suffix from an image ref, leaving the
// bare registry path (`host[:port]/path`). The tag colon is the last colon
// after the last slash, so a registry-host port (e.g. `localhost:5000/img`)
// is not mistaken for a tag.
func repoOf(ref string) string {
	if i := strings.IndexByte(ref, '@'); i >= 0 {
		ref = ref[:i]
	}
	slash := strings.LastIndexByte(ref, '/')
	if colon := strings.LastIndexByte(ref, ':'); colon > slash {
		ref = ref[:colon]
	}
	return ref
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

// LocalRepoDigest reports the repo digest the local Docker store holds for
// ref. The second return says whether the store answered at all, which is not
// the same as carrying a digest: an image built locally exists and has none
// until it is pushed or pulled, and the callers split on exactly that
// difference — the update prefetch abstains when there is no digest, while a
// container's own digest record is rewritten to whatever the store says,
// empty included.
//
// The one place ImageInspect is turned into a repo digest. It was four,
// spelled four ways, before three of them disagreed about what an empty
// answer meant.
// localStore is the local image store LocalRepoDigest reads, declared here at
// the width the read actually uses. → CONTEXT.md, Declared Docker Surface.
type localStore interface {
	ImageInspect(ctx context.Context, ref string, opts ...client.ImageInspectOption) (client.ImageInspectResult, error)
}

func LocalRepoDigest(ctx context.Context, cli localStore, ref string) (string, bool) {
	res, err := cli.ImageInspect(ctx, ref)
	if err != nil {
		return "", false
	}
	return RepoDigest(ref, res.RepoDigests), true
}
