# syntax=docker/dockerfile:1.24
# ============================================================================
# Toolbox — containerized development environment
# Build:  docker build -f internal/build/assets/Dockerfile internal/build/assets
# Test:   make test
#
# Requires BuildKit (Docker 23+ default). Uses --mount=type=cache for apt,
# cargo registry, and npm cache to keep rebuilds fast across layers.
#
# Every optional tool below is guarded by an INSTALL_<TOOL>=true build arg.
# The toolbox CLI translates the user's `tools:` config into these build args.
# When INSTALL_<TOOL>=false, the corresponding install block short-circuits
# and the tool's files are absent from the final image. (rtk's flag also
# gates the multi-stage `rtk-builder` so opting out skips the cargo build
# entirely on arm64 — see the stage's own block for details.)
# ============================================================================

# -- Builder stage: rtk binary (TOOL-RTK) -------------------------------------
# Single source for the rtk binary across both arches. Output: /out/bin/rtk.
#
#   amd64 → curl static MUSL tarball + verify SHA256 against upstream
#           checksums.txt (gh/glab pattern). Fast path.
#   arm64 → cargo install --git from source against the bookworm sysroot.
#           Upstream's `aarch64-unknown-linux-gnu` build links against
#           GLIBC 2.39 which bookworm (2.36) doesn't ship; there is no
#           aarch64-musl release. Building locally fixes the ABI mismatch.
#
# Centralising both branches in one builder stage keeps Layer 13c in the
# main image trivial (single COPY + version check). When INSTALL_RTK=false,
# the RUN short-circuits to a placeholder so opting out doesn't pay either
# the cargo build (arm64) or the tarball download (amd64) cost.
#
# Re-evaluate the arm64 cargo build when upstream publishes
# aarch64-unknown-linux-musl OR when Microsoft Azure CLI's apt repo gains
# a trixie suite (currently bookworm-only — blocks bumping the base image
# to GLIBC 2.41+, which would let arm64 use the upstream gnu binary).
ARG RTK_VERSION=0.40.0

FROM rust:1-slim-bookworm AS rtk-builder
ARG TARGETARCH
ARG RTK_VERSION
ARG INSTALL_RTK=true
# rust:1-slim-bookworm has cargo + git but ships no curl / ca-certificates, so
# apt installs them up front for the amd64 tarball path. arm64 also needs
# build-essential + pkg-config for the cargo build (rusqlite's bundled SQLite
# pulls in `cc`).
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked,id=rtk-apt-cache \
    --mount=type=cache,target=/var/lib/apt/lists,sharing=locked,id=rtk-apt-lists \
    --mount=type=cache,target=/usr/local/cargo/registry,id=rtk-cargo-registry \
    --mount=type=cache,target=/usr/local/cargo/git,id=rtk-cargo-git \
    set -eux; \
    mkdir -p /out/bin; \
    if [ "${INSTALL_RTK}" != "true" ]; then \
      echo "rtk-builder: skip (INSTALL_RTK=${INSTALL_RTK})"; \
      : > /out/bin/rtk; \
      exit 0; \
    fi; \
    rm -f /etc/apt/apt.conf.d/docker-clean; \
    apt-get update -qqy; \
    apt-get install -qqy --no-install-recommends curl ca-certificates; \
    if [ "${TARGETARCH}" = "amd64" ]; then \
      RTK_ARCH=x86_64-unknown-linux-musl; \
      RTK_TARBALL="rtk-${RTK_ARCH}.tar.gz"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o "/tmp/${RTK_TARBALL}" \
        "https://github.com/rtk-ai/rtk/releases/download/v${RTK_VERSION}/${RTK_TARBALL}"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /tmp/rtk_checksums.txt \
        "https://github.com/rtk-ai/rtk/releases/download/v${RTK_VERSION}/checksums.txt"; \
      cd /tmp && grep "${RTK_TARBALL}" rtk_checksums.txt | sha256sum -c -; \
      tar -xzf "/tmp/${RTK_TARBALL}" -C /out/bin rtk; \
      chmod +x /out/bin/rtk; \
    elif [ "${TARGETARCH}" = "arm64" ]; then \
      apt-get install -qqy --no-install-recommends build-essential pkg-config; \
      cargo install --git https://github.com/rtk-ai/rtk --tag "v${RTK_VERSION}" --root /out --locked rtk; \
    else \
      echo "Unsupported arch: ${TARGETARCH}" && exit 1; \
    fi

FROM node:24-bookworm-slim

# -- Version pinning (D-09) ---------------------------------------------------
# Bumps are driven by Renovate (see renovate.json for the per-ARG mapping).
# The verification steps below fetch the matching SHA256 from upstream at
# build time, so changing only the version here keeps checksums correct.
ARG TARGETARCH
ARG DEBIAN_FRONTEND=noninteractive
ARG GH_VERSION=2.92.0
ARG GLAB_VERSION=1.97.0
ARG GWS_VERSION=0.22.5
ARG ATUIN_VERSION=18.16.1
ARG KUBECTL_VERSION=v1.36.1
ARG KUBECTX_VERSION=0.11.0
ARG HELM_VERSION=v4.2.0
ARG TOFU_VERSION=1.12.0
ARG DOCKER_CLI_VERSION=29.4.3
ARG JQ_VERSION=1.8.1
ARG YQ_VERSION=4.53.2
ARG STARSHIP_VERSION=1.25.1
ARG PNPM_VERSION=11.1.2
ARG BUN_VERSION=1.3.14
ARG UV_VERSION=0.11.14
ARG CLAUDE_CODE_VERSION=2.1.141
ARG CODEX_CLI_VERSION=0.130.0
ARG CF_CLI_VERSION=0.0.5
ARG WRANGLER_VERSION=4.91.0
ARG PYRIGHT_VERSION=1.1.409
# RTK_VERSION lives in the global ARG block at the top of the file
# (consumed only by the rtk-builder stage; the main stage just COPYs the
# resulting binary, so it doesn't need to redeclare the ARG here).
ARG GCLOUD_VERSION=568.0.0
ARG AZURE_CLI_VERSION=2.86.0
ARG OCI_VERSION=3.82.0
ARG GRAPHIFY_VERSION=0.7.19
ARG PLAYWRIGHT_VERSION=1.60.0
ARG PLAYWRIGHT_CLI_VERSION=0.1.13
ARG COMPOSE_VERSION=5.1.3
ARG GO_VERSION=1.26.3
ARG GOPLS_VERSION=0.21.1
ARG GOIMPORTS_VERSION=0.45.0
ARG TINI_VERSION=0.19.0
ARG BAT_VERSION=0.26.1
# sharkdp/bat does not publish per-release checksum files — SHA256 is pinned
# per-arch as a build arg (same pattern as ZOXIDE_SHA256_*). Renovate bumps
# BAT_VERSION; the maintainer refreshes these two hashes in the same PR.
# Accepted risk: analogous to T-01-08 but tighter (hash still verified).
ARG BAT_SHA256_AMD64=726f04c8f576a7fd18b7634f1bbf2f915c43494c1c0f013baa3287edb0d5a2a3
ARG BAT_SHA256_ARM64=422eb73e11c854fddd99f5ca8461c2f1d6e6dce0a2a8c3d5daade5ffcb6564aa

# -- zsh bundle pinning (D-01) ------------------------------------------------
# OMZ + plugins are pinned by commit SHA (reproducibility — `master` drifts).
# Renovate manages bumps via customManagers on these ARG names (see renovate.json).
# SHAs MUST be full 40-char form (abbreviated SHAs break `git fetch --depth=1`).
# Renovate keeps these pinned to the current HEAD of each upstream branch.
ARG OMZ_COMMIT=a07126330b9f56f12f1e00ddd29786d35d6e64d9
ARG ZSH_AUTOSUGGESTIONS_COMMIT=85919cd1ffa7d2d5412f6d3fe437ebdbeeec4fc5
ARG ZSH_SYNTAX_HIGHLIGHTING_COMMIT=1d85c692615a25fe2293bdd44b34c217d5d2bf04
ARG ZSH_COMPLETIONS_COMMIT=64f802936cc368202957e1db42e9305eaaddda4d
ARG FZF_TAB_COMMIT=e394092c17277c84cb3d234917c4ac1073102ba6
ARG ZOXIDE_VERSION=0.9.9
# zoxide v0.9.9 does NOT publish a per-release sha256sums file (tofu/helm
# pattern not applicable — upstream simply does not ship one). SHA256 is
# pinned per-arch below; Renovate bumps ZOXIDE_VERSION and the maintainer
# refreshes these two literals in the same PR. Accepted risk analogous to
# T-01-08 for Docker CLI / gcloud (HTTPS + version pin), but tighter because
# we still verify a cryptographic hash.
ARG ZOXIDE_SHA256_AMD64=4ff057d3c4d957946937274c2b8be7af2a9bbae7f90a1b5e9baaa7cb65a20caa
ARG ZOXIDE_SHA256_ARM64=96e6ea2e47a71db42cb7ad5a36e9209c8cb3708f8ae00f6945573d0d93315cb0

# -- Opt-out flags (one per tool, driven by tools.<key> in .toolbox.yaml) -----
ARG INSTALL_KUBECTL=true
ARG INSTALL_HELM=true
ARG INSTALL_TOFU=true
ARG INSTALL_GH=true
ARG INSTALL_GLAB=true
ARG INSTALL_GWS=true
ARG INSTALL_ATUIN=true
ARG INSTALL_GO=true
# gopls / goimports are independently opt-out-able even when INSTALL_GO=true.
# Each is gated inside Layer 14a so users who want the Go runtime without the
# LSP / formatter (smaller image, no background indexer) can flip them off
# while keeping the toolchain. Both flags are no-ops when INSTALL_GO=false —
# the entire layer skips.
ARG INSTALL_GOPLS=true
ARG INSTALL_GOIMPORTS=true
ARG INSTALL_DOCKER_CLI=true
ARG INSTALL_STARSHIP=true
ARG INSTALL_JQ=true
ARG INSTALL_YQ=true
ARG INSTALL_PNPM=true
ARG INSTALL_BUN=true
ARG INSTALL_UV=true
ARG INSTALL_CLAUDE_CODE=true
ARG INSTALL_CODEX_CLI=true
ARG INSTALL_CF=true
ARG INSTALL_WRANGLER=true
ARG INSTALL_GCLOUD=true
ARG INSTALL_AZURE=true
ARG INSTALL_OCI=true
ARG INSTALL_GRAPHIFY=true
ARG INSTALL_PLAYWRIGHT=true
ARG INSTALL_PLAYWRIGHT_CLI=true
ARG INSTALL_COMPOSE=true
ARG INSTALL_ZSH=true
ARG INSTALL_BAT=true
ARG INSTALL_PYRIGHT=true
# INSTALL_RTK is ALSO declared in the rtk-builder stage above (~line 38),
# but Dockerfile ARGs are stage-scoped — the main stage needs its own
# declaration so the conditional `if [ "${INSTALL_RTK}" != "true" ]`
# branches (Layer 13c rtk install, plus the COPY-from-rtk-builder logic)
# resolve to a real value at build time. Removing this line silently
# disables rtk in default builds — the CAT-04 bijection test does not
# catch it because it uses set semantics over the entire file (line 38
# alone satisfies "INSTALL_RTK appears in Dockerfile") even though Docker
# itself sees two distinct stage-scoped ARGs.
ARG INSTALL_RTK=true

# consoledonottrack.com cross-tool convention. Honored by bun, playwright, and
# many newer CLIs. Tool-specific kills live next to each install RUN below.
ENV DO_NOT_TRACK=1

# -- Layer 1: apt-get (always-on base) ----------------------------------------
# Transitive deps of almost every tool. Unconditional so opt-outs don't need
# cascade rules.
#
# `rm -f docker-clean` once: removes Debian hook that wipes /var/cache/apt
# after install, so BuildKit cache mounts below retain .debs across rebuilds.
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked,id=apt-cache \
    --mount=type=cache,target=/var/lib/apt/lists,sharing=locked,id=apt-lists \
    set -eux; \
    rm -f /etc/apt/apt.conf.d/docker-clean; \
    apt-get update -qqy; \
    apt-get install -qqy --no-install-recommends \
      curl \
      ca-certificates \
      git \
      openssh-client \
      ripgrep \
      bash-completion \
      make \
      python3 \
      python3-pip \
      python-is-python3 \
      gnupg \
      vim-tiny

# -- Layer 2: kubectl (TOOL-05, IMG-02) ---------------------------------------
RUN set -eux; \
    if [ "${INSTALL_KUBECTL}" != "true" ]; then \
      echo "Skipping kubectl (tools.kubectl=false)"; \
    else \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -O "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${TARGETARCH}/kubectl"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o kubectl.sha256 \
        "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${TARGETARCH}/kubectl.sha256"; \
      echo "$(cat kubectl.sha256)  kubectl" | sha256sum -c -; \
      chmod +x kubectl; \
      mv kubectl /usr/local/bin/kubectl; \
      rm kubectl.sha256; \
      kubectl version --client; \
    fi

# -- Layer 2a: kubectx + kubens (kubectl context/namespace switchers) ---------
# Both binaries ship in the same ahmetb/kubectx release with a shared
# checksums.txt (gh-style). One version pin covers both. Linux assets use the
# GNU arch suffix (x86_64 / arm64) — translate from Docker's TARGETARCH
# (amd64 / arm64) before building the URL.
#
# Gated on INSTALL_KUBECTL because kubens shells out to `kubectl get
# namespaces` (no value without kubectl on PATH) and kubectx is meaningless
# without a kubeconfig managed alongside kubectl. Same dependent-tool pattern
# as gopls/goimports under INSTALL_GO.
RUN set -eux; \
    if [ "${INSTALL_KUBECTL}" != "true" ]; then \
      echo "Skipping kubectx + kubens (tools.kubectl=false)"; \
    else \
      case "${TARGETARCH}" in \
        amd64) KUBECTX_ARCH=x86_64;; \
        arm64) KUBECTX_ARCH=arm64;; \
        *) echo "Unsupported arch: ${TARGETARCH}" && exit 1;; \
      esac; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /tmp/kubectx_checksums.txt \
        "https://github.com/ahmetb/kubectx/releases/download/v${KUBECTX_VERSION}/checksums.txt"; \
      for tool in kubectx kubens; do \
        TARBALL="${tool}_v${KUBECTX_VERSION}_linux_${KUBECTX_ARCH}.tar.gz"; \
        curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o "/tmp/${TARBALL}" \
          "https://github.com/ahmetb/kubectx/releases/download/v${KUBECTX_VERSION}/${TARBALL}"; \
        (cd /tmp && grep "${TARBALL}" kubectx_checksums.txt | sha256sum -c -); \
        tar -xzf "/tmp/${TARBALL}" -C /usr/local/bin "${tool}"; \
        chmod +x "/usr/local/bin/${tool}"; \
        rm "/tmp/${TARBALL}"; \
      done; \
      rm /tmp/kubectx_checksums.txt; \
      # Zsh completions: upstream ships static `_<tool>.zsh` files in the repo
      # but NOT in the release tarball, and the binary has no `completion zsh`
      # subcommand. Fetch the per-tag files and drop them into vendor-completions
      # under the bare `_<tool>` name so zsh autoloads them via fpath. Same
      # write-unconditionally rationale as bat (Layer 10a): a few KB orphaned
      # when INSTALL_ZSH=false, harmless. No upstream checksum exists for these
      # raw files — accepted risk T-01-08 class (HTTPS + tag pin).
      mkdir -p /usr/share/zsh/vendor-completions; \
      for tool in kubectx kubens; do \
        curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 \
          -o "/usr/share/zsh/vendor-completions/_${tool}" \
          "https://raw.githubusercontent.com/ahmetb/kubectx/v${KUBECTX_VERSION}/completion/_${tool}.zsh"; \
      done; \
      kubectx --version; \
      kubens --version; \
    fi

# -- Layer 3: helm (TOOL-05, IMG-02) ------------------------------------------
RUN set -eux; \
    if [ "${INSTALL_HELM}" != "true" ]; then \
      echo "Skipping helm (tools.helm=false)"; \
    else \
      HELM_TARBALL="helm-${HELM_VERSION}-linux-${TARGETARCH}.tar.gz"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o "/tmp/${HELM_TARBALL}" \
        "https://get.helm.sh/${HELM_TARBALL}"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /tmp/helm.sha256 \
        "https://get.helm.sh/${HELM_TARBALL}.sha256sum"; \
      cd /tmp && grep "${HELM_TARBALL}" helm.sha256 | sha256sum -c -; \
      tar -xzf "/tmp/${HELM_TARBALL}" -C /usr/local/bin "linux-${TARGETARCH}/helm" --strip-components=1; \
      rm "/tmp/${HELM_TARBALL}" /tmp/helm.sha256; \
      helm version; \
    fi

# -- Layer 4: tofu (TOOL-05, IMG-02) ------------------------------------------
# CHECKPOINT_DISABLE=1: opt out of HashiCorp Checkpoint version-check pings.
# OpenTofu kept the Checkpoint client when forking from Terraform; same env.
ENV CHECKPOINT_DISABLE=1
RUN set -eux; \
    if [ "${INSTALL_TOFU}" != "true" ]; then \
      echo "Skipping tofu (tools.tofu=false)"; \
    else \
      TOFU_TARBALL="tofu_${TOFU_VERSION}_linux_${TARGETARCH}.tar.gz"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o "/tmp/${TOFU_TARBALL}" \
        "https://github.com/opentofu/opentofu/releases/download/v${TOFU_VERSION}/${TOFU_TARBALL}"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /tmp/tofu_SHA256SUMS \
        "https://github.com/opentofu/opentofu/releases/download/v${TOFU_VERSION}/tofu_${TOFU_VERSION}_SHA256SUMS"; \
      cd /tmp && grep "${TOFU_TARBALL}" tofu_SHA256SUMS | sha256sum -c -; \
      tar -xzf "/tmp/${TOFU_TARBALL}" -C /usr/local/bin tofu; \
      rm "/tmp/${TOFU_TARBALL}" /tmp/tofu_SHA256SUMS; \
      tofu version; \
    fi

# -- Layer 5: gh (TOOL-02, IMG-02) --------------------------------------------
RUN set -eux; \
    if [ "${INSTALL_GH}" != "true" ]; then \
      echo "Skipping gh (tools.gh=false)"; \
    else \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /tmp/gh.tar.gz \
        "https://github.com/cli/cli/releases/download/v${GH_VERSION}/gh_${GH_VERSION}_linux_${TARGETARCH}.tar.gz"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /tmp/gh_checksums.txt \
        "https://github.com/cli/cli/releases/download/v${GH_VERSION}/gh_${GH_VERSION}_checksums.txt"; \
      grep "gh_${GH_VERSION}_linux_${TARGETARCH}.tar.gz" /tmp/gh_checksums.txt \
        | awk '{print $1"  /tmp/gh.tar.gz"}' | sha256sum -c -; \
      tar -xzf /tmp/gh.tar.gz -C /usr/local --strip-components=1 "gh_${GH_VERSION}_linux_${TARGETARCH}/bin/gh"; \
      rm /tmp/gh.tar.gz /tmp/gh_checksums.txt; \
      gh --version; \
    fi

# -- Layer 6: glab (TOOL-02, IMG-02) ------------------------------------------
RUN set -eux; \
    if [ "${INSTALL_GLAB}" != "true" ]; then \
      echo "Skipping glab (tools.glab=false)"; \
    else \
      GLAB_TARBALL="glab_${GLAB_VERSION}_linux_${TARGETARCH}.tar.gz"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o "/tmp/${GLAB_TARBALL}" \
        "https://gitlab.com/api/v4/projects/gitlab-org%2Fcli/packages/generic/glab/${GLAB_VERSION}/${GLAB_TARBALL}"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /tmp/glab_checksums.txt \
        "https://gitlab.com/api/v4/projects/gitlab-org%2Fcli/packages/generic/glab/${GLAB_VERSION}/checksums.txt"; \
      cd /tmp && grep "${GLAB_TARBALL}" glab_checksums.txt | sha256sum -c -; \
      tar -xzf "/tmp/${GLAB_TARBALL}" -C /usr/local/bin bin/glab --strip-components=1; \
      rm "/tmp/${GLAB_TARBALL}" /tmp/glab_checksums.txt; \
      glab version; \
    fi

# -- Layer 6a: gws (Google Workspace CLI) -------------------------------------
# Pre-1.0 Rust binary from googleworkspace/cli. MUSL variant is used because the
# GNU build targets GLIBC 2.39 while the base image (node:24-bookworm-slim) is
# bookworm with GLIBC 2.36 — the `-gnu` tarball fails at runtime with
# "version `GLIBC_2.39' not found". The musl build is statically linked and
# runs on any kernel, matching the zoxide (Layer 20c) strategy. Per-asset
# `<file>.sha256` is published in standard sha256sum format.
#
# GOOGLE_WORKSPACE_CLI_KEYRING_BACKEND=file forces the encryption key for
# `gws auth login` to be written to ~/.config/gws/.encryption_key instead of
# an OS keyring. There's no D-Bus / Secret Service inside the container, so
# the default keyring backend would fail; the file backend persists across
# container recreations via the ~/.toolbox/gws bind-mount (DefaultMounts).
ENV GOOGLE_WORKSPACE_CLI_KEYRING_BACKEND=file
RUN set -eux; \
    if [ "${INSTALL_GWS}" != "true" ]; then \
      echo "Skipping gws (tools.gws=false)"; \
    else \
      case "${TARGETARCH}" in \
        amd64) GWS_ARCH=x86_64-unknown-linux-musl;; \
        arm64) GWS_ARCH=aarch64-unknown-linux-musl;; \
        *) echo "Unsupported arch: ${TARGETARCH}" && exit 1;; \
      esac; \
      GWS_TARBALL="google-workspace-cli-${GWS_ARCH}.tar.gz"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o "/tmp/${GWS_TARBALL}" \
        "https://github.com/googleworkspace/cli/releases/download/v${GWS_VERSION}/${GWS_TARBALL}"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /tmp/gws.sha256 \
        "https://github.com/googleworkspace/cli/releases/download/v${GWS_VERSION}/${GWS_TARBALL}.sha256"; \
      cd /tmp && sha256sum -c gws.sha256; \
      tar -xzf "/tmp/${GWS_TARBALL}" -C /usr/local/bin --strip-components=1 ./gws; \
      chmod +x /usr/local/bin/gws; \
      rm "/tmp/${GWS_TARBALL}" /tmp/gws.sha256; \
      gws --version; \
    fi

# -- Layer 6b: atuin (SQLite-backed shell history) ----------------------------
# Replaces the default Ctrl-R history search with a SQLite-backed fuzzy
# searcher (atuinsh/atuin). MUSL build for the same reason as gws (Layer 6a)
# and zoxide (Layer 20c): bookworm ships GLIBC 2.36, the upstream `-gnu`
# tarball targets a newer GLIBC. The musl build is statically linked.
# Per-asset `<file>.sha256` is published in standard sha256sum format.
#
# ATUIN_CONFIG_DIR consolidates config + DB under a single bind-mount
# (~/.toolbox/atuin/) instead of needing two binds for the upstream split
# (~/.config/atuin + ~/.local/share/atuin). The DB path itself still defaults
# to the XDG data dir, which IS the bind target — see internal/mountplan/defaults.go.
ENV ATUIN_CONFIG_DIR=/home/toolbox/.local/share/atuin/config
RUN set -eux; \
    if [ "${INSTALL_ATUIN}" != "true" ]; then \
      echo "Skipping atuin (tools.atuin=false)"; \
    else \
      case "${TARGETARCH}" in \
        amd64) ATUIN_ARCH=x86_64-unknown-linux-musl;; \
        arm64) ATUIN_ARCH=aarch64-unknown-linux-musl;; \
        *) echo "Unsupported arch: ${TARGETARCH}" && exit 1;; \
      esac; \
      ATUIN_TARBALL="atuin-${ATUIN_ARCH}.tar.gz"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o "/tmp/${ATUIN_TARBALL}" \
        "https://github.com/atuinsh/atuin/releases/download/v${ATUIN_VERSION}/${ATUIN_TARBALL}"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /tmp/atuin.sha256 \
        "https://github.com/atuinsh/atuin/releases/download/v${ATUIN_VERSION}/${ATUIN_TARBALL}.sha256"; \
      cd /tmp && sha256sum -c atuin.sha256; \
      tar -xzf "/tmp/${ATUIN_TARBALL}" -C /usr/local/bin --strip-components=1 \
        "atuin-${ATUIN_ARCH}/atuin"; \
      chmod +x /usr/local/bin/atuin; \
      rm "/tmp/${ATUIN_TARBALL}" /tmp/atuin.sha256; \
      atuin --version; \
    fi

# -- Layer 7: Docker CLI standalone (TOOL-06, IMG-02) -------------------------
# NOTE: Docker does not publish per-artifact .sha256 files for static binaries
# (download.docker.com returns 404). Checksum verification is not possible here.
# Accepted risk: T-01-08. Mitigated by HTTPS + version pinning.
RUN set -eux; \
    if [ "${INSTALL_DOCKER_CLI}" != "true" ]; then \
      echo "Skipping docker CLI (tools.docker=false)"; \
    else \
      case "${TARGETARCH}" in amd64) DOCKER_ARCH=x86_64;; arm64) DOCKER_ARCH=aarch64;; *) DOCKER_ARCH=${TARGETARCH};; esac; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /tmp/docker.tgz \
        "https://download.docker.com/linux/static/stable/${DOCKER_ARCH}/docker-${DOCKER_CLI_VERSION}.tgz"; \
      tar -xzf /tmp/docker.tgz -C /usr/local/bin --strip-components=1 docker/docker; \
      rm /tmp/docker.tgz; \
      docker --version; \
    fi

# -- Layer 8: starship (D-05) -------------------------------------------------
RUN set -eux; \
    if [ "${INSTALL_STARSHIP}" != "true" ]; then \
      echo "Skipping starship (tools.starship=false)"; \
    else \
      case "${TARGETARCH}" in \
        amd64) STARSHIP_TARBALL="starship-x86_64-unknown-linux-gnu.tar.gz";; \
        arm64) STARSHIP_TARBALL="starship-aarch64-unknown-linux-musl.tar.gz";; \
        *) echo "Unsupported arch: ${TARGETARCH}" && exit 1;; \
      esac; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /tmp/starship.tar.gz \
        "https://github.com/starship/starship/releases/download/v${STARSHIP_VERSION}/${STARSHIP_TARBALL}"; \
      tar -xzf /tmp/starship.tar.gz -C /usr/local/bin starship; \
      rm /tmp/starship.tar.gz; \
      starship --version; \
    fi

# -- Layer 9: jq (TOOL-07, IMG-02) --------------------------------------------
RUN set -eux; \
    if [ "${INSTALL_JQ}" != "true" ]; then \
      echo "Skipping jq (tools.jq=false)"; \
    else \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /usr/local/bin/jq \
        "https://github.com/jqlang/jq/releases/download/jq-${JQ_VERSION}/jq-linux-${TARGETARCH}"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /tmp/jq_sha256 \
        "https://github.com/jqlang/jq/releases/download/jq-${JQ_VERSION}/sha256sum.txt"; \
      grep "jq-linux-${TARGETARCH}$" /tmp/jq_sha256 \
        | awk '{print $1"  /usr/local/bin/jq"}' | sha256sum -c -; \
      chmod +x /usr/local/bin/jq; \
      rm /tmp/jq_sha256; \
      jq --version; \
    fi

# -- Layer 10: yq (TOOL-07, IMG-02) -------------------------------------------
# yq checksums file contains 31 hash algorithms per binary (see checksums_hashes_order).
# Field layout: $1=filename, $2=CRC32, ..., $19=SHA-256, ..., $32=BLAKE2B.
# We extract the SHA-256 column ($19) which is algorithm #18 in checksums_hashes_order.
RUN set -eux; \
    if [ "${INSTALL_YQ}" != "true" ]; then \
      echo "Skipping yq (tools.yq=false)"; \
    else \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /usr/local/bin/yq \
        "https://github.com/mikefarah/yq/releases/download/v${YQ_VERSION}/yq_linux_${TARGETARCH}"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /tmp/yq_checksums \
        "https://github.com/mikefarah/yq/releases/download/v${YQ_VERSION}/checksums"; \
      EXPECTED=$(grep "^yq_linux_${TARGETARCH} " /tmp/yq_checksums | awk '{print $19}'); \
      test -n "${EXPECTED}" || { echo "ERROR: SHA-256 hash not found in checksums file"; exit 1; }; \
      echo "${EXPECTED}  /usr/local/bin/yq" | sha256sum -c -; \
      chmod +x /usr/local/bin/yq; \
      rm /tmp/yq_checksums; \
      yq --version; \
    fi

# -- Layer 10a: bat (pretty-printer / cat replacement) ------------------------
# sharkdp/bat does not publish checksum files with releases, so SHA256 is
# pinned per-arch via BAT_SHA256_{AMD64,ARM64} build args (same pattern as
# zoxide — see Layer 20c). Both Linux tarballs are the `unknown-linux-gnu`
# variant to match Debian bookworm's glibc.
RUN set -eux; \
    if [ "${INSTALL_BAT}" != "true" ]; then \
      echo "Skipping bat (tools.bat=false)"; \
    else \
      case "${TARGETARCH}" in \
        amd64) BAT_ARCH=x86_64-unknown-linux-gnu;   BAT_SHA256="${BAT_SHA256_AMD64}";; \
        arm64) BAT_ARCH=aarch64-unknown-linux-gnu;  BAT_SHA256="${BAT_SHA256_ARM64}";; \
        *) echo "Unsupported arch: ${TARGETARCH}" && exit 1;; \
      esac; \
      BAT_TARBALL="bat-v${BAT_VERSION}-${BAT_ARCH}.tar.gz"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o "/tmp/${BAT_TARBALL}" \
        "https://github.com/sharkdp/bat/releases/download/v${BAT_VERSION}/${BAT_TARBALL}"; \
      echo "${BAT_SHA256}  /tmp/${BAT_TARBALL}" | sha256sum -c -; \
      tar -xzf "/tmp/${BAT_TARBALL}" -C /usr/local/bin --strip-components=1 \
        "bat-v${BAT_VERSION}-${BAT_ARCH}/bat"; \
      chmod +x /usr/local/bin/bat; \
      # Ship the `#compdef bat` generator from the release tarball. Written to
      # vendor-completions unconditionally: if INSTALL_ZSH=false the file is
      # orphaned (~10KB) but harmless; if true, Layer 20d picks it up via the
      # same dir. No `bat completion zsh` subcommand exists — static file is
      # the only upstream path.
      mkdir -p /usr/share/zsh/vendor-completions; \
      tar -xzf "/tmp/${BAT_TARBALL}" -C /tmp --strip-components=2 \
        "bat-v${BAT_VERSION}-${BAT_ARCH}/autocomplete/bat.zsh"; \
      mv /tmp/bat.zsh /usr/share/zsh/vendor-completions/_bat; \
      rm "/tmp/${BAT_TARBALL}"; \
      bat --version; \
    fi

# -- Layer 11: pnpm via corepack (TOOL-03) ------------------------------------
RUN set -eux; \
    if [ "${INSTALL_PNPM}" != "true" ]; then \
      echo "Skipping pnpm (tools.pnpm=false)"; \
    else \
      corepack enable; \
      corepack prepare pnpm@${PNPM_VERSION} --activate; \
      pnpm --version; \
    fi

# -- Layer 11a: bun (JavaScript runtime + package manager + bundler) ----------
# oven-sh/bun ships per-arch zip archives plus a per-release SHASUMS256.txt
# (standard `<sha>  <file>` format — gh/glab pattern, but zip instead of
# tar.gz). The GLIBC variant (NOT `-musl`) is used because the base image
# (node:24-bookworm-slim) ships glibc and no musl loader: bun's musl build
# is dynamically linked against /lib/ld-musl-x86_64.so.1, which doesn't
# exist on Debian — invoking the binary then fails ENOENT on the
# interpreter and the kernel reports "not found", masquerading as a
# missing binary. Bun's GLIBC build targets ≥ 2.27 (Ubuntu 18.04);
# bookworm ships 2.36, well above the floor. Opposite of the gws/zoxide
# MUSL rationale, which kicks in only when upstream targets a newer glibc
# than the base image.
#
# Extraction uses Python's stdlib `zipfile` module (python3 is already in
# Layer 1) so we avoid pulling in `apt-get install unzip` for a one-shot
# extract.
#
# Persistent state lives at ~/.bun (install cache, global packages, per-user
# bin). Bound from ~/.toolbox/bun via DefaultMounts() so `bun add -g <pkg>`
# survives `toolbox stop`. ~/.bun/bin is prepended to PATH below so global
# binaries run without the user editing shell rcfiles.
RUN set -eux; \
    if [ "${INSTALL_BUN}" != "true" ]; then \
      echo "Skipping bun (tools.bun=false)"; \
    else \
      case "${TARGETARCH}" in \
        amd64) BUN_ARCH=x64;; \
        arm64) BUN_ARCH=aarch64;; \
        *) echo "Unsupported arch: ${TARGETARCH}" && exit 1;; \
      esac; \
      BUN_DIR="bun-linux-${BUN_ARCH}"; \
      BUN_ZIP="${BUN_DIR}.zip"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o "/tmp/${BUN_ZIP}" \
        "https://github.com/oven-sh/bun/releases/download/bun-v${BUN_VERSION}/${BUN_ZIP}"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /tmp/bun_SHASUMS256.txt \
        "https://github.com/oven-sh/bun/releases/download/bun-v${BUN_VERSION}/SHASUMS256.txt"; \
      cd /tmp && grep "${BUN_ZIP}" bun_SHASUMS256.txt | sha256sum -c -; \
      python3 -m zipfile -e "/tmp/${BUN_ZIP}" /tmp/; \
      mv "/tmp/${BUN_DIR}/bun" /usr/local/bin/bun; \
      chmod +x /usr/local/bin/bun; \
      ln -sf /usr/local/bin/bun /usr/local/bin/bunx; \
      rm -rf "/tmp/${BUN_ZIP}" /tmp/bun_SHASUMS256.txt "/tmp/${BUN_DIR}"; \
      bun --version; \
    fi

# Per-user bun global bin in PATH for every subsequent layer + runtime.
# Same rationale as the npm-global PATH idiom: when INSTALL_BUN=false the
# path resolves to a non-existent dir and has no effect.
ENV PATH="/home/toolbox/.bun/bin:${PATH}"

# -- Layer 12: uv (TOOL-04) ---------------------------------------------------
# Pinned tarball install (not a multi-stage `COPY --from=uv-source`) so the
# layer can be gated by INSTALL_UV like the others.
RUN set -eux; \
    if [ "${INSTALL_UV}" != "true" ]; then \
      echo "Skipping uv (tools.uv=false)"; \
    else \
      case "${TARGETARCH}" in \
        amd64) UV_ARCH=x86_64-unknown-linux-gnu;; \
        arm64) UV_ARCH=aarch64-unknown-linux-gnu;; \
        *) echo "Unsupported arch: ${TARGETARCH}" && exit 1;; \
      esac; \
      UV_TARBALL="uv-${UV_ARCH}.tar.gz"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o "/tmp/${UV_TARBALL}" \
        "https://github.com/astral-sh/uv/releases/download/${UV_VERSION}/${UV_TARBALL}"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /tmp/uv.sha256 \
        "https://github.com/astral-sh/uv/releases/download/${UV_VERSION}/${UV_TARBALL}.sha256"; \
      EXPECTED=$(awk '{print $1}' /tmp/uv.sha256); \
      echo "${EXPECTED}  /tmp/${UV_TARBALL}" | sha256sum -c -; \
      tar -xzf "/tmp/${UV_TARBALL}" -C /usr/local/bin --strip-components=1 \
        "uv-${UV_ARCH}/uv" "uv-${UV_ARCH}/uvx"; \
      rm "/tmp/${UV_TARBALL}" /tmp/uv.sha256; \
      uv --version; \
    fi

# -- Layer 13: Claude Code (TOOL-01) ------------------------------------------
# Auto-update strategy:
#   - CLI binary: pinned. The CLI lives at /usr/local/lib/node_modules
#     (installed as root via `npm install -g`) and the runtime UID has no
#     write access there, so background self-update would fail every shell.
#     Bumps happen via CLAUDE_CODE_VERSION + image rebuild (Renovate-driven).
#   - Plugins: kept fresh. Plugins live under ~/.claude/plugins (user-writable
#     bind-mount), so Claude Code's native plugin auto-updater can refresh
#     them without permission errors.
#
# Env vars:
#   DISABLE_AUTOUPDATER=1
#       Documented kill-switch for the background CLI auto-update check
#       (https://code.claude.com/docs/en/setup#disable-auto-updates).
#       Required: the CLI binary lives in /usr/local/lib/node_modules,
#       installed as root, so the runtime UID can't write to it — auto-update
#       would only generate noisy errors.
#   FORCE_AUTOUPDATE_PLUGINS=1
#       Documented escape hatch
#       (https://code.claude.com/docs/en/discover-plugins#configure-auto-updates):
#       keeps plugin auto-updates running even when DISABLE_AUTOUPDATER is
#       set. Plugins live under the user-writable ~/.claude/plugins bind so
#       refresh works without permission errors. /reload-plugins notification
#       is shown when an update lands.
#   DISABLE_TELEMETRY=1
#   DISABLE_FEEDBACK_COMMAND=1
#   DISABLE_ERROR_REPORTING=1
#       Granular privacy kill-switches (https://code.claude.com/docs/en/env-vars).
#       Together with DISABLE_AUTOUPDATER above, this set covers exactly the
#       four flags that the docs say CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC
#       expands to. We use the granular flags instead of the umbrella because
#       baking NONESSENTIAL_TRAFFIC=1 into the image was correlated with
#       intermittent "/login" prompts on long-lived shells, even though none
#       of the four documented sub-flags is supposed to affect auth — the
#       umbrella appears to gate undocumented behaviour beyond its stated
#       composition. Granular flags reproduce the privacy posture without
#       triggering whatever the umbrella does extra. If a logout regression
#       appears here too, narrow down by removing one flag at a time
#       (DISABLE_TELEMETRY first, since AUTOUPDATER cannot affect auth).
#
#   CLAUDE_CONFIG_DIR=/home/toolbox/.claude
#       Relocates both .credentials.json and .claude.json into the single
#       bind-mounted /home/toolbox/.claude dir. Without it, .claude.json
#       lives at $HOME/.claude.json — outside the mount — so OAuth session
#       state is lost on every container recreate and the user has to
#       /login each shell.
# NPM_CONFIG_FUND/AUDIT: suppress fund banner + network audit on every
# subsequent `npm install -g` (Claude/Pyright/Codex/cf/Playwright/playwright-cli).
ENV NPM_CONFIG_FUND=false \
    NPM_CONFIG_AUDIT=false \
    DISABLE_AUTOUPDATER=1 \
    FORCE_AUTOUPDATE_PLUGINS=1 \
    DISABLE_TELEMETRY=1 \
    DISABLE_FEEDBACK_COMMAND=1 \
    DISABLE_ERROR_REPORTING=1 \
    CLAUDE_CONFIG_DIR=/home/toolbox/.claude

# -- xdg-open no-op shim -----------------------------------------------------
# Several bundled CLIs (cf, wrangler, and any future OAuth-using tool) use
# the npm `open` package to launch the consent page; on Linux that shells
# out to `xdg-open` and the login flow aborts if it is missing. The
# container has no GUI/browser, so the real binary would be useless even if
# installed. A no-op stub is sufficient: the OAuth URL is always printed to
# stdout immediately before the call, so the user copies it into the host
# browser while the CLI's local callback listener keeps running.
# Unconditional — outside any tools.<key> gate so cf-only / wrangler-only
# configs both work, and any future OAuth-using CLI inherits the shim.
RUN printf '#!/bin/sh\nexit 0\n' > /usr/local/bin/xdg-open && \
    chmod +x /usr/local/bin/xdg-open

RUN --mount=type=cache,target=/root/.npm,id=npm-cache \
    set -eux; \
    if [ "${INSTALL_CLAUDE_CODE}" != "true" ]; then \
      echo "Skipping Claude Code (tools.claude=false)"; \
    else \
      npm install -g @anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}; \
      claude --version; \
      find /usr/local/lib/node_modules/@anthropic-ai \
        \( -type f \( -name '*.map' -o -name '*.md' -o -name '*.markdown' \) \
        -o -type d \( -name docs -o -name examples -o -name example \) \) \
        -prune -exec rm -rf {} +; \
    fi

# -- Layer 13a: Pyright language server (TOOL-01) ----------------------------
# Python LSP used by the pyright-lsp Claude Code plugin. Installed as a global
# npm package so `pyright-langserver` lands in /usr/local/bin without needing
# pipx/venv wiring. Gated by INSTALL_PYRIGHT (tools.pyright).
RUN --mount=type=cache,target=/root/.npm,id=npm-cache \
    set -eux; \
    if [ "${INSTALL_PYRIGHT}" != "true" ]; then \
      echo "Skipping pyright (tools.pyright=false)"; \
    else \
      npm install -g pyright@${PYRIGHT_VERSION}; \
      pyright --version; \
      find /usr/local/lib/node_modules/pyright \
        \( -type f \( -name '*.map' -o -name '*.md' -o -name '*.markdown' \) \
        -o -type d \( -name docs -o -name examples -o -name example \) \) \
        -prune -exec rm -rf {} +; \
    fi

# -- Layer 13b: OpenAI Codex CLI (TOOL-01) -----------------------------------
# Installed as a global npm package — npm registry artefacts ship with SLSA
# provenance + integrity hash auto-resolved by npm, and per-arch native binaries
# are pulled via `optionalDependencies` (linux-x64 / linux-arm64). Single
# version source for Renovate, no per-arch SHA pinning needed.
#
# Zsh completion is generated at build time by `codex completion zsh` in
# Layer 20d, same pattern as kubectl / helm / gh.
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked,id=apt-cache \
    --mount=type=cache,target=/var/lib/apt/lists,sharing=locked,id=apt-lists \
    --mount=type=cache,target=/root/.npm,id=npm-cache \
    set -eux; \
    if [ "${INSTALL_CODEX_CLI}" != "true" ]; then \
      echo "Skipping Codex CLI (tools.codex=false)"; \
    else \
      apt-get update -qqy; \
      apt-get install -qqy --no-install-recommends bubblewrap; \
      npm install -g "@openai/codex@${CODEX_CLI_VERSION}"; \
      codex --version; \
      find /usr/local/lib/node_modules/@openai \
        \( -type f \( -name '*.map' -o -name '*.md' -o -name '*.markdown' \) \
        -o -type d \( -name docs -o -name examples -o -name example \) \) \
        -prune -exec rm -rf {} +; \
    fi

# -- Layer 13c: Cloudflare CLI `cf` (TOOL-02) --------------------------------
# Technical preview of the next-generation Cloudflare CLI (Wrangler successor),
# distributed as a single npm package whose `bin` entry exposes `cf`. Same
# install pattern as Claude Code / Codex: registry-shipped tarball with built-in
# integrity, no per-arch SHA pin, single version source for Renovate.
#
# Persistent state splits across two upstream paths (both hard-coded — no env
# override on either):
#   ~/.cf/config.toml             OAuth access_token + refresh_token (auth)
#   ~/.config/cf/config.json      context defaults, completion-install marker
# Both are bound from ~/.toolbox/cf/{auth,config} via DefaultMounts() (rtk
# split pattern). Without the .cf bind specifically, `cf auth login` would
# wipe on every `toolbox stop`.
RUN --mount=type=cache,target=/root/.npm,id=npm-cache \
    set -eux; \
    if [ "${INSTALL_CF}" != "true" ]; then \
      echo "Skipping cf (tools.cf=false)"; \
    else \
      npm install -g "cf@${CF_CLI_VERSION}"; \
      cf --version; \
      # Upstream cf hardcodes OAuth callback bind to 127.0.0.1 with no env \
      # override, so loopback inside container is invisible to `docker -p` \
      # forward → browser hits ERR_EMPTY_RESPONSE. grep guard fails build if \
      # upstream restyles the literal (Renovate bumps `cf@*` regularly). \
      sed -i -E 's/(bindHost:\s*)["'\'']127\.0\.0\.1["'\'']/\1"0.0.0.0"/' \
        /usr/local/lib/node_modules/cf/cf-dist/index.js; \
      grep -qE 'bindHost:\s*"0\.0\.0\.0"' /usr/local/lib/node_modules/cf/cf-dist/index.js; \
      find /usr/local/lib/node_modules/cf \
        \( -type f \( -name '*.map' -o -name '*.md' -o -name '*.markdown' \) \
        -o -type d \( -name docs -o -name examples -o -name example \) \) \
        -prune -exec rm -rf {} +; \
    fi

# -- Layer 13d: Cloudflare Wrangler CLI (TOOL-02) ----------------------------
# Wrangler is Cloudflare's primary Workers CLI (distinct from `cf`, the
# vNext preview installed in Layer 13c). Same install pattern as Claude
# Code / Codex / cf: global npm package with built-in tarball integrity,
# no per-arch SHA pin, single version source for Renovate.
#
# Upstream OS support requires glibc 2.35+; node:24-bookworm-slim ships
# glibc 2.36, so the GLIBC variant is the only viable choice (no -musl
# build is published).
#
# Persistent state: wrangler uses xdg-app-paths(".wrangler") → on Linux
# this resolves to ~/.config/.wrangler/, where OAuth credentials
# (config/default.toml) and login state live. Bound from
# ~/.toolbox/wrangler via DefaultMounts(); without it `wrangler login`
# wipes on every `toolbox stop`.
#
# WRANGLER_SEND_METRICS=false opts out of usage telemetry. DO_NOT_TRACK
# (set globally in Layer 1) is also honoured but the explicit flag is
# documented and stable across versions.
ENV WRANGLER_SEND_METRICS=false
RUN --mount=type=cache,target=/root/.npm,id=npm-cache \
    set -eux; \
    if [ "${INSTALL_WRANGLER}" != "true" ]; then \
      echo "Skipping Wrangler (tools.wrangler=false)"; \
    else \
      npm install -g "wrangler@${WRANGLER_VERSION}"; \
      wrangler --version; \
      # Upstream wrangler hardcodes OAuth callback listen on "localhost":8976 \
      # with no env override, so loopback inside container is invisible to \
      # `docker -p 8976:8976` → browser hits ERR_EMPTY_RESPONSE. grep guard \
      # fails build if upstream restyles the literal (Renovate bumps regular). \
      sed -i -E 's/(\.listen\(8976,[[:space:]]*)"localhost"/\1"0.0.0.0"/' \
        /usr/local/lib/node_modules/wrangler/wrangler-dist/cli.js; \
      grep -qE '\.listen\(8976,[[:space:]]*"0\.0\.0\.0"' /usr/local/lib/node_modules/wrangler/wrangler-dist/cli.js; \
      find /usr/local/lib/node_modules/wrangler \
        \( -type f \( -name '*.map' -o -name '*.md' -o -name '*.markdown' \) \
        -o -type d \( -name docs -o -name examples -o -name example \) \) \
        -prune -exec rm -rf {} +; \
    fi

# -- Layer 14: gcloud (TOOL-02, IMG-02) ---------------------------------------
# NOTE: Google does not publish per-version SHA256 files for Cloud SDK archives
# (the docs list checksums only for the unversioned "latest" URL, which shift on
# every release). Accepted risk following the DOCKER_CLI_VERSION precedent
# (T-01-08): mitigated by HTTPS + version pinning.
#
# CLOUDSDK_CORE_DISABLE_USAGE_REPORTING: opt out of gcloud usage telemetry.
ENV CLOUDSDK_CORE_DISABLE_USAGE_REPORTING=true
RUN set -eux; \
    if [ "${INSTALL_GCLOUD}" != "true" ]; then \
      echo "Skipping gcloud (tools.gcloud=false)"; \
    else \
      case "${TARGETARCH}" in \
        amd64) GCLOUD_ARCH=x86_64;; \
        arm64) GCLOUD_ARCH=arm;; \
        *) echo "Unsupported arch: ${TARGETARCH}" && exit 1;; \
      esac; \
      GCLOUD_TARBALL="google-cloud-cli-${GCLOUD_VERSION}-linux-${GCLOUD_ARCH}.tar.gz"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o "/tmp/${GCLOUD_TARBALL}" \
        "https://dl.google.com/dl/cloudsdk/channels/rapid/downloads/${GCLOUD_TARBALL}"; \
      tar -xzf "/tmp/${GCLOUD_TARBALL}" -C /opt; \
      rm "/tmp/${GCLOUD_TARBALL}"; \
      /opt/google-cloud-sdk/bin/gcloud components install gke-gcloud-auth-plugin --quiet; \
      ln -s /opt/google-cloud-sdk/bin/gcloud /usr/local/bin/gcloud; \
      ln -s /opt/google-cloud-sdk/bin/gsutil /usr/local/bin/gsutil; \
      ln -s /opt/google-cloud-sdk/bin/bq /usr/local/bin/bq; \
      ln -s /opt/google-cloud-sdk/bin/gke-gcloud-auth-plugin /usr/local/bin/gke-gcloud-auth-plugin; \
      gcloud --version; \
      gke-gcloud-auth-plugin --version; \
      find /opt/google-cloud-sdk -type d -name __pycache__ -prune -exec rm -rf {} + 2>/dev/null || true; \
    fi

# -- Layer 14a: Go toolchain + gopls + goimports (GO-01, GO-02, GO-03) --------
# Single RUN gated by INSTALL_GO: downloads the Go tarball (SHA256-verified
# against the hash published in go.dev's release JSON index), extracts to
# /usr/local/go, then runs `go install` for gopls (LSP used by Claude Code)
# and goimports (formatter).
#
# Pattern rationale:
#   - tarball -> verify -> extract -> go install (gopls + goimports) -> cleanup, all in one RUN
#   - -ldflags="-s -w" strips debug info + symbol table on gopls/goimports (~30% smaller)
#   - no ENV GOPATH / GOROOT — Go defaults resolve correctly
#     (GOPATH=$HOME/go=/home/toolbox/go matches the bind-mount target;
#     GOROOT is auto-detected from /usr/local/go/bin/go exec path).
#   - HOME=/tmp/go-install applies ONLY to this RUN's `go install`
#     (inline var), so the global HOME=/home/toolbox (Layer 21) stays intact.
#
# SHA256 source: `https://go.dev/dl/<tarball>.sha256` redirects to the HTML
# documentation page (meta refresh to /dl/#<tarball>.sha256), so the raw hash
# isn't reachable that way. Canonical source is the JSON index at
# https://go.dev/dl/?mode=json — we extract the sha256 via jq (installed in
# Layer 9) and verify with `sha256sum -c -`. Same trust anchor (HTTPS to
# go.dev), same cryptographic guarantee.
RUN set -eux; \
    if [ "${INSTALL_GO}" != "true" ]; then \
      echo "Skipping Go toolchain (tools.go=false)"; \
    else \
      GO_TARBALL="go${GO_VERSION}.linux-${TARGETARCH}.tar.gz"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o "/tmp/${GO_TARBALL}" \
        "https://go.dev/dl/${GO_TARBALL}"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 "https://go.dev/dl/?mode=json&include=all" \
        -o /tmp/go_dl_index.json; \
      EXPECTED=$(jq -r --arg f "${GO_TARBALL}" \
        '.[].files[]? | select(.filename == $f) | .sha256' \
        /tmp/go_dl_index.json | head -n1); \
      test -n "${EXPECTED}" || { echo "ERROR: SHA-256 hash not found for ${GO_TARBALL} in go.dev index"; exit 1; }; \
      echo "${EXPECTED}  /tmp/${GO_TARBALL}" | sha256sum -c -; \
      tar -C /usr/local -xzf "/tmp/${GO_TARBALL}"; \
      rm "/tmp/${GO_TARBALL}" /tmp/go_dl_index.json; \
      # gopls and goimports live in different Go modules (gopls is its own
      # module; goimports is in golang.org/x/tools) so `go install` has to be
      # invoked twice — a single invocation fails with
      # "all arguments must refer to packages in the same module at the same version".
      # Each is independently opt-out-able via INSTALL_GOPLS / INSTALL_GOIMPORTS
      # so users can ship the Go runtime without the LSP / formatter.
      if [ "${INSTALL_GOPLS}" = "true" ]; then \
        HOME=/tmp/go-install \
        GOPATH=/tmp/go-install \
        GOCACHE=/tmp/go-install/cache \
        GOMODCACHE=/tmp/go-install/pkg/mod \
        GOBIN=/usr/local/bin \
        /usr/local/go/bin/go install -ldflags="-s -w" \
          "golang.org/x/tools/gopls@v${GOPLS_VERSION}"; \
        gopls version; \
      else \
        echo "Skipping gopls (tools.gopls=false)"; \
      fi; \
      if [ "${INSTALL_GOIMPORTS}" = "true" ]; then \
        HOME=/tmp/go-install \
        GOPATH=/tmp/go-install \
        GOCACHE=/tmp/go-install/cache \
        GOMODCACHE=/tmp/go-install/pkg/mod \
        GOBIN=/usr/local/bin \
        /usr/local/go/bin/go install -ldflags="-s -w" \
          "golang.org/x/tools/cmd/goimports@v${GOIMPORTS_VERSION}"; \
        # goimports at runtime invokes `go list`, so it needs `go` on PATH.
        # The ENV PATH that prepends /usr/local/go/bin is applied by the next
        # Dockerfile line (after this RUN), so a functional invocation like
        # `echo "" | goimports` would fail here. Presence-check is enough —
        # smoke-test.sh runs the functional check against the final image.
        test -x /usr/local/bin/goimports; \
      else \
        echo "Skipping goimports (tools.goimports=false)"; \
      fi; \
      rm -rf /tmp/go-install; \
      rm -rf /usr/local/go/test /usr/local/go/api /usr/local/go/doc /usr/local/go/misc; \
      find /usr/local/go/src -name '*_test.go' -delete; \
      find /usr/local/go/src -type d -name testdata -prune -exec rm -rf {} +; \
      /usr/local/go/bin/go version; \
    fi

# Go binaries in PATH for every subsequent layer + runtime.
# Applied unconditionally: when INSTALL_GO=false the path simply resolves
# to a non-existent dir and has no effect (matches npm-global PATH idiom).
ENV PATH="/usr/local/go/bin:${PATH}"

# -- Layer 15: Azure CLI (TOOL-02, IMG-02) ------------------------------------
# Installed from Microsoft's Debian apt repository (the upstream-recommended
# path — no standalone binary release is published). Version pinned against
# the repo; trust anchor is the Microsoft GPG key on the repo metadata.
#
# AZURE_CORE_COLLECT_TELEMETRY=false: also stops the telemetry subprocess
# that races with `find __pycache__` cleanup on slow/qemu arm64 runners.
ENV AZURE_CORE_COLLECT_TELEMETRY=false
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked,id=apt-cache \
    --mount=type=cache,target=/var/lib/apt/lists,sharing=locked,id=apt-lists \
    set -eux; \
    if [ "${INSTALL_AZURE}" != "true" ]; then \
      echo "Skipping azure-cli (tools.azure=false)"; \
    else \
      install -d -m 0755 /etc/apt/keyrings; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 https://packages.microsoft.com/keys/microsoft.asc \
        | gpg --dearmor -o /etc/apt/keyrings/microsoft.gpg; \
      echo "deb [arch=${TARGETARCH} signed-by=/etc/apt/keyrings/microsoft.gpg] https://packages.microsoft.com/repos/azure-cli/ bookworm main" \
        > /etc/apt/sources.list.d/azure-cli.list; \
      apt-get update -qqy; \
      apt-get install -qqy --no-install-recommends "azure-cli=${AZURE_CLI_VERSION}-1~bookworm"; \
      az --version; \
      find /opt/az -type d -name __pycache__ -prune -exec rm -rf {} + 2>/dev/null || true; \
      # Strip stdlib parts useless in headless container (IDE, turtle, GUI,
      # pydoc). pip/setuptools kept so `az extension add` still works.
      for d in idlelib turtledemo tkinter pydoc_data; do \
        rm -rf /opt/az/lib/python*/$d; \
      done; \
    fi

# -- Layer 16: OCI CLI (TOOL-02, IMG-02) --------------------------------------
# Installed via pip: Oracle does not publish a standalone binary release, and
# the upstream install.sh bootstraps into a venv we do not need here. Uses
# --break-system-packages to opt out of PEP 668 — accepted for this single-
# purpose image. Transitive deps are locked by pip's dependency resolver at
# install time against the pinned OCI_VERSION.
RUN set -eux; \
    if [ "${INSTALL_OCI}" != "true" ]; then \
      echo "Skipping oci-cli (tools.oci=false)"; \
    else \
      pip install --break-system-packages --no-cache-dir \
        --timeout 300 --retries 5 \
        "oci-cli==${OCI_VERSION}"; \
      oci --version; \
      find /usr/local/lib/python* -type d -name __pycache__ -prune -exec rm -rf {} + 2>/dev/null || true; \
    fi

# -- Layer 16a: graphify (knowledge-graph builder, Claude Code skill) ---------
# Installed via pip (oci pattern). Upstream PyPI name is `graphifyy` (double y)
# while the upstream `graphify` name is being reclaimed; the CLI binary and
# Claude Code skill command are still `graphify`. Same --break-system-packages
# rationale as Layer 16. Skill is NOT auto-installed at entrypoint — users run
# `graphify install` once after first `toolbox shell` to write
# ~/.claude/skills/graphify/SKILL.md (persists via the .claude bind-mount).
RUN set -eux; \
    if [ "${INSTALL_GRAPHIFY}" != "true" ]; then \
      echo "Skipping graphify (tools.graphify=false)"; \
    else \
      pip install --break-system-packages --no-cache-dir \
        --timeout 300 --retries 5 \
        "graphifyy==${GRAPHIFY_VERSION}"; \
      # Upstream CLI has no `--version` / `version` subcommand (subcommands
      # are install/path/explain/clone/merge-driver/…). `--help` exits 0 and
      # validates the entry point; pip show prints the installed version.
      graphify --help >/dev/null; \
      pip show graphifyy | grep ^Version:; \
      find /usr/local/lib/python* -type d -name __pycache__ -prune -exec rm -rf {} + 2>/dev/null || true; \
    fi

# -- Layer 17: Playwright CLI + Chromium deps (TOOL-01) -----------------------
# Installs the CLI globally and the system libraries Chromium needs at
# runtime (fonts, libnss3, libgbm, libasound2, libxcb-*, …). `playwright
# install-deps chromium` resolves the exact apt list for the current image
# and runs apt-get install — which needs root, so it must happen at build
# time (the runtime user has no sudo). Firefox/WebKit deps are skipped to
# keep the image lean; opt in via `playwright install-deps firefox` etc. at
# build-time overrides if needed.
#
# Browsers (~170MB chromium, much more for full matrix) are NOT pre-baked.
# They are fetched on demand with `playwright install` into the
# ~/.toolbox/playwright-cache bind-mount (internal/config/config.go), which
# persists them across container restarts.
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked,id=apt-cache \
    --mount=type=cache,target=/var/lib/apt/lists,sharing=locked,id=apt-lists \
    --mount=type=cache,target=/root/.npm,id=npm-cache \
    set -eux; \
    if [ "${INSTALL_PLAYWRIGHT}" != "true" ]; then \
      echo "Skipping playwright (tools.playwright=false)"; \
    else \
      npm install -g "playwright@${PLAYWRIGHT_VERSION}"; \
      playwright --version; \
      apt-get update -qqy; \
      playwright install-deps chromium; \
      find /usr/local/lib/node_modules/playwright \
        \( -type f \( -name '*.map' -o -name '*.md' -o -name '*.markdown' \) \
        -o -type d \( -name docs -o -name examples -o -name example \) \) \
        -prune -exec rm -rf {} +; \
    fi

# -- Layer 18: Playwright CLI with SKILLS (TOOL-01) ---------------------------
# Agent-oriented companion to the `playwright` CLI above. Installs as the
# `playwright-cli` binary (note the `-cli` suffix — different from the
# `playwright` binary in Layer 17). Ships pre-made Claude Code skills for
# browser automation; install them at runtime with `playwright-cli install
# --skills`. Kept as a separate opt-out because not everyone using Playwright
# wants the agent flow, and vice-versa.
RUN --mount=type=cache,target=/root/.npm,id=npm-cache \
    set -eux; \
    if [ "${INSTALL_PLAYWRIGHT_CLI}" != "true" ]; then \
      echo "Skipping @playwright/cli (tools.playwright_cli=false)"; \
    else \
      npm install -g "@playwright/cli@${PLAYWRIGHT_CLI_VERSION}"; \
      playwright-cli --version; \
      find /usr/local/lib/node_modules/@playwright \
        \( -type f \( -name '*.map' -o -name '*.md' -o -name '*.markdown' \) \
        -o -type d \( -name docs -o -name examples -o -name example \) \) \
        -prune -exec rm -rf {} +; \
    fi

# -- Layer 19: Docker Compose plugin (TOOL-06, IMG-02) ------------------------
# Installs as a CLI plugin for the static docker binary baked in Layer 7.
# Useless without tools.docker=true — that mismatch is a documented user error,
# not enforced at build time. SHA256 is published alongside each binary, so
# the checksum verification pattern matches gh/glab.
RUN set -eux; \
    if [ "${INSTALL_COMPOSE}" != "true" ]; then \
      echo "Skipping docker-compose (tools.compose=false)"; \
    else \
      case "${TARGETARCH}" in \
        amd64) COMPOSE_ARCH=x86_64;; \
        arm64) COMPOSE_ARCH=aarch64;; \
        *) echo "Unsupported arch: ${TARGETARCH}" && exit 1;; \
      esac; \
      COMPOSE_BIN="docker-compose-linux-${COMPOSE_ARCH}"; \
      install -d -m 0755 /usr/local/libexec/docker/cli-plugins; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /usr/local/libexec/docker/cli-plugins/docker-compose \
        "https://github.com/docker/compose/releases/download/v${COMPOSE_VERSION}/${COMPOSE_BIN}"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /tmp/compose.sha256 \
        "https://github.com/docker/compose/releases/download/v${COMPOSE_VERSION}/${COMPOSE_BIN}.sha256"; \
      EXPECTED=$(awk '{print $1}' /tmp/compose.sha256); \
      echo "${EXPECTED}  /usr/local/libexec/docker/cli-plugins/docker-compose" | sha256sum -c -; \
      chmod +x /usr/local/libexec/docker/cli-plugins/docker-compose; \
      rm /tmp/compose.sha256; \
      /usr/local/libexec/docker/cli-plugins/docker-compose version; \
    fi

# -- Layer 20a: tini (PID 1 init / signal handler) ----------------------------
# `sleep infinity` is the CMD and becomes the PID-1 child via `exec "$@"` in
# entrypoint.sh — chosen specifically because interactive shells (zsh/bash)
# ignore SIGTERM when they own the TTY with job control, so an interactive
# shell as CMD makes `docker stop` hang the full 10s grace before SIGKILL.
# `sleep` exits instantly on SIGTERM, so shell teardown is sub-second. The
# user's interactive shell is launched via `docker exec` from the Go CLI, not
# from CMD; nothing about user UX depends on CMD being a shell. tini is a
# minimal init that forwards signals properly and reaps zombies. `-g`
# forwards signals to the entire process group (not just the direct child),
# so the sleep (and any background entrypoint side-effects) receive SIGTERM
# and the container exits cleanly.
#
# Not opt-out: the binary is ~400 KB and correcting PID-1 semantics is a
# cross-cutting concern (every shell/exec/attach path benefits).
RUN set -eux; \
    curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /usr/bin/tini \
      "https://github.com/krallin/tini/releases/download/v${TINI_VERSION}/tini-static-${TARGETARCH}"; \
    curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /tmp/tini.sha256sum \
      "https://github.com/krallin/tini/releases/download/v${TINI_VERSION}/tini-static-${TARGETARCH}.sha256sum"; \
    EXPECTED=$(awk '{print $1}' /tmp/tini.sha256sum); \
    echo "${EXPECTED}  /usr/bin/tini" | sha256sum -c -; \
    chmod +x /usr/bin/tini; \
    rm /tmp/tini.sha256sum; \
    /usr/bin/tini --version

# -- Layer 20b: zsh + Oh-My-Zsh + 4 custom plugins (ZSH-01/02/03/04/07) -------
# Split: heavy install RUN + separate COPY zshrc.sh + cheap mv. zshrc.sh edits
# don't invalidate the OMZ git clones.
#
# `.git` retained (per R-05, SPEC ZSH-02 acceptance: `git rev-parse HEAD`
# equals OMZ_COMMIT). Delta ~24 MB, within 50 MB budget.
#
# Home dir trick: writes go to /home/node/.oh-my-zsh because Layer 21 runs
# `usermod -d /home/toolbox -m toolbox`, which relocates the dir but refuses
# if /home/toolbox already exists. End state: /home/toolbox/.oh-my-zsh.
#
# Pattern: `git init + remote add + fetch --depth=1 <sha> + checkout FETCH_HEAD`.
# `git clone --depth=1 --branch <sha>` FAILS — --branch accepts named refs only.
# GitHub enables `uploadpack.allowReachableSHA1InWant=true`, so full-SHA fetches
# work. Abbreviated SHAs do NOT work — always use the full 40-char form.
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked,id=apt-cache \
    --mount=type=cache,target=/var/lib/apt/lists,sharing=locked,id=apt-lists \
    set -eux; \
    if [ "${INSTALL_ZSH}" != "true" ]; then \
      echo "Skipping zsh + Oh-My-Zsh (tools.zsh=false)"; \
    else \
      apt-get update -qqy; \
      # node:24-bookworm-slim ships /etc/dpkg/dpkg.cfg.d/docker-clean (or
      # similar) with `path-exclude /usr/share/doc/*`, which silently strips
      # fzf's key-bindings.{bash,zsh,fish} + completion.{bash,zsh} integration
      # scripts at install time. Without re-including them, our bashrc/zshrc
      # `source /usr/share/doc/fzf/examples/*` lines no-op — fzf works as a
      # binary but Ctrl-R / Ctrl-T / Alt-C bindings stay dormant. The override
      # filename is lexically LAST in dpkg.cfg.d so it post-dates the slim
      # path-exclude rule. Applies only to fzf — every other tool's docs stay
      # stripped per upstream design. \
      echo 'path-include /usr/share/doc/fzf/examples/*' \
        > /etc/dpkg/dpkg.cfg.d/zz-keep-fzf-shellintegration; \
      apt-get install -qqy --no-install-recommends zsh fzf; \
      mkdir -p /home/node/.oh-my-zsh/custom/plugins; \
      git -c init.defaultBranch=master init /home/node/.oh-my-zsh; \
      git -C /home/node/.oh-my-zsh remote add origin https://github.com/ohmyzsh/ohmyzsh.git; \
      git -C /home/node/.oh-my-zsh fetch --depth=1 origin "${OMZ_COMMIT}"; \
      git -C /home/node/.oh-my-zsh checkout FETCH_HEAD; \
      for triplet in \
        "zsh-autosuggestions|https://github.com/zsh-users/zsh-autosuggestions.git|${ZSH_AUTOSUGGESTIONS_COMMIT}" \
        "zsh-syntax-highlighting|https://github.com/zsh-users/zsh-syntax-highlighting.git|${ZSH_SYNTAX_HIGHLIGHTING_COMMIT}" \
        "zsh-completions|https://github.com/zsh-users/zsh-completions.git|${ZSH_COMPLETIONS_COMMIT}" \
        "fzf-tab|https://github.com/Aloxaf/fzf-tab.git|${FZF_TAB_COMMIT}" ; do \
        name=$(echo "$triplet" | cut -d'|' -f1); \
        url=$(echo "$triplet"  | cut -d'|' -f2); \
        sha=$(echo "$triplet"  | cut -d'|' -f3); \
        dir="/home/node/.oh-my-zsh/custom/plugins/$name"; \
        git init "$dir"; \
        git -C "$dir" remote add origin "$url"; \
        git -C "$dir" fetch --depth=1 origin "$sha"; \
        git -C "$dir" checkout FETCH_HEAD; \
      done; \
      mkdir -p /etc/zsh; \
      echo '# managed by toolbox — see /etc/zsh/zshrc' > /home/node/.zshrc; \
      zsh --version; \
      fzf --version; \
    fi

COPY zshrc.sh /etc/toolbox-zshrc.sh
RUN set -eux; \
    if [ "${INSTALL_ZSH}" != "true" ]; then \
      rm -f /etc/toolbox-zshrc.sh; \
    else \
      mv /etc/toolbox-zshrc.sh /etc/zsh/zshrc; \
    fi

# -- Layer 20c: zoxide binary (ZSH-07) ----------------------------------------
# Pattern variant: tarball + SHA256 verification against a build-arg pinned
# hash (NOT a per-release checksums file — zoxide upstream does not publish
# one). Both arches use musl (static binary — glibc availability on x86_64
# is irrelevant and upstream only publishes musl for arm64).
RUN set -eux; \
    if [ "${INSTALL_ZSH}" != "true" ]; then \
      echo "Skipping zoxide (tools.zsh=false)"; \
    else \
      case "${TARGETARCH}" in \
        amd64) ZOXIDE_ARCH=x86_64-unknown-linux-musl; ZOXIDE_SHA256="${ZOXIDE_SHA256_AMD64}";; \
        arm64) ZOXIDE_ARCH=aarch64-unknown-linux-musl; ZOXIDE_SHA256="${ZOXIDE_SHA256_ARM64}";; \
        *) echo "Unsupported arch: ${TARGETARCH}" && exit 1;; \
      esac; \
      ZOXIDE_TARBALL="zoxide-${ZOXIDE_VERSION}-${ZOXIDE_ARCH}.tar.gz"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o "/tmp/${ZOXIDE_TARBALL}" \
        "https://github.com/ajeetdsouza/zoxide/releases/download/v${ZOXIDE_VERSION}/${ZOXIDE_TARBALL}"; \
      echo "${ZOXIDE_SHA256}  /tmp/${ZOXIDE_TARBALL}" | sha256sum -c -; \
      tar -xzf "/tmp/${ZOXIDE_TARBALL}" -C /usr/local/bin zoxide; \
      chmod +x /usr/local/bin/zoxide; \
      rm "/tmp/${ZOXIDE_TARBALL}"; \
      zoxide --version; \
    fi

# -- Layer 20d: precompute zsh completions (ZSH-05) ---------------------------
# Writes _<tool> files to Debian's vendor-completions dir (automatically in
# $fpath). Per R-02/R-03, tofu and gcloud are NOT listed — their OMZ plugins
# handle completion via bashcompinit / auto-source respectively.
#
# X-14: NO `|| true` guards. If a tool is installed, its `completion zsh`
# subcommand MUST succeed — a broken generator means the image ships a broken
# completion definition, and fail-fast is correct. The `if [ INSTALL_X=true ]`
# gate suppresses the call when the tool isn't installed (the common case
# where silent-skip would otherwise be tempting).
RUN set -eux; \
    if [ "${INSTALL_ZSH}" != "true" ]; then \
      echo "Skipping completion precompute (tools.zsh=false)"; \
    else \
      mkdir -p /usr/share/zsh/vendor-completions; \
      if [ "${INSTALL_KUBECTL}" = "true" ];    then kubectl completion zsh  > /usr/share/zsh/vendor-completions/_kubectl;  fi; \
      if [ "${INSTALL_HELM}" = "true" ];       then helm completion zsh     > /usr/share/zsh/vendor-completions/_helm;     fi; \
      if [ "${INSTALL_GH}" = "true" ];         then gh completion -s zsh    > /usr/share/zsh/vendor-completions/_gh;       fi; \
      if [ "${INSTALL_GLAB}" = "true" ];       then glab completion -s zsh  > /usr/share/zsh/vendor-completions/_glab;     fi; \
      if [ "${INSTALL_YQ}" = "true" ];         then yq shell-completion zsh > /usr/share/zsh/vendor-completions/_yq;       fi; \
      if [ "${INSTALL_DOCKER_CLI}" = "true" ]; then docker completion zsh   > /usr/share/zsh/vendor-completions/_docker;   fi; \
      if [ "${INSTALL_UV}" = "true" ];         then uv generate-shell-completion zsh > /usr/share/zsh/vendor-completions/_uv; fi; \
      if [ "${INSTALL_PNPM}" = "true" ];       then pnpm completion zsh     > /usr/share/zsh/vendor-completions/_pnpm;     fi; \
      if [ "${INSTALL_STARSHIP}" = "true" ];   then starship completions zsh > /usr/share/zsh/vendor-completions/_starship; fi; \
      if [ "${INSTALL_CODEX_CLI}" = "true" ];  then codex completion zsh    > /usr/share/zsh/vendor-completions/_codex;    fi; \
      ls /usr/share/zsh/vendor-completions; \
    fi

# -- Layer 20e: rtk (CLI proxy that compresses output for LLM agents) ---------
# Rust binary from rtk-ai/rtk. Wraps common dev commands (git/grep/cat/test
# runners/...) and emits token-optimized output before it hits the LLM context.
# Companion to Claude Code / Codex — `rtk init -g` writes hooks into
# ~/.claude/settings.json so Bash tool calls get auto-rewritten. The entrypoint
# runs `rtk init -g` on every shell so the hook stays wired even after a fresh
# ~/.toolbox/.claude bind-mount.
#
# Both arches' install logic lives in the `rtk-builder` stage (top of file)
# so this layer is a trivial COPY: amd64 receives the upstream MUSL binary,
# arm64 receives a binary built from source against the bookworm sysroot.
# When INSTALL_RTK=false the builder produces an empty placeholder which we
# discard here.
#
# RTK_TELEMETRY_DISABLED=1: hard kill-switch for rtk's GDPR-gated telemetry
# (https://github.com/rtk-ai/rtk/blob/main/docs/TELEMETRY.md). Telemetry is
# off by default in upstream too, but this env var blocks every code path
# regardless of consent state, so a user accidentally running
# `rtk telemetry enable` inside a toolbox shell still sends nothing.
#
# RTK_TEE=0: hard kill-switch for the tee feature, which by default writes
# failed-command stdout/stderr to ~/.local/share/rtk/tee/*.log (up to 20 files
# x 1MB). Inside a toolbox shell those failures often carry tokens or PII
# (`gh auth status`, `aws sts ...`, `curl -H Authorization:`) so persisting
# them to a host-bound directory is a leakage surface. The entrypoint also
# pre-seeds [tee] enabled = false in config.toml on first launch, but this
# env var is the primary defense because:
#   - it covers users with a stale config.toml from before the seed existed
#     (the seed is gated on file absence, the bind-mount persists old files)
#   - it survives `rtk telemetry enable/disable` rewriting the whole TOML
#     and resetting [tee] back to upstream's enabled = true default
# Source-side override is checked at src/core/tee.rs (rtk-ai/rtk v0.37.2,
# lines 152 and 200).
ENV RTK_TELEMETRY_DISABLED=1 \
    RTK_TEE=0
COPY --from=rtk-builder /out/bin/rtk /tmp/rtk-from-builder
RUN set -eux; \
    if [ "${INSTALL_RTK}" != "true" ]; then \
      echo "Skipping rtk (tools.rtk=false)"; \
      rm -f /tmp/rtk-from-builder; \
    else \
      mv /tmp/rtk-from-builder /usr/local/bin/rtk; \
      chmod +x /usr/local/bin/rtk; \
      rtk --version; \
    fi

# -- Layer 21: User setup (IMG-03) --------------------------------------------
# The container is launched with --user <host-uid>:<host-gid> (see
# internal/container/lifecycle.go) so bind-mounted host files retain correct
# ownership. The runtime uid usually won't match the "toolbox" uid baked here,
# so /home/toolbox is made writable to every uid.
#
# .cache, .config, and .local/share are pre-created here because Docker
# would otherwise create them as root:root 0755 at runtime (as the parent of
# the playwright-cache / gh / glab-cli / gcloud / rtk-data bind mounts),
# blocking any tool that writes a sibling subdir — e.g. starship under
# ~/.cache, helm under ~/.config. ~/.local/share currently has only one
# child mount (rtk-data) but is created the same way so any future XDG-
# compliant tool that lands a subdir there does not hit a root-owned parent.
#
# /etc/passwd and /etc/group are made world-writable so the entrypoint can
# inject a passwd entry for the runtime UID/GID. Without it, tools that call
# getpwuid() fail — notably ssh ("No user exists for uid NNN"), which breaks
# every git operation over ssh://. OpenShift-style fix: safe because the
# container is ephemeral and neither file carries secrets.
RUN set -eux; \
    usermod -l toolbox node; \
    groupmod -n toolbox node; \
    usermod -d /home/toolbox -m toolbox; \
    mkdir -p \
      /home/toolbox/.npm-global \
      /home/toolbox/.toolbox-startup.d \
      /home/toolbox/.cache \
      /home/toolbox/.config \
      /home/toolbox/.local/share; \
    chmod -R a+rwX /home/toolbox; \
    chmod 0666 /etc/passwd /etc/group; \
    # V8 compile cache leaked from `npm install -g`; regenerated at runtime.
    rm -rf /tmp/node-compile-cache /tmp/v8-compile-cache-*

ENV HOME=/home/toolbox \
    NPM_CONFIG_PREFIX=/home/toolbox/.npm-global \
    EDITOR=vi \
    KUBE_EDITOR=vi \
    STARSHIP_CONFIG=/etc/toolbox/starship.toml
ENV PATH="/home/toolbox/.npm-global/bin:${PATH}"

# Curated starship config (D-05). Disables ~17 modules irrelevant to the
# toolbox image (no SDKs installed) and the two slow live probes (aws/azure).
# Per-user override: set STARSHIP_CONFIG in a ~/.toolbox-startup.d/ hook.
COPY starship.toml /etc/toolbox/starship.toml

COPY bashrc.sh /etc/toolbox-bashrc.sh
RUN set -eux; \
    echo '' >> /etc/bash.bashrc; \
    echo '# toolbox shell UX (alias + completions + starship)' >> /etc/bash.bashrc; \
    echo '[ -f /etc/toolbox-bashrc.sh ] && source /etc/toolbox-bashrc.sh' \
      >> /etc/bash.bashrc

COPY entrypoint.sh /usr/local/bin/entrypoint
RUN chmod +x /usr/local/bin/entrypoint

# Per-tool init manifest. Last so init.d/* edits invalidate only this layer.
# Embedded via internal/build/embed.go for `toolbox build` w/o repo checkout.
# chmod belt-and-braces: embed.FS strips exec bits to 0444.
COPY init.d/ /usr/local/lib/toolbox/init.d/
RUN chmod -R 0755 /usr/local/lib/toolbox/init.d/

USER toolbox
WORKDIR /workspace
ENTRYPOINT ["/usr/bin/tini", "-g", "--", "/usr/local/bin/entrypoint"]
# See Layer 20a for why CMD is `sleep infinity` rather than a shell.
CMD ["sleep", "infinity"]
