# syntax=docker/dockerfile:1.24
# Toolbox image. BuildKit required. Layers ordered rare→frequent so a
# Renovate version bump invalidates only its layer + tail.

# rtk builder: amd64 uses upstream MUSL tarball, arm64 builds from source
# against bookworm sysroot. Upstream aarch64-gnu binary links GLIBC 2.39;
# bookworm ships 2.36, no aarch64-musl release. Revisit when upstream
# publishes aarch64-musl OR Azure CLI apt repo gains trixie suite.
ARG RTK_VERSION=0.42.1

FROM rust:1-slim-bookworm AS rtk-builder
# Pipes in RUN fail the build when any segment fails (hadolint DL4006).
# Per-stage: SHELL does not survive stage boundaries.
SHELL ["/bin/bash", "-o", "pipefail", "-c"]
ARG TARGETARCH
ARG RTK_VERSION
# slim rust ships no curl/ca-certs; arm64 cargo build needs build-essential
# + pkg-config (rusqlite bundled SQLite pulls in cc).
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked,id=rtk-apt-cache \
    --mount=type=cache,target=/var/lib/apt/lists,sharing=locked,id=rtk-apt-lists \
    --mount=type=cache,target=/usr/local/cargo/registry,id=rtk-cargo-registry \
    --mount=type=cache,target=/usr/local/cargo/git,id=rtk-cargo-git \
    set -eux; \
    mkdir -p /out/bin; \
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
# Pipes in RUN fail the build when any segment fails (hadolint DL4006).
SHELL ["/bin/bash", "-o", "pipefail", "-c"]

# Version pins. Renovate-bumped; SHA256s fetched at build time below.
ARG TARGETARCH
ARG DEBIAN_FRONTEND=noninteractive
ARG GH_VERSION=2.93.0
ARG GLAB_VERSION=1.101.0
ARG GWS_VERSION=0.22.5
ARG ATUIN_VERSION=18.16.1
ARG KUBECTL_VERSION=v1.36.1
ARG KUBECTX_VERSION=0.11.0
ARG HELM_VERSION=v4.2.0
ARG TOFU_VERSION=1.12.1
ARG DOCKER_CLI_VERSION=29.5.3
ARG JQ_VERSION=1.8.1
ARG YQ_VERSION=4.53.2
ARG STARSHIP_VERSION=1.25.1
ARG PNPM_VERSION=11.5.1
ARG BUN_VERSION=1.3.14
ARG UV_VERSION=0.11.19
ARG CLAUDE_CODE_VERSION=2.1.163
ARG CODEX_CLI_VERSION=0.137.0
ARG CF_CLI_VERSION=0.0.6
ARG WRANGLER_VERSION=4.98.0
ARG PYRIGHT_VERSION=1.1.410
# RTK_VERSION declared at top (rtk-builder stage only).
ARG GCLOUD_VERSION=571.0.0
ARG AZURE_CLI_VERSION=2.87.0
ARG OCI_VERSION=3.85.0
ARG GRAPHIFY_VERSION=0.8.31
ARG PLAYWRIGHT_VERSION=1.60.0
ARG PLAYWRIGHT_CLI_VERSION=0.1.13
ARG COMPOSE_VERSION=5.1.4
# Homebrew tags carry no `v` prefix — bare version.
ARG HOMEBREW_VERSION=5.1.15
ARG GO_VERSION=1.26.4
ARG GOPLS_VERSION=0.22.0
ARG GOIMPORTS_VERSION=0.45.0
ARG TINI_VERSION=0.19.0
ARG BAT_VERSION=0.26.1
# bat: no per-release checksum file upstream; SHA256 pinned per-arch + bumped
# by maintainer in sync with BAT_VERSION (Renovate). T-01-08-class accepted risk.
ARG BAT_SHA256_AMD64=726f04c8f576a7fd18b7634f1bbf2f915c43494c1c0f013baa3287edb0d5a2a3
ARG BAT_SHA256_ARM64=422eb73e11c854fddd99f5ca8461c2f1d6e6dce0a2a8c3d5daade5ffcb6564aa
ARG FD_VERSION=10.4.2
# fd: same accepted-risk pattern as bat — no upstream checksum file; pin SHA256
# per-arch + bump in sync with FD_VERSION (Renovate).
ARG FD_SHA256_AMD64=e3257d48e29a6be965187dbd24ce9af564e0fe67b3e73c9bdcd180f4ec11bdde
ARG FD_SHA256_ARM64=f32d3657473fba74e2600babc8db0b93420d51169223b7e8143b2ed55d8fd9e8
ARG EZA_VERSION=0.23.4
# eza: no upstream checksum file. Upstream ships aarch64-MUSL only as zip; we
# fall back to aarch64-GNU on arm64 (built on ubuntu-22.04 GLIBC 2.35, compatible
# with bookworm 2.36). amd64 stays MUSL. Completions tarball is arch-agnostic,
# so a third pinned SHA256 covers it.
ARG EZA_SHA256_AMD64=d231bb3ee33b08c76279b5888845dceb7034d055c42bb9be46dbe0dae39394df
ARG EZA_SHA256_ARM64=366e8430225f9955c3dc659b452150c169894833ccfef455e01765e265a3edda
ARG EZA_COMPLETIONS_SHA256=960c6564e5662880b49919525a6e357e9325807733467e65a95aefcee0910b07

# OMZ + plugins pinned by full 40-char SHA (abbreviated breaks `git fetch
# --depth=1`). Renovate keeps each at upstream HEAD.
ARG OMZ_COMMIT=70ad5e3df8f7bed68aa6672029496926e632aedd
ARG ZSH_AUTOSUGGESTIONS_COMMIT=85919cd1ffa7d2d5412f6d3fe437ebdbeeec4fc5
ARG ZSH_SYNTAX_HIGHLIGHTING_COMMIT=1d85c692615a25fe2293bdd44b34c217d5d2bf04
ARG ZSH_COMPLETIONS_COMMIT=dd83145816fe2d90b1ab4154ed528050e94ac5e3
ARG FZF_TAB_COMMIT=24105b15714bfec37989ed5c5b6e60f572253019
ARG ZOXIDE_VERSION=0.9.9
# zoxide: no upstream sha256sums file. Same T-01-08-class accepted risk as bat.
ARG ZOXIDE_SHA256_AMD64=4ff057d3c4d957946937274c2b8be7af2a9bbae7f90a1b5e9baaa7cb65a20caa
ARG ZOXIDE_SHA256_ARM64=96e6ea2e47a71db42cb7ad5a36e9209c8cb3708f8ae00f6945573d0d93315cb0

# consoledonottrack.com convention — honored by bun, playwright, etc.
ENV DO_NOT_TRACK=1

# Base apt deps. `rm -f docker-clean` keeps BuildKit cache mount populated.
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked,id=apt-cache \
    --mount=type=cache,target=/var/lib/apt/lists,sharing=locked,id=apt-lists \
    set -eux; \
    rm -f /etc/apt/apt.conf.d/docker-clean; \
    apt-get update -qqy; \
    apt-get install -qqy --no-install-recommends \
      ca-certificates \
      curl \
      file \
      git \
      gnupg \
      less \
      libnss3-tools \
      libpython3.11-minimal \
      libpython3.11-stdlib \
      make \
      ncurses-bin \
      openssh-client \
      procps \
      python-is-python3 \
      python3 \
      python3-pip \
      ripgrep \
      socat \
      sudo \
      tree \
      unzip \
      vim-tiny \
      wget

# kubectl
RUN set -eux; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -O "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${TARGETARCH}/kubectl"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o kubectl.sha256 \
        "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${TARGETARCH}/kubectl.sha256"; \
      echo "$(cat kubectl.sha256)  kubectl" | sha256sum -c -; \
      chmod +x kubectl; \
      mv kubectl /usr/local/bin/kubectl; \
      rm kubectl.sha256; \
      kubectl version --client;
# kubectx + kubens (kubectl context/namespace switchers). Linux assets use
# GNU arch suffix (x86_64) — translate from Docker TARGETARCH.
RUN set -eux; \
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
      # Zsh completions live in the repo, NOT the tarball; binary has no
      # `completion zsh` subcommand. Fetch per-tag raw files. T-01-08-class
      # risk (HTTPS + tag pin, no per-file checksum upstream).
      mkdir -p /usr/share/zsh/vendor-completions; \
      for tool in kubectx kubens; do \
        curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 \
          -o "/usr/share/zsh/vendor-completions/_${tool}" \
          "https://raw.githubusercontent.com/ahmetb/kubectx/v${KUBECTX_VERSION}/completion/_${tool}.zsh"; \
      done; \
      kubectx --version; \
      kubens --version;
# helm
RUN set -eux; \
      HELM_TARBALL="helm-${HELM_VERSION}-linux-${TARGETARCH}.tar.gz"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o "/tmp/${HELM_TARBALL}" \
        "https://get.helm.sh/${HELM_TARBALL}"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /tmp/helm.sha256 \
        "https://get.helm.sh/${HELM_TARBALL}.sha256sum"; \
      cd /tmp && grep "${HELM_TARBALL}" helm.sha256 | sha256sum -c -; \
      tar -xzf "/tmp/${HELM_TARBALL}" -C /usr/local/bin "linux-${TARGETARCH}/helm" --strip-components=1; \
      rm "/tmp/${HELM_TARBALL}" /tmp/helm.sha256; \
      helm version;
# tofu. CHECKPOINT_DISABLE=1: opt out of HashiCorp Checkpoint pings
# (inherited from Terraform).
ENV CHECKPOINT_DISABLE=1
RUN set -eux; \
      TOFU_TARBALL="tofu_${TOFU_VERSION}_linux_${TARGETARCH}.tar.gz"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o "/tmp/${TOFU_TARBALL}" \
        "https://github.com/opentofu/opentofu/releases/download/v${TOFU_VERSION}/${TOFU_TARBALL}"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /tmp/tofu_SHA256SUMS \
        "https://github.com/opentofu/opentofu/releases/download/v${TOFU_VERSION}/tofu_${TOFU_VERSION}_SHA256SUMS"; \
      cd /tmp && grep "${TOFU_TARBALL}" tofu_SHA256SUMS | sha256sum -c -; \
      tar -xzf "/tmp/${TOFU_TARBALL}" -C /usr/local/bin tofu; \
      rm "/tmp/${TOFU_TARBALL}" /tmp/tofu_SHA256SUMS; \
      tofu version;
# gh
RUN set -eux; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /tmp/gh.tar.gz \
        "https://github.com/cli/cli/releases/download/v${GH_VERSION}/gh_${GH_VERSION}_linux_${TARGETARCH}.tar.gz"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /tmp/gh_checksums.txt \
        "https://github.com/cli/cli/releases/download/v${GH_VERSION}/gh_${GH_VERSION}_checksums.txt"; \
      grep "gh_${GH_VERSION}_linux_${TARGETARCH}.tar.gz" /tmp/gh_checksums.txt \
        | awk '{print $1"  /tmp/gh.tar.gz"}' | sha256sum -c -; \
      tar -xzf /tmp/gh.tar.gz -C /usr/local --strip-components=1 "gh_${GH_VERSION}_linux_${TARGETARCH}/bin/gh"; \
      rm /tmp/gh.tar.gz /tmp/gh_checksums.txt; \
      gh --version;
# glab
RUN set -eux; \
      GLAB_TARBALL="glab_${GLAB_VERSION}_linux_${TARGETARCH}.tar.gz"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o "/tmp/${GLAB_TARBALL}" \
        "https://gitlab.com/api/v4/projects/gitlab-org%2Fcli/packages/generic/glab/${GLAB_VERSION}/${GLAB_TARBALL}"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /tmp/glab_checksums.txt \
        "https://gitlab.com/api/v4/projects/gitlab-org%2Fcli/packages/generic/glab/${GLAB_VERSION}/checksums.txt"; \
      cd /tmp && grep "${GLAB_TARBALL}" glab_checksums.txt | sha256sum -c -; \
      tar -xzf "/tmp/${GLAB_TARBALL}" -C /usr/local/bin bin/glab --strip-components=1; \
      rm "/tmp/${GLAB_TARBALL}" /tmp/glab_checksums.txt; \
      glab version;
# gws (Google Workspace CLI). MUSL variant — upstream `-gnu` targets
# GLIBC 2.39, bookworm ships 2.36. `KEYRING_BACKEND=file` writes auth key
# to ~/.config/gws/.encryption_key (no D-Bus inside container).
ENV GOOGLE_WORKSPACE_CLI_KEYRING_BACKEND=file
RUN set -eux; \
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
      gws --version;
# atuin (SQLite-backed Ctrl-R). MUSL — same GLIBC mismatch as gws.
# ATUIN_CONFIG_DIR consolidates config + DB under one bind-mount.
ENV ATUIN_CONFIG_DIR=/home/toolbox/.local/share/atuin/config
RUN set -eux; \
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
      atuin --version;
# Docker CLI standalone. No upstream .sha256 — T-01-08 accepted risk
# (HTTPS + version pin only).
RUN set -eux; \
      case "${TARGETARCH}" in amd64) DOCKER_ARCH=x86_64;; arm64) DOCKER_ARCH=aarch64;; *) DOCKER_ARCH=${TARGETARCH};; esac; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /tmp/docker.tgz \
        "https://download.docker.com/linux/static/stable/${DOCKER_ARCH}/docker-${DOCKER_CLI_VERSION}.tgz"; \
      tar -xzf /tmp/docker.tgz -C /usr/local/bin --strip-components=1 docker/docker; \
      rm /tmp/docker.tgz; \
      docker --version;
# starship
RUN set -eux; \
      case "${TARGETARCH}" in \
        amd64) STARSHIP_TARBALL="starship-x86_64-unknown-linux-gnu.tar.gz";; \
        arm64) STARSHIP_TARBALL="starship-aarch64-unknown-linux-musl.tar.gz";; \
        *) echo "Unsupported arch: ${TARGETARCH}" && exit 1;; \
      esac; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /tmp/starship.tar.gz \
        "https://github.com/starship/starship/releases/download/v${STARSHIP_VERSION}/${STARSHIP_TARBALL}"; \
      tar -xzf /tmp/starship.tar.gz -C /usr/local/bin starship; \
      rm /tmp/starship.tar.gz; \
      starship --version;
# jq
RUN set -eux; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /usr/local/bin/jq \
        "https://github.com/jqlang/jq/releases/download/jq-${JQ_VERSION}/jq-linux-${TARGETARCH}"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /tmp/jq_sha256 \
        "https://github.com/jqlang/jq/releases/download/jq-${JQ_VERSION}/sha256sum.txt"; \
      grep "jq-linux-${TARGETARCH}$" /tmp/jq_sha256 \
        | awk '{print $1"  /usr/local/bin/jq"}' | sha256sum -c -; \
      chmod +x /usr/local/bin/jq; \
      rm /tmp/jq_sha256; \
      jq --version;
# yq. Checksums file has 31 hash algorithms per row; SHA-256 is column $19.
RUN set -eux; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /usr/local/bin/yq \
        "https://github.com/mikefarah/yq/releases/download/v${YQ_VERSION}/yq_linux_${TARGETARCH}"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /tmp/yq_checksums \
        "https://github.com/mikefarah/yq/releases/download/v${YQ_VERSION}/checksums"; \
      EXPECTED=$(grep "^yq_linux_${TARGETARCH} " /tmp/yq_checksums | awk '{print $19}'); \
      test -n "${EXPECTED}" || { echo "ERROR: SHA-256 hash not found in checksums file"; exit 1; }; \
      echo "${EXPECTED}  /usr/local/bin/yq" | sha256sum -c -; \
      chmod +x /usr/local/bin/yq; \
      rm /tmp/yq_checksums; \
      yq --version;
# bat (pretty cat). SHA256 pinned per-arch (no upstream checksum file).
RUN set -eux; \
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
      # bat has no `completion zsh` subcommand — ship the upstream static file.
      mkdir -p /usr/share/zsh/vendor-completions; \
      tar -xzf "/tmp/${BAT_TARBALL}" -C /tmp --strip-components=2 \
        "bat-v${BAT_VERSION}-${BAT_ARCH}/autocomplete/bat.zsh"; \
      mv /tmp/bat.zsh /usr/share/zsh/vendor-completions/_bat; \
      rm "/tmp/${BAT_TARBALL}"; \
      bat --version;
# fd (find replacement). MUSL both arches. SHA256 pinned per-arch (no
# upstream checksum file — same accepted-risk pattern as bat/zoxide).
RUN set -eux; \
      case "${TARGETARCH}" in \
        amd64) FD_ARCH=x86_64-unknown-linux-musl;  FD_SHA256="${FD_SHA256_AMD64}";; \
        arm64) FD_ARCH=aarch64-unknown-linux-musl; FD_SHA256="${FD_SHA256_ARM64}";; \
        *) echo "Unsupported arch: ${TARGETARCH}" && exit 1;; \
      esac; \
      FD_TARBALL="fd-v${FD_VERSION}-${FD_ARCH}.tar.gz"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o "/tmp/${FD_TARBALL}" \
        "https://github.com/sharkdp/fd/releases/download/v${FD_VERSION}/${FD_TARBALL}"; \
      echo "${FD_SHA256}  /tmp/${FD_TARBALL}" | sha256sum -c -; \
      tar -xzf "/tmp/${FD_TARBALL}" -C /usr/local/bin --strip-components=1 \
        "fd-v${FD_VERSION}-${FD_ARCH}/fd"; \
      chmod +x /usr/local/bin/fd; \
      # fd ships a pre-generated _fd zsh completion in the tarball — same
      # convention as bat. No `completion zsh` subcommand on fd itself.
      mkdir -p /usr/share/zsh/vendor-completions; \
      tar -xzf "/tmp/${FD_TARBALL}" -C /tmp --strip-components=2 \
        "fd-v${FD_VERSION}-${FD_ARCH}/autocomplete/_fd"; \
      mv /tmp/_fd /usr/share/zsh/vendor-completions/_fd; \
      rm "/tmp/${FD_TARBALL}"; \
      fd --version;
# eza (ls replacement). amd64 MUSL, arm64 GNU (no upstream aarch64-musl tar).
# Completions ship in a separate arch-independent tarball; extract `_eza` to
# vendor-completions in the same RUN so a Renovate bump of EZA_VERSION
# invalidates both layers atomically.
RUN set -eux; \
      case "${TARGETARCH}" in \
        amd64) EZA_ARCH=x86_64-unknown-linux-musl;  EZA_SHA256="${EZA_SHA256_AMD64}";; \
        arm64) EZA_ARCH=aarch64-unknown-linux-gnu;  EZA_SHA256="${EZA_SHA256_ARM64}";; \
        *) echo "Unsupported arch: ${TARGETARCH}" && exit 1;; \
      esac; \
      EZA_TARBALL="eza_${EZA_ARCH}.tar.gz"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o "/tmp/${EZA_TARBALL}" \
        "https://github.com/eza-community/eza/releases/download/v${EZA_VERSION}/${EZA_TARBALL}"; \
      echo "${EZA_SHA256}  /tmp/${EZA_TARBALL}" | sha256sum -c -; \
      tar -xzf "/tmp/${EZA_TARBALL}" -C /usr/local/bin ./eza; \
      chmod +x /usr/local/bin/eza; \
      rm "/tmp/${EZA_TARBALL}"; \
      EZA_COMPLETIONS_TARBALL="completions-${EZA_VERSION}.tar.gz"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o "/tmp/${EZA_COMPLETIONS_TARBALL}" \
        "https://github.com/eza-community/eza/releases/download/v${EZA_VERSION}/${EZA_COMPLETIONS_TARBALL}"; \
      echo "${EZA_COMPLETIONS_SHA256}  /tmp/${EZA_COMPLETIONS_TARBALL}" | sha256sum -c -; \
      mkdir -p /usr/share/zsh/vendor-completions; \
      tar -xzf "/tmp/${EZA_COMPLETIONS_TARBALL}" -C /tmp \
        "./target/completions-${EZA_VERSION}/_eza"; \
      mv "/tmp/target/completions-${EZA_VERSION}/_eza" /usr/share/zsh/vendor-completions/_eza; \
      rm -rf "/tmp/${EZA_COMPLETIONS_TARBALL}" /tmp/target; \
      eza --version;
# pnpm via corepack
RUN set -eux; \
      corepack enable; \
      corepack prepare pnpm@${PNPM_VERSION} --activate; \
      pnpm --version;
# bun. GLIBC variant (NOT -musl) — bun's musl build is dynamically linked
# against /lib/ld-musl-x86_64.so.1, which Debian doesn't ship → ENOENT.
# Bun's GLIBC build targets ≥ 2.27, well below bookworm's 2.36. Opposite
# rationale vs gws/atuin/zoxide. ~/.bun bound via mountplan defaults.
RUN set -eux; \
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
      bun --version;
# Per-user bun global bin in PATH.
ENV PATH="/home/toolbox/.bun/bin:${PATH}"

# uv (Python package manager). Pinned tarball install, SHA256 from upstream.
RUN set -eux; \
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
      uv --version;
# Claude Code env knobs:
#   DISABLE_AUTOUPDATER: CLI binary lives under root-owned /usr/local/lib —
#     self-update would always fail. Renovate drives version bumps instead.
#   FORCE_AUTOUPDATE_PLUGINS: plugins live in user-writable ~/.claude/plugins
#     bind — update path works there, keep it on.
#   DISABLE_TELEMETRY/FEEDBACK/ERROR_REPORTING: granular privacy switches.
#     CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC umbrella correlates with
#     intermittent /login prompts on long-lived shells — use granular set
#     instead. If logout regression: drop DISABLE_TELEMETRY first.
#   CLAUDE_CONFIG_DIR: relocate .credentials.json + .claude.json into the
#     ~/.claude bind so OAuth survives container recreates.
#   NPM_CONFIG_FUND/AUDIT: suppress banner + audit on every npm install -g.
ENV NPM_CONFIG_FUND=false \
    NPM_CONFIG_AUDIT=false \
    DISABLE_AUTOUPDATER=1 \
    FORCE_AUTOUPDATE_PLUGINS=1 \
    DISABLE_TELEMETRY=1 \
    DISABLE_FEEDBACK_COMMAND=1 \
    DISABLE_ERROR_REPORTING=1 \
    CLAUDE_CONFIG_DIR=/home/toolbox/.claude

# xdg-open no-op shim. cf/wrangler/any OAuth CLI shells out to xdg-open;
# container has no browser. URL is printed to stdout, user copies to host.
RUN printf '#!/bin/sh\nexit 0\n' > /usr/local/bin/xdg-open && \
    chmod +x /usr/local/bin/xdg-open

RUN --mount=type=cache,target=/root/.npm,id=npm-cache \
    set -eux; \
      npm install -g @anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}; \
      claude --version; \
      find /usr/local/lib/node_modules/@anthropic-ai \
        \( -type f \( -name '*.map' -o -name '*.md' -o -name '*.markdown' -o -name 'CHANGELOG*' -o -name 'AUTHORS*' \) \
        -o -type d \( -name docs -o -name examples -o -name example -o -name __tests__ -o -name .github \) \) \
        -prune -exec rm -rf {} +;
# Pyright language server (Python LSP for Claude Code plugin).
RUN --mount=type=cache,target=/root/.npm,id=npm-cache \
    set -eux; \
      npm install -g pyright@${PYRIGHT_VERSION}; \
      pyright --version; \
      find /usr/local/lib/node_modules/pyright \
        \( -type f \( -name '*.map' -o -name '*.md' -o -name '*.markdown' -o -name 'CHANGELOG*' -o -name 'AUTHORS*' \) \
        -o -type d \( -name docs -o -name examples -o -name example -o -name __tests__ -o -name .github \) \) \
        -prune -exec rm -rf {} +;
# OpenAI Codex CLI. bubblewrap required for its in-tool sandbox.
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked,id=apt-cache \
    --mount=type=cache,target=/var/lib/apt/lists,sharing=locked,id=apt-lists \
    --mount=type=cache,target=/root/.npm,id=npm-cache \
    set -eux; \
      apt-get update -qqy; \
      apt-get install -qqy --no-install-recommends bubblewrap; \
      npm install -g "@openai/codex@${CODEX_CLI_VERSION}"; \
      codex --version; \
      find /usr/local/lib/node_modules/@openai \
        \( -type f \( -name '*.map' -o -name '*.md' -o -name '*.markdown' -o -name 'CHANGELOG*' -o -name 'AUTHORS*' \) \
        -o -type d \( -name docs -o -name examples -o -name example -o -name __tests__ -o -name .github \) \) \
        -prune -exec rm -rf {} +;
# Cloudflare `cf` (Wrangler vNext preview). State split: ~/.cf/config.toml
# (auth) + ~/.config/cf/config.json (context defaults) — both bound under
# ~/.toolbox/cf/ via mountplan defaults.
RUN --mount=type=cache,target=/root/.npm,id=npm-cache \
    set -eux; \
      npm install -g "cf@${CF_CLI_VERSION}"; \
      cf --version; \
      # Dynamic-port carve-out: cf picks OAuth callback port at runtime from \
      # startPort:8877, so `toolbox -B` bridge cannot pre-bind. Rewrite \
      # bindHost 127.0.0.1 → 0.0.0.0 so cf binds eth0 and host -p forward \
      # lands directly. cf 0.0.6+ splits sources into hash-named dist/*.mjs \
      # chunks (was cf-dist/index.js) and quote style drifts (backtick now, \
      # was double-quote) — locate the chunk dynamically and accept any of \
      # backtick/double/single as the literal delimiter. Empty match or \
      # missing post-sed literal fails the build loud. \
      CF_AUTH_MJS=$(grep -lE 'bindHost:[[:space:]]*[`"'\'']127\.0\.0\.1[`"'\'']' \
        /usr/local/lib/node_modules/cf/dist/*.mjs); \
      [ -n "$CF_AUTH_MJS" ]; \
      sed -i -E 's/(bindHost:[[:space:]]*)[`"'\'']127\.0\.0\.1[`"'\'']/\1`0.0.0.0`/' $CF_AUTH_MJS; \
      grep -qE 'bindHost:[[:space:]]*`0\.0\.0\.0`' $CF_AUTH_MJS; \
      find /usr/local/lib/node_modules/cf \
        \( -type f \( -name '*.map' -o -name '*.md' -o -name '*.markdown' -o -name 'CHANGELOG*' -o -name 'AUTHORS*' \) \
        -o -type d \( -name docs -o -name examples -o -name example -o -name __tests__ -o -name .github \) \) \
        -prune -exec rm -rf {} +;
# Cloudflare Wrangler (primary Workers CLI). State under
# ~/.config/.wrangler/ via xdg-app-paths(".wrangler"). OAuth callback is
# static localhost:8976 — use `toolbox shell -B -p 8976:8976`.
ENV WRANGLER_SEND_METRICS=false
RUN --mount=type=cache,target=/root/.npm,id=npm-cache \
    set -eux; \
      npm install -g "wrangler@${WRANGLER_VERSION}"; \
      wrangler --version; \
      find /usr/local/lib/node_modules/wrangler \
        \( -type f \( -name '*.map' -o -name '*.md' -o -name '*.markdown' -o -name 'CHANGELOG*' -o -name 'AUTHORS*' \) \
        -o -type d \( -name docs -o -name examples -o -name example -o -name __tests__ -o -name .github \) \) \
        -prune -exec rm -rf {} +;
# gcloud. No per-version SHA256 file upstream — T-01-08 accepted risk.
# gcloud env knobs:
#   - CLOUDSDK_CORE_DISABLE_USAGE_REPORTING: opt out of gcloud usage telemetry.
#   - CLOUDSDK_COMPONENT_MANAGER_DISABLE_UPDATE_CHECK: suppress the
#     "Updates are available for some Google Cloud CLI components" nag that
#     fires on every gcloud call. Component upgrades inside the container
#     wouldn't persist past `toolbox stop` anyway — the canonical upgrade
#     path is bumping GCLOUD_VERSION above (Renovate-managed).
#   - CLOUDSDK_SURVEY_PROMPT_DISABLE: suppress the "take a quick anonymous
#     survey" nag printed at the tail of authenticated commands.
ENV CLOUDSDK_CORE_DISABLE_USAGE_REPORTING=true \
    CLOUDSDK_COMPONENT_MANAGER_DISABLE_UPDATE_CHECK=1 \
    CLOUDSDK_SURVEY_PROMPT_DISABLE=1
RUN set -eux; \
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
      # Size trim. Keeps gcloud/gsutil/bq/gke-gcloud-auth-plugin functional.
      find /opt/google-cloud-sdk -type d -name __pycache__ -prune -exec rm -rf {} + 2>/dev/null || true; \
      find /opt/google-cloud-sdk -type f -name '*.pyc' -delete 2>/dev/null || true; \
      find /opt/google-cloud-sdk -type d \( -name tests -o -name test \) -prune -exec rm -rf {} + 2>/dev/null || true; \
      find /opt/google-cloud-sdk -type f \( -name '*.md' -o -name '*.markdown' -o -name 'CHANGELOG*' -o -name 'README*' \) -delete 2>/dev/null || true; \
      rm -rf /opt/google-cloud-sdk/platform/bundledpythonunix 2>/dev/null || true; \
      rm -rf /opt/google-cloud-sdk/platform/anthoscli 2>/dev/null || true;
# Go toolchain + gopls + goimports.
#   -ldflags="-s -w" strips debug + symtab (~30% smaller).
#   HOME=/tmp/go-install scoped to this RUN; global HOME stays intact.
#   SHA256: go.dev/dl/<file>.sha256 redirects to HTML — pull from JSON index.
RUN set -eux; \
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
      # gopls + goimports live in different modules — `go install` invoked twice.
        HOME=/tmp/go-install \
        GOPATH=/tmp/go-install \
        GOCACHE=/tmp/go-install/cache \
        GOMODCACHE=/tmp/go-install/pkg/mod \
        GOBIN=/usr/local/bin \
        /usr/local/go/bin/go install -ldflags="-s -w" \
          "golang.org/x/tools/gopls@v${GOPLS_VERSION}"; \
        gopls version; \
        HOME=/tmp/go-install \
        GOPATH=/tmp/go-install \
        GOCACHE=/tmp/go-install/cache \
        GOMODCACHE=/tmp/go-install/pkg/mod \
        GOBIN=/usr/local/bin \
        /usr/local/go/bin/go install -ldflags="-s -w" \
          "golang.org/x/tools/cmd/goimports@v${GOIMPORTS_VERSION}"; \
        # PATH for /usr/local/go/bin is set by the ENV after this RUN, so
        # functional goimports invocation here would fail — presence check
        # only. smoke-test.sh runs the functional check.
        test -x /usr/local/bin/goimports; \
      rm -rf /tmp/go-install; \
      rm -rf /usr/local/go/test /usr/local/go/api /usr/local/go/doc /usr/local/go/misc; \
      find /usr/local/go/src -name '*_test.go' -delete; \
      find /usr/local/go/src -type d -name testdata -prune -exec rm -rf {} +; \
      /usr/local/go/bin/go version;
ENV PATH="/usr/local/go/bin:${PATH}"

# Azure CLI from MS apt repo. AZURE_CORE_COLLECT_TELEMETRY=false also stops
# the telemetry subprocess that races with __pycache__ cleanup on qemu arm64.
ENV AZURE_CORE_COLLECT_TELEMETRY=false
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked,id=apt-cache \
    --mount=type=cache,target=/var/lib/apt/lists,sharing=locked,id=apt-lists \
    set -eux; \
      install -d -m 0755 /etc/apt/keyrings; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 https://packages.microsoft.com/keys/microsoft.asc \
        | gpg --dearmor -o /etc/apt/keyrings/microsoft.gpg; \
      echo "deb [arch=${TARGETARCH} signed-by=/etc/apt/keyrings/microsoft.gpg] https://packages.microsoft.com/repos/azure-cli/ bookworm main" \
        > /etc/apt/sources.list.d/azure-cli.list; \
      apt-get update -qqy; \
      apt-get install -qqy --no-install-recommends "azure-cli=${AZURE_CLI_VERSION}-1~bookworm"; \
      az --version; \
      find /opt/az -type d -name __pycache__ -prune -exec rm -rf {} + 2>/dev/null || true; \
      # Strip headless-useless stdlib (IDE, turtle, GUI, pydoc).
      for d in idlelib turtledemo tkinter pydoc_data; do \
        rm -rf /opt/az/lib/python*/$d; \
      done;
# OCI CLI via pip. No upstream binary release; --break-system-packages
# accepted for this single-purpose image (PEP 668).
RUN --mount=type=cache,target=/root/.cache/pip,id=pip-cache \
    set -eux; \
      pip install --break-system-packages \
        --timeout 300 --retries 5 \
        "oci-cli==${OCI_VERSION}"; \
      oci --version; \
      find /usr/local/lib/python* -type d -name __pycache__ -prune -exec rm -rf {} + 2>/dev/null || true;
# graphify (knowledge-graph builder, Claude Code skill). PyPI name is
# `graphifyy` (double y); binary stays `graphify`.
# `tree-sitter-dm` (transitive dep) ships only a win_amd64 wheel on PyPI, so
# pip builds it from sdist on Linux — gcc + python3-dev installed transiently
# and purged after to keep the final image lean.
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked,id=apt-cache \
    --mount=type=cache,target=/var/lib/apt/lists,sharing=locked,id=apt-lists \
    --mount=type=cache,target=/root/.cache/pip,id=pip-cache \
    set -eux; \
      apt-get update -qqy; \
      apt-get install -y --no-install-recommends gcc python3-dev; \
      pip install --break-system-packages \
        --timeout 300 --retries 5 \
        "graphifyy==${GRAPHIFY_VERSION}"; \
      apt-get purge -y --auto-remove gcc python3-dev; \
      # No `--version` subcommand; `--help` is the smoke check.
      graphify --help >/dev/null; \
      pip show graphifyy | grep ^Version:; \
      find /usr/local/lib/python* -type d -name __pycache__ -prune -exec rm -rf {} + 2>/dev/null || true;
# Playwright + Chromium apt deps (root-only, must happen at build). Firefox/
# WebKit deps skipped; opt in at runtime. Browsers fetched on-demand into
# ~/.toolbox/playwright-cache bind.
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked,id=apt-cache \
    --mount=type=cache,target=/var/lib/apt/lists,sharing=locked,id=apt-lists \
    --mount=type=cache,target=/root/.npm,id=npm-cache \
    set -eux; \
      npm install -g "playwright@${PLAYWRIGHT_VERSION}"; \
      playwright --version; \
      apt-get update -qqy; \
      playwright install-deps chromium; \
      find /usr/local/lib/node_modules/playwright \
        \( -type f \( -name '*.map' -o -name '*.md' -o -name '*.markdown' -o -name 'CHANGELOG*' -o -name 'AUTHORS*' \) \
        -o -type d \( -name docs -o -name examples -o -name example -o -name __tests__ -o -name .github \) \) \
        -prune -exec rm -rf {} +;
# playwright-cli (agent companion). Skills installed at runtime via
# `playwright-cli install --skills`. Binary name has `-cli` suffix.
RUN --mount=type=cache,target=/root/.npm,id=npm-cache \
    set -eux; \
      npm install -g "@playwright/cli@${PLAYWRIGHT_CLI_VERSION}"; \
      playwright-cli --version; \
      find /usr/local/lib/node_modules/@playwright \
        \( -type f \( -name '*.map' -o -name '*.md' -o -name '*.markdown' -o -name 'CHANGELOG*' -o -name 'AUTHORS*' \) \
        -o -type d \( -name docs -o -name examples -o -name example -o -name __tests__ -o -name .github \) \) \
        -prune -exec rm -rf {} +;
# Docker Compose plugin for the static docker CLI.
RUN set -eux; \
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
      /usr/local/libexec/docker/cli-plugins/docker-compose version;
# tini PID-1. CMD is `sleep infinity` (not a shell) — interactive shells
# ignore SIGTERM when owning the TTY, blocking `docker stop` until SIGKILL.
# tini -g forwards signals to the whole process group.
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

# zsh + Oh-My-Zsh + 4 custom plugins
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
      apt-get update -qqy; \
      # node:24-bookworm-slim ships /etc/dpkg/dpkg.cfg.d/docker-clean (or
      # similar) with `path-exclude /usr/share/doc/*`, which silently strips
      # fzf's key-bindings.zsh + completion.zsh integration scripts at install
      # time. Without re-including them, our zshrc `source
      # /usr/share/doc/fzf/examples/*` lines no-op — fzf works as a binary
      # but Ctrl-R / Ctrl-T / Alt-C bindings stay dormant. The override
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
      fzf --version;
COPY zshrc.sh /etc/toolbox-zshrc.sh
RUN set -eux; \
    mv /etc/toolbox-zshrc.sh /etc/zsh/zshrc
# zoxide. SHA256 from build-arg literal (no upstream checksum file). MUSL
# both arches (upstream only ships musl for arm64).
RUN set -eux; \
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
      zoxide --version;
# Precompute zsh completions to vendor-completions (Debian fpath default).
# tofu/gcloud excluded — their OMZ plugins handle completion separately.
# Fail-fast: a broken `completion zsh` subcommand fails the build.
RUN set -eux; \
      mkdir -p /usr/share/zsh/vendor-completions; \
      kubectl completion zsh  > /usr/share/zsh/vendor-completions/_kubectl; \
      helm completion zsh     > /usr/share/zsh/vendor-completions/_helm; \
      gh completion -s zsh    > /usr/share/zsh/vendor-completions/_gh; \
      glab completion -s zsh  > /usr/share/zsh/vendor-completions/_glab; \
      yq shell-completion zsh > /usr/share/zsh/vendor-completions/_yq; \
      docker completion zsh   > /usr/share/zsh/vendor-completions/_docker; \
      uv generate-shell-completion zsh > /usr/share/zsh/vendor-completions/_uv; \
      pnpm completion zsh     > /usr/share/zsh/vendor-completions/_pnpm; \
      starship completions zsh > /usr/share/zsh/vendor-completions/_starship; \
      codex completion zsh    > /usr/share/zsh/vendor-completions/_codex; \
      # cf 0.0.6 dropped the `completions <shell>` subcommand. No replacement
      # yet (yargs default `completion` is not registered either); revisit on
      # the next Renovate bump and reinstate _cf when upstream restores it.
      ls /usr/share/zsh/vendor-completions;
# rtk (Rust CLI proxy). Binary built in rtk-builder stage above.
# RTK_TELEMETRY_DISABLED=1: hard kill-switch surviving any `rtk telemetry
# enable` rewrite of config.toml.
# RTK_TEE=0: hard kill-switch for tee feature — writes failed-command
# stdout/stderr (often carrying tokens) to ~/.local/share/rtk/tee/*.log.
# Source-side override checked at src/core/tee.rs (v0.37.2 lines 152, 200).
ENV RTK_TELEMETRY_DISABLED=1 \
    RTK_TEE=0
COPY --from=rtk-builder /out/bin/rtk /tmp/rtk-from-builder
RUN set -eux; \
      mv /tmp/rtk-from-builder /usr/local/bin/rtk; \
      chmod +x /usr/local/bin/rtk; \
      rtk --version;
# Homebrew (Linuxbrew). Shallow tag clone at the DEFAULT prefix — bottles
# only work at /home/linuxbrew/.linuxbrew; the official installer is unusable
# in a RUN (refuses root, clones unpinned `main`). Layout mirrors what the
# installer produces: repo at …/Homebrew + bin/brew symlink. Pre-built _brew
# zsh completion ships from the clone (bat/fd/eza precedent). Variable host
# UID handling: world-writable prefix (same pattern as /home/toolbox) +
# system-level safe.directory for the root-owned clone (~/.gitconfig may be
# a RO host mount — must not be edited). brew refuses to RUN as root →
# presence check only here; smoke-test.sh runs `brew --version` as the
# runtime user. NO_ANALYTICS/NO_AUTO_UPDATE env set next to the PATH ENV
# below (npm-global precedent).
RUN set -eux; \
      git clone --depth 1 --branch "${HOMEBREW_VERSION}" \
        https://github.com/Homebrew/brew /home/linuxbrew/.linuxbrew/Homebrew; \
      mkdir -p /home/linuxbrew/.linuxbrew/bin; \
      ln -s ../Homebrew/bin/brew /home/linuxbrew/.linuxbrew/bin/brew; \
      mkdir -p /usr/share/zsh/vendor-completions; \
      cp /home/linuxbrew/.linuxbrew/Homebrew/completions/zsh/_brew \
        /usr/share/zsh/vendor-completions/_brew; \
      git config --system --add safe.directory /home/linuxbrew/.linuxbrew/Homebrew; \
      chmod -R a+rwX /home/linuxbrew; \
      test -x /home/linuxbrew/.linuxbrew/bin/brew
# xterm-ghostty terminfo. ncurses 6.4 (bookworm) predates the entry added
# in 6.5. Bundled .src includes all `use=` fragments — tic resolves locally.
RUN --mount=type=bind,source=xterm-ghostty.src,target=/tmp/xterm-ghostty.src \
    set -eux; \
    tic -x -o /usr/share/terminfo /tmp/xterm-ghostty.src

# User setup. Container runs --user <host-uid>:<host-gid>, so /home/toolbox
# made world-writable. Pre-create .cache/.config/.local/share so Docker
# doesn't auto-mkdir them as root:root 0755 (blocks sibling subdir writes).
# /etc/passwd + /etc/group world-writable: entrypoint injects runtime UID
# entry (OpenShift pattern; safe — ephemeral, no secrets).
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
    rm -rf /tmp/node-compile-cache /tmp/v8-compile-cache-*

# Passwordless root for the disposable container. Runtime UID is the host's
# (--user <host-uid>) and gets a /etc/passwd entry injected by the entrypoint;
# the blanket `ALL` rule matches whatever UID lands, so `sudo apt-get update &&
# sudo apt install …` works without knowing it at build time (apt lists aren't
# baked — the `update` is required first). Safe-by-ephemerality: the container
# is AutoRemove, so anything installed at runtime vanishes on exit. Caveat:
# sudo writes into bind-mounted host paths (/workspace, ~/.toolbox/*) land as
# root:root on the host — escalate only for in-container/system state.
RUN set -eux; \
    printf '%s\n' \
      'Defaults !requiretty' \
      'Defaults !fqdn' \
      'ALL ALL=(ALL:ALL) NOPASSWD: ALL' \
      > /etc/sudoers.d/toolbox; \
    chmod 0440 /etc/sudoers.d/toolbox; \
    visudo -cf /etc/sudoers.d/toolbox

ENV HOME=/home/toolbox \
    NPM_CONFIG_PREFIX=/home/toolbox/.npm-global \
    EDITOR=vi \
    KUBE_EDITOR=vi \
    STARSHIP_CONFIG=/etc/toolbox/starship.toml
ENV PATH="/home/toolbox/.npm-global/bin:${PATH}"
# Homebrew on PATH for non-interactive `docker exec` too; interactive zsh
# additionally evals `brew shellenv` (idempotent w.r.t. this entry) for
# HOMEBREW_PREFIX/MANPATH/INFOPATH. Analytics off (privacy posture, rtk
# telemetry-lockdown precedent); auto-update off (pin-everything policy —
# also avoids the shallow-clone `brew update` trip on every install).
ENV PATH="/home/linuxbrew/.linuxbrew/bin:/home/linuxbrew/.linuxbrew/sbin:${PATH}" \
    HOMEBREW_NO_ANALYTICS=1 \
    HOMEBREW_NO_AUTO_UPDATE=1

# Curated starship config. Per-user override via STARSHIP_CONFIG in a
# ~/.toolbox-startup.d/ hook.
COPY starship.toml /etc/toolbox/starship.toml

COPY entrypoint.sh /usr/local/bin/entrypoint
RUN chmod +x /usr/local/bin/entrypoint

# Per-tool init manifest. Last so edits invalidate only this layer.
# chmod restores exec bits (embed.FS strips to 0444).
COPY init.d/ /usr/local/lib/toolbox/init.d/
RUN chmod -R 0755 /usr/local/lib/toolbox/init.d/

# Browser-bridge wrapper. Shadows /usr/bin/xdg-open (+synonyms) so URL
# opens hand off to host daemon via host.docker.internal.
COPY bin/xdg-open /usr/local/bin/xdg-open
RUN set -eux; \
    chmod 0755 /usr/local/bin/xdg-open; \
    ln -sf /usr/local/bin/xdg-open /usr/local/bin/sensible-browser; \
    ln -sf /usr/local/bin/xdg-open /usr/local/bin/gnome-open; \
    ln -sf /usr/local/bin/xdg-open /usr/local/bin/x-www-browser; \
    ln -sf /usr/local/bin/xdg-open /usr/local/bin/www-browser; \
    ln -sf /usr/local/bin/xdg-open /usr/local/bin/open
ENV BROWSER=xdg-open

USER toolbox
WORKDIR /workspace
ENTRYPOINT ["/usr/bin/tini", "-g", "--", "/usr/local/bin/entrypoint"]
# See Layer 20a for why CMD is `sleep infinity` rather than a shell.
CMD ["sleep", "infinity"]
