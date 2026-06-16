# syntax=docker/dockerfile:1.24
# Toolbox image. BuildKit required.
#
# Build-time strategy (wall-clock first):
#   1. Every static-binary tool is fetched in its own `fetch-<tool>` stage and
#      lands in the final stage via an independent `COPY --link` layer. Fetch
#      stages run in parallel on cold builds, and a Renovate bump of one tool
#      re-runs only that stage + its COPY — never the tail.
#   2. RUN layers that must live in the final stage (apt/pip/npm installs) are
#      ordered rare→frequent by measured Renovate cadence: heavy+rare first
#      (azure, oci, playwright deps, zsh), frequent npm/pip CLIs last
#      (claude-code and graphify bump ~25×/6 months each).
#   3. `make build` seeds BuildKit from the CI registry cache
#      (ghcr.io/filippolmt/toolbox:buildcache-main).
#
# Fetch-stage convention: artefacts land under /out mirroring the final
# filesystem; the final stage does `COPY --link --from=fetch-<tool> /out/ /`.
# Anything extra a fetch stage installs (unzip, python3, jq) stays out of the
# final image by construction. Run the tool's `--version` inside the fetch
# stage — it's the only thing that catches a wrong-arch or GLIBC-mismatched
# binary before the smoke test.

# Version pins. Renovate-bumped; declared once here (global), re-declared bare
# (no value) inside the stage that consumes them.
ARG RTK_VERSION=0.42.4
ARG GH_VERSION=2.94.0
ARG GLAB_VERSION=1.102.0
ARG GWS_VERSION=0.22.5
ARG ATUIN_VERSION=18.16.1
ARG KUBECTL_VERSION=v1.36.2
ARG KUBECTX_VERSION=0.11.0
ARG HELM_VERSION=v4.2.1
ARG TOFU_VERSION=1.12.2
ARG DOCKER_CLI_VERSION=29.5.3
ARG JQ_VERSION=1.8.1
ARG YQ_VERSION=4.53.3
ARG STARSHIP_VERSION=1.25.1
ARG PNPM_VERSION=11.7.0
ARG BUN_VERSION=1.3.14
ARG UV_VERSION=0.11.21
# sonarqube-cli tags carry no `v` prefix and use a 4-segment
# MAJOR.MINOR.PATCH.BUILD scheme (Renovate: loose versioning).
ARG SONARQUBE_CLI_VERSION=1.0.0.2628
ARG CLAUDE_CODE_VERSION=2.1.178
ARG CODEX_CLI_VERSION=0.140.0
ARG CODEGRAPH_VERSION=1.0.1
ARG CF_CLI_VERSION=0.0.6
ARG WRANGLER_VERSION=4.100.0
ARG PYRIGHT_VERSION=1.1.410
ARG TYPESCRIPT_VERSION=6.0.3
ARG TYPESCRIPT_LANGUAGE_SERVER_VERSION=5.3.0
ARG GCLOUD_VERSION=572.0.0
ARG AZURE_CLI_VERSION=2.87.0
ARG OCI_VERSION=3.86.0
ARG GRAPHIFY_VERSION=0.8.40
ARG PLAYWRIGHT_VERSION=1.61.0
ARG PLAYWRIGHT_CLI_VERSION=0.1.14
ARG COMPOSE_VERSION=5.1.4
# Homebrew tags carry no `v` prefix — bare version.
ARG HOMEBREW_VERSION=6.0.2
ARG GO_VERSION=1.26.4
ARG GOPLS_VERSION=0.22.0
ARG GOIMPORTS_VERSION=0.46.0
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
ARG OMZ_COMMIT=df34d2b8d575777465aed8ae9b7cd90d63fdcd6e
ARG ZSH_AUTOSUGGESTIONS_COMMIT=85919cd1ffa7d2d5412f6d3fe437ebdbeeec4fc5
ARG ZSH_SYNTAX_HIGHLIGHTING_COMMIT=1d85c692615a25fe2293bdd44b34c217d5d2bf04
ARG ZSH_COMPLETIONS_COMMIT=4b23ecd62a9ad79ddf2ac03c0bd8a7b43266508c
ARG FZF_TAB_COMMIT=24105b15714bfec37989ed5c5b6e60f572253019
ARG ZOXIDE_VERSION=0.9.9
# zoxide: no upstream sha256sums file. Same T-01-08-class accepted risk as bat.
ARG ZOXIDE_SHA256_AMD64=4ff057d3c4d957946937274c2b8be7af2a9bbae7f90a1b5e9baaa7cb65a20caa
ARG ZOXIDE_SHA256_ARM64=96e6ea2e47a71db42cb7ad5a36e9209c8cb3708f8ae00f6945573d0d93315cb0
ARG SHELLCHECK_VERSION=0.11.0
# shellcheck: no upstream checksum file. Same accepted-risk pattern as bat/fd —
# pin SHA256 per-arch (.tar.gz; slim base has no xz) + bump in sync (Renovate).
ARG SHELLCHECK_SHA256_AMD64=b7af85e41cc99489dcc21d66c6d5f3685138f06d34651e6d34b42ec6d54fe6f6
ARG SHELLCHECK_SHA256_ARM64=68a8133197a50beb8803f8d42f9908d1af1c5540d4bb05fdfca8c1fa47decefc
ARG SHFMT_VERSION=3.13.1
# shfmt: single static binary, no upstream checksum file. SHA256 pinned per-arch.
ARG SHFMT_SHA256_AMD64=fb096c5d1ac6beabbdbaa2874d025badb03ee07929f0c9ff67563ce8c75398b1
ARG SHFMT_SHA256_ARM64=32d92acaa5cd8abb29fc49dac123dc412442d5713967819d8af2c29f1b3857c7

# rtk builder: amd64 uses upstream MUSL tarball, arm64 builds from source
# against bookworm sysroot. Upstream aarch64-gnu binary links GLIBC 2.39;
# bookworm ships 2.36, no aarch64-musl release. Revisit when upstream
# publishes aarch64-musl OR Azure CLI apt repo gains trixie suite.
FROM rust:1-slim-bookworm AS rtk-builder
# Pipes in RUN fail the build when any segment fails (hadolint DL4006).
# Per-stage: SHELL does not survive a FROM that targets an external image.
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
    fi; \
    /out/bin/rtk --version

# Shared parent for the fetch-* stages: curl + CA bundle + git. tar/gzip/awk/
# sha256sum ship with the slim base. Children inherit the SHELL config.
FROM debian:bookworm-slim AS fetch-base
SHELL ["/bin/bash", "-o", "pipefail", "-c"]
ARG DEBIAN_FRONTEND=noninteractive
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked,id=fetch-apt-cache \
    --mount=type=cache,target=/var/lib/apt/lists,sharing=locked,id=fetch-apt-lists \
    set -eux; \
    rm -f /etc/apt/apt.conf.d/docker-clean; \
    apt-get update -qqy; \
    apt-get install -qqy --no-install-recommends ca-certificates curl git; \
    mkdir -p /out/usr/local/bin /out/usr/share/zsh/vendor-completions

# kubectl
FROM fetch-base AS fetch-kubectl
ARG TARGETARCH
ARG KUBECTL_VERSION
RUN set -eux; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -O "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${TARGETARCH}/kubectl"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o kubectl.sha256 \
        "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${TARGETARCH}/kubectl.sha256"; \
      echo "$(cat kubectl.sha256)  kubectl" | sha256sum -c -; \
      chmod +x kubectl; \
      mv kubectl /out/usr/local/bin/kubectl; \
      rm kubectl.sha256; \
      /out/usr/local/bin/kubectl version --client

# kubectx + kubens (kubectl context/namespace switchers). Linux assets use
# GNU arch suffix (x86_64) — translate from Docker TARGETARCH.
FROM fetch-base AS fetch-kubectx
ARG TARGETARCH
ARG KUBECTX_VERSION
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
        tar -xzf "/tmp/${TARBALL}" -C /out/usr/local/bin "${tool}"; \
        chmod +x "/out/usr/local/bin/${tool}"; \
        rm "/tmp/${TARBALL}"; \
      done; \
      rm /tmp/kubectx_checksums.txt; \
      # Zsh completions live in the repo, NOT the tarball; binary has no
      # `completion zsh` subcommand. Fetch per-tag raw files. T-01-08-class
      # risk (HTTPS + tag pin, no per-file checksum upstream).
      for tool in kubectx kubens; do \
        curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 \
          -o "/out/usr/share/zsh/vendor-completions/_${tool}" \
          "https://raw.githubusercontent.com/ahmetb/kubectx/v${KUBECTX_VERSION}/completion/_${tool}.zsh"; \
      done; \
      /out/usr/local/bin/kubectx --version; \
      /out/usr/local/bin/kubens --version

# helm
FROM fetch-base AS fetch-helm
ARG TARGETARCH
ARG HELM_VERSION
RUN set -eux; \
      HELM_TARBALL="helm-${HELM_VERSION}-linux-${TARGETARCH}.tar.gz"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o "/tmp/${HELM_TARBALL}" \
        "https://get.helm.sh/${HELM_TARBALL}"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /tmp/helm.sha256 \
        "https://get.helm.sh/${HELM_TARBALL}.sha256sum"; \
      cd /tmp && grep "${HELM_TARBALL}" helm.sha256 | sha256sum -c -; \
      tar -xzf "/tmp/${HELM_TARBALL}" -C /out/usr/local/bin "linux-${TARGETARCH}/helm" --strip-components=1; \
      rm "/tmp/${HELM_TARBALL}" /tmp/helm.sha256; \
      /out/usr/local/bin/helm version

# tofu
FROM fetch-base AS fetch-tofu
ARG TARGETARCH
ARG TOFU_VERSION
RUN set -eux; \
      TOFU_TARBALL="tofu_${TOFU_VERSION}_linux_${TARGETARCH}.tar.gz"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o "/tmp/${TOFU_TARBALL}" \
        "https://github.com/opentofu/opentofu/releases/download/v${TOFU_VERSION}/${TOFU_TARBALL}"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /tmp/tofu_SHA256SUMS \
        "https://github.com/opentofu/opentofu/releases/download/v${TOFU_VERSION}/tofu_${TOFU_VERSION}_SHA256SUMS"; \
      cd /tmp && grep "${TOFU_TARBALL}" tofu_SHA256SUMS | sha256sum -c -; \
      tar -xzf "/tmp/${TOFU_TARBALL}" -C /out/usr/local/bin tofu; \
      rm "/tmp/${TOFU_TARBALL}" /tmp/tofu_SHA256SUMS; \
      /out/usr/local/bin/tofu version

# gh
FROM fetch-base AS fetch-gh
ARG TARGETARCH
ARG GH_VERSION
RUN set -eux; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /tmp/gh.tar.gz \
        "https://github.com/cli/cli/releases/download/v${GH_VERSION}/gh_${GH_VERSION}_linux_${TARGETARCH}.tar.gz"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /tmp/gh_checksums.txt \
        "https://github.com/cli/cli/releases/download/v${GH_VERSION}/gh_${GH_VERSION}_checksums.txt"; \
      grep "gh_${GH_VERSION}_linux_${TARGETARCH}.tar.gz" /tmp/gh_checksums.txt \
        | awk '{print $1"  /tmp/gh.tar.gz"}' | sha256sum -c -; \
      tar -xzf /tmp/gh.tar.gz -C /out/usr/local --strip-components=1 "gh_${GH_VERSION}_linux_${TARGETARCH}/bin/gh"; \
      rm /tmp/gh.tar.gz /tmp/gh_checksums.txt; \
      /out/usr/local/bin/gh --version

# glab
FROM fetch-base AS fetch-glab
ARG TARGETARCH
ARG GLAB_VERSION
RUN set -eux; \
      GLAB_TARBALL="glab_${GLAB_VERSION}_linux_${TARGETARCH}.tar.gz"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o "/tmp/${GLAB_TARBALL}" \
        "https://gitlab.com/api/v4/projects/gitlab-org%2Fcli/packages/generic/glab/${GLAB_VERSION}/${GLAB_TARBALL}"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /tmp/glab_checksums.txt \
        "https://gitlab.com/api/v4/projects/gitlab-org%2Fcli/packages/generic/glab/${GLAB_VERSION}/checksums.txt"; \
      cd /tmp && grep "${GLAB_TARBALL}" glab_checksums.txt | sha256sum -c -; \
      tar -xzf "/tmp/${GLAB_TARBALL}" -C /out/usr/local/bin bin/glab --strip-components=1; \
      rm "/tmp/${GLAB_TARBALL}" /tmp/glab_checksums.txt; \
      /out/usr/local/bin/glab version

# gws (Google Workspace CLI). MUSL variant — upstream `-gnu` targets
# GLIBC 2.39, bookworm ships 2.36. Runtime keyring knob
# (GOOGLE_WORKSPACE_CLI_KEYRING_BACKEND=file) is set in the final stage.
FROM fetch-base AS fetch-gws
ARG TARGETARCH
ARG GWS_VERSION
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
      tar -xzf "/tmp/${GWS_TARBALL}" -C /out/usr/local/bin --strip-components=1 ./gws; \
      chmod +x /out/usr/local/bin/gws; \
      rm "/tmp/${GWS_TARBALL}" /tmp/gws.sha256; \
      /out/usr/local/bin/gws --version

# atuin (SQLite-backed Ctrl-R). MUSL — same GLIBC mismatch as gws.
FROM fetch-base AS fetch-atuin
ARG TARGETARCH
ARG ATUIN_VERSION
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
      tar -xzf "/tmp/${ATUIN_TARBALL}" -C /out/usr/local/bin --strip-components=1 \
        "atuin-${ATUIN_ARCH}/atuin"; \
      chmod +x /out/usr/local/bin/atuin; \
      rm "/tmp/${ATUIN_TARBALL}" /tmp/atuin.sha256; \
      /out/usr/local/bin/atuin --version

# Docker CLI standalone. No upstream .sha256 — T-01-08 accepted risk
# (HTTPS + version pin only).
FROM fetch-base AS fetch-docker
ARG TARGETARCH
ARG DOCKER_CLI_VERSION
RUN set -eux; \
      case "${TARGETARCH}" in amd64) DOCKER_ARCH=x86_64;; arm64) DOCKER_ARCH=aarch64;; *) DOCKER_ARCH=${TARGETARCH};; esac; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /tmp/docker.tgz \
        "https://download.docker.com/linux/static/stable/${DOCKER_ARCH}/docker-${DOCKER_CLI_VERSION}.tgz"; \
      tar -xzf /tmp/docker.tgz -C /out/usr/local/bin --strip-components=1 docker/docker; \
      rm /tmp/docker.tgz; \
      /out/usr/local/bin/docker --version

# Docker Compose plugin for the static docker CLI.
FROM fetch-base AS fetch-compose
ARG TARGETARCH
ARG COMPOSE_VERSION
RUN set -eux; \
      case "${TARGETARCH}" in \
        amd64) COMPOSE_ARCH=x86_64;; \
        arm64) COMPOSE_ARCH=aarch64;; \
        *) echo "Unsupported arch: ${TARGETARCH}" && exit 1;; \
      esac; \
      COMPOSE_BIN="docker-compose-linux-${COMPOSE_ARCH}"; \
      install -d -m 0755 /out/usr/local/libexec/docker/cli-plugins; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /out/usr/local/libexec/docker/cli-plugins/docker-compose \
        "https://github.com/docker/compose/releases/download/v${COMPOSE_VERSION}/${COMPOSE_BIN}"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /tmp/compose.sha256 \
        "https://github.com/docker/compose/releases/download/v${COMPOSE_VERSION}/${COMPOSE_BIN}.sha256"; \
      EXPECTED=$(awk '{print $1}' /tmp/compose.sha256); \
      echo "${EXPECTED}  /out/usr/local/libexec/docker/cli-plugins/docker-compose" | sha256sum -c -; \
      chmod +x /out/usr/local/libexec/docker/cli-plugins/docker-compose; \
      rm /tmp/compose.sha256; \
      /out/usr/local/libexec/docker/cli-plugins/docker-compose version

# starship
FROM fetch-base AS fetch-starship
ARG TARGETARCH
ARG STARSHIP_VERSION
RUN set -eux; \
      case "${TARGETARCH}" in \
        amd64) STARSHIP_TARBALL="starship-x86_64-unknown-linux-gnu.tar.gz";; \
        arm64) STARSHIP_TARBALL="starship-aarch64-unknown-linux-musl.tar.gz";; \
        *) echo "Unsupported arch: ${TARGETARCH}" && exit 1;; \
      esac; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /tmp/starship.tar.gz \
        "https://github.com/starship/starship/releases/download/v${STARSHIP_VERSION}/${STARSHIP_TARBALL}"; \
      tar -xzf /tmp/starship.tar.gz -C /out/usr/local/bin starship; \
      rm /tmp/starship.tar.gz; \
      /out/usr/local/bin/starship --version

# jq
FROM fetch-base AS fetch-jq
ARG TARGETARCH
ARG JQ_VERSION
RUN set -eux; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /out/usr/local/bin/jq \
        "https://github.com/jqlang/jq/releases/download/jq-${JQ_VERSION}/jq-linux-${TARGETARCH}"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /tmp/jq_sha256 \
        "https://github.com/jqlang/jq/releases/download/jq-${JQ_VERSION}/sha256sum.txt"; \
      grep "jq-linux-${TARGETARCH}$" /tmp/jq_sha256 \
        | awk '{print $1"  /out/usr/local/bin/jq"}' | sha256sum -c -; \
      chmod +x /out/usr/local/bin/jq; \
      rm /tmp/jq_sha256; \
      /out/usr/local/bin/jq --version

# yq. Checksums file has 31 hash algorithms per row; SHA-256 is column $19.
FROM fetch-base AS fetch-yq
ARG TARGETARCH
ARG YQ_VERSION
RUN set -eux; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /out/usr/local/bin/yq \
        "https://github.com/mikefarah/yq/releases/download/v${YQ_VERSION}/yq_linux_${TARGETARCH}"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /tmp/yq_checksums \
        "https://github.com/mikefarah/yq/releases/download/v${YQ_VERSION}/checksums"; \
      EXPECTED=$(grep "^yq_linux_${TARGETARCH} " /tmp/yq_checksums | awk '{print $19}'); \
      test -n "${EXPECTED}" || { echo "ERROR: SHA-256 hash not found in checksums file"; exit 1; }; \
      echo "${EXPECTED}  /out/usr/local/bin/yq" | sha256sum -c -; \
      chmod +x /out/usr/local/bin/yq; \
      rm /tmp/yq_checksums; \
      /out/usr/local/bin/yq --version

# bat (pretty cat). SHA256 pinned per-arch (no upstream checksum file).
FROM fetch-base AS fetch-bat
ARG TARGETARCH
ARG BAT_VERSION
ARG BAT_SHA256_AMD64
ARG BAT_SHA256_ARM64
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
      tar -xzf "/tmp/${BAT_TARBALL}" -C /out/usr/local/bin --strip-components=1 \
        "bat-v${BAT_VERSION}-${BAT_ARCH}/bat"; \
      chmod +x /out/usr/local/bin/bat; \
      # bat has no `completion zsh` subcommand — ship the upstream static file.
      tar -xzf "/tmp/${BAT_TARBALL}" -C /tmp --strip-components=2 \
        "bat-v${BAT_VERSION}-${BAT_ARCH}/autocomplete/bat.zsh"; \
      mv /tmp/bat.zsh /out/usr/share/zsh/vendor-completions/_bat; \
      rm "/tmp/${BAT_TARBALL}"; \
      /out/usr/local/bin/bat --version

# fd (find replacement). MUSL both arches. SHA256 pinned per-arch (no
# upstream checksum file — same accepted-risk pattern as bat/zoxide).
FROM fetch-base AS fetch-fd
ARG TARGETARCH
ARG FD_VERSION
ARG FD_SHA256_AMD64
ARG FD_SHA256_ARM64
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
      tar -xzf "/tmp/${FD_TARBALL}" -C /out/usr/local/bin --strip-components=1 \
        "fd-v${FD_VERSION}-${FD_ARCH}/fd"; \
      chmod +x /out/usr/local/bin/fd; \
      # fd ships a pre-generated _fd zsh completion in the tarball — same
      # convention as bat. No `completion zsh` subcommand on fd itself.
      tar -xzf "/tmp/${FD_TARBALL}" -C /tmp --strip-components=2 \
        "fd-v${FD_VERSION}-${FD_ARCH}/autocomplete/_fd"; \
      mv /tmp/_fd /out/usr/share/zsh/vendor-completions/_fd; \
      rm "/tmp/${FD_TARBALL}"; \
      /out/usr/local/bin/fd --version

# eza (ls replacement). amd64 MUSL, arm64 GNU (no upstream aarch64-musl tar).
# Completions ship in a separate arch-independent tarball; extract `_eza` in
# the same RUN so a Renovate bump of EZA_VERSION invalidates both atomically.
FROM fetch-base AS fetch-eza
ARG TARGETARCH
ARG EZA_VERSION
ARG EZA_SHA256_AMD64
ARG EZA_SHA256_ARM64
ARG EZA_COMPLETIONS_SHA256
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
      tar -xzf "/tmp/${EZA_TARBALL}" -C /out/usr/local/bin ./eza; \
      chmod +x /out/usr/local/bin/eza; \
      rm "/tmp/${EZA_TARBALL}"; \
      EZA_COMPLETIONS_TARBALL="completions-${EZA_VERSION}.tar.gz"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o "/tmp/${EZA_COMPLETIONS_TARBALL}" \
        "https://github.com/eza-community/eza/releases/download/v${EZA_VERSION}/${EZA_COMPLETIONS_TARBALL}"; \
      echo "${EZA_COMPLETIONS_SHA256}  /tmp/${EZA_COMPLETIONS_TARBALL}" | sha256sum -c -; \
      tar -xzf "/tmp/${EZA_COMPLETIONS_TARBALL}" -C /tmp \
        "./target/completions-${EZA_VERSION}/_eza"; \
      mv "/tmp/target/completions-${EZA_VERSION}/_eza" /out/usr/share/zsh/vendor-completions/_eza; \
      rm -rf "/tmp/${EZA_COMPLETIONS_TARBALL}" /tmp/target; \
      /out/usr/local/bin/eza --version

# shellcheck (shell linter). Static binary, .tar.gz (slim base has no xz).
# No upstream checksum file → SHA256 pinned per-arch. No shipped completion.
FROM fetch-base AS fetch-shellcheck
ARG TARGETARCH
ARG SHELLCHECK_VERSION
ARG SHELLCHECK_SHA256_AMD64
ARG SHELLCHECK_SHA256_ARM64
RUN set -eux; \
      case "${TARGETARCH}" in \
        amd64) SC_ARCH=x86_64;   SC_SHA256="${SHELLCHECK_SHA256_AMD64}";; \
        arm64) SC_ARCH=aarch64;  SC_SHA256="${SHELLCHECK_SHA256_ARM64}";; \
        *) echo "Unsupported arch: ${TARGETARCH}" && exit 1;; \
      esac; \
      SC_TARBALL="shellcheck-v${SHELLCHECK_VERSION}.linux.${SC_ARCH}.tar.gz"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o "/tmp/${SC_TARBALL}" \
        "https://github.com/koalaman/shellcheck/releases/download/v${SHELLCHECK_VERSION}/${SC_TARBALL}"; \
      echo "${SC_SHA256}  /tmp/${SC_TARBALL}" | sha256sum -c -; \
      tar -xzf "/tmp/${SC_TARBALL}" -C /out/usr/local/bin --strip-components=1 \
        "shellcheck-v${SHELLCHECK_VERSION}/shellcheck"; \
      chmod +x /out/usr/local/bin/shellcheck; \
      rm "/tmp/${SC_TARBALL}"; \
      /out/usr/local/bin/shellcheck --version

# shfmt (shell formatter). Single static binary, no upstream checksum file →
# SHA256 pinned per-arch.
FROM fetch-base AS fetch-shfmt
ARG TARGETARCH
ARG SHFMT_VERSION
ARG SHFMT_SHA256_AMD64
ARG SHFMT_SHA256_ARM64
RUN set -eux; \
      case "${TARGETARCH}" in \
        amd64) SHFMT_SHA256="${SHFMT_SHA256_AMD64}";; \
        arm64) SHFMT_SHA256="${SHFMT_SHA256_ARM64}";; \
        *) echo "Unsupported arch: ${TARGETARCH}" && exit 1;; \
      esac; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /tmp/shfmt \
        "https://github.com/mvdan/sh/releases/download/v${SHFMT_VERSION}/shfmt_v${SHFMT_VERSION}_linux_${TARGETARCH}"; \
      echo "${SHFMT_SHA256}  /tmp/shfmt" | sha256sum -c -; \
      install -m 0755 /tmp/shfmt /out/usr/local/bin/shfmt; \
      rm /tmp/shfmt; \
      /out/usr/local/bin/shfmt --version

# bun. GLIBC variant (NOT -musl) — bun's musl build is dynamically linked
# against /lib/ld-musl-x86_64.so.1, which Debian doesn't ship → ENOENT.
# Bun's GLIBC build targets ≥ 2.27, well below bookworm's 2.36. Opposite
# rationale vs gws/atuin/zoxide. ~/.bun bound via mountplan defaults.
# unzip installed here stays out of the final image (fetch-stage convention).
FROM fetch-base AS fetch-bun
ARG DEBIAN_FRONTEND=noninteractive
ARG TARGETARCH
ARG BUN_VERSION
# Per-stage apt cache id: a shared `sharing=locked` id would serialize the
# apt phases of the parallel fetch stages (bun/go/gcloud) on one lock.
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked,id=fetch-apt-cache-bun \
    --mount=type=cache,target=/var/lib/apt/lists,sharing=locked,id=fetch-apt-lists-bun \
    set -eux; \
      apt-get update -qqy; \
      apt-get install -qqy --no-install-recommends unzip; \
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
      unzip -q "/tmp/${BUN_ZIP}" -d /tmp/; \
      mv "/tmp/${BUN_DIR}/bun" /out/usr/local/bin/bun; \
      chmod +x /out/usr/local/bin/bun; \
      # Absolute symlink — dangling inside this stage, valid in the final image.
      ln -sf /usr/local/bin/bun /out/usr/local/bin/bunx; \
      rm -rf "/tmp/${BUN_ZIP}" /tmp/bun_SHASUMS256.txt "/tmp/${BUN_DIR}"; \
      /out/usr/local/bin/bun --version

# uv (Python package manager). Pinned tarball install, SHA256 from upstream.
FROM fetch-base AS fetch-uv
ARG TARGETARCH
ARG UV_VERSION
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
      tar -xzf "/tmp/${UV_TARBALL}" -C /out/usr/local/bin --strip-components=1 \
        "uv-${UV_ARCH}/uv" "uv-${UV_ARCH}/uvx"; \
      rm "/tmp/${UV_TARBALL}" /tmp/uv.sha256; \
      /out/usr/local/bin/uv --version

# sonar (SonarQube CLI). Bun-compiled single binary from the SonarSource CDN —
# the GitHub releases carry no assets. Per-file .sha256 published alongside
# (bare hex digest, no filename column). glibc-dynamic (min 2.17, libsecret
# loaded lazily via dlopen), fine on bookworm; no musl build exists.
FROM fetch-base AS fetch-sonar
ARG TARGETARCH
ARG SONARQUBE_CLI_VERSION
RUN set -eux; \
      case "${TARGETARCH}" in \
        amd64) SONAR_ARCH=x86-64;; \
        arm64) SONAR_ARCH=arm64;; \
        *) echo "Unsupported arch: ${TARGETARCH}" && exit 1;; \
      esac; \
      SONAR_BIN="sonarqube-cli-${SONARQUBE_CLI_VERSION}-linux-${SONAR_ARCH}.bin"; \
      SONAR_URL="https://binaries.sonarsource.com/Distribution/sonarqube-cli/${SONARQUBE_CLI_VERSION}/linux"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o "/tmp/${SONAR_BIN}" \
        "${SONAR_URL}/${SONAR_BIN}"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /tmp/sonar.sha256 \
        "${SONAR_URL}/${SONAR_BIN}.sha256"; \
      echo "$(cat /tmp/sonar.sha256)  /tmp/${SONAR_BIN}" | sha256sum -c -; \
      install -m 0755 "/tmp/${SONAR_BIN}" /out/usr/local/bin/sonar; \
      rm "/tmp/${SONAR_BIN}" /tmp/sonar.sha256; \
      /out/usr/local/bin/sonar --version

# zoxide. SHA256 from build-arg literal (no upstream checksum file). MUSL
# both arches (upstream only ships musl for arm64).
FROM fetch-base AS fetch-zoxide
ARG TARGETARCH
ARG ZOXIDE_VERSION
ARG ZOXIDE_SHA256_AMD64
ARG ZOXIDE_SHA256_ARM64
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
      tar -xzf "/tmp/${ZOXIDE_TARBALL}" -C /out/usr/local/bin zoxide; \
      chmod +x /out/usr/local/bin/zoxide; \
      rm "/tmp/${ZOXIDE_TARBALL}"; \
      /out/usr/local/bin/zoxide --version

# tini PID-1. CMD is `sleep infinity` (not a shell) — interactive shells
# ignore SIGTERM when owning the TTY, blocking `docker stop` until SIGKILL.
# tini -g forwards signals to the whole process group.
FROM fetch-base AS fetch-tini
ARG TARGETARCH
ARG TINI_VERSION
RUN set -eux; \
    mkdir -p /out/usr/bin; \
    curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /out/usr/bin/tini \
      "https://github.com/krallin/tini/releases/download/v${TINI_VERSION}/tini-static-${TARGETARCH}"; \
    curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o /tmp/tini.sha256sum \
      "https://github.com/krallin/tini/releases/download/v${TINI_VERSION}/tini-static-${TARGETARCH}.sha256sum"; \
    EXPECTED=$(awk '{print $1}' /tmp/tini.sha256sum); \
    echo "${EXPECTED}  /out/usr/bin/tini" | sha256sum -c -; \
    chmod +x /out/usr/bin/tini; \
    rm /tmp/tini.sha256sum; \
    /out/usr/bin/tini --version

# Go toolchain + gopls + goimports.
#   -ldflags="-s -w" strips debug + symtab (~30% smaller).
#   HOME=/tmp/go-install scoped to this RUN; the toolchain untars straight
#   into /out and runs from there (GOROOT is inferred from the binary path).
#   SHA256: go.dev/dl/<file>.sha256 redirects to HTML — pull from JSON index
#   (jq installed in-stage; stays out of the final image, which gets its own
#   jq from fetch-jq).
FROM fetch-base AS fetch-go
ARG DEBIAN_FRONTEND=noninteractive
ARG TARGETARCH
ARG GO_VERSION
ARG GOPLS_VERSION
ARG GOIMPORTS_VERSION
# Per-stage apt cache id — see fetch-bun.
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked,id=fetch-apt-cache-go \
    --mount=type=cache,target=/var/lib/apt/lists,sharing=locked,id=fetch-apt-lists-go \
    set -eux; \
      apt-get update -qqy; \
      apt-get install -qqy --no-install-recommends jq; \
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
      tar -C /out/usr/local -xzf "/tmp/${GO_TARBALL}"; \
      rm "/tmp/${GO_TARBALL}" /tmp/go_dl_index.json; \
      # gopls + goimports live in different modules — `go install` invoked twice.
        HOME=/tmp/go-install \
        GOPATH=/tmp/go-install \
        GOCACHE=/tmp/go-install/cache \
        GOMODCACHE=/tmp/go-install/pkg/mod \
        GOBIN=/out/usr/local/bin \
        /out/usr/local/go/bin/go install -ldflags="-s -w" \
          "golang.org/x/tools/gopls@v${GOPLS_VERSION}"; \
        /out/usr/local/bin/gopls version; \
        HOME=/tmp/go-install \
        GOPATH=/tmp/go-install \
        GOCACHE=/tmp/go-install/cache \
        GOMODCACHE=/tmp/go-install/pkg/mod \
        GOBIN=/out/usr/local/bin \
        /out/usr/local/go/bin/go install -ldflags="-s -w" \
          "golang.org/x/tools/cmd/goimports@v${GOIMPORTS_VERSION}"; \
        # goimports needs `go` on PATH to do anything useful; the final image
        # gets that via ENV PATH. Presence check only here — smoke-test.sh
        # runs the functional check.
        test -x /out/usr/local/bin/goimports; \
      rm -rf /tmp/go-install; \
      rm -rf /out/usr/local/go/test /out/usr/local/go/api /out/usr/local/go/doc /out/usr/local/go/misc; \
      find /out/usr/local/go/src -name '*_test.go' -delete; \
      find /out/usr/local/go/src -type d -name testdata -prune -exec rm -rf {} +; \
      /out/usr/local/go/bin/go version

# gcloud. No per-version SHA256 file upstream — T-01-08 accepted risk.
# The SDK is relocatable: extracted into /out/opt and driven from there
# (components install included). python3 installed in-stage; the final image
# provides python3 from its own base apt layer. Symlinks point at the final
# /opt path — dangling inside this stage, valid after COPY.
FROM fetch-base AS fetch-gcloud
ARG DEBIAN_FRONTEND=noninteractive
ARG TARGETARCH
ARG GCLOUD_VERSION
# Per-stage apt cache id — see fetch-bun.
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked,id=fetch-apt-cache-gcloud \
    --mount=type=cache,target=/var/lib/apt/lists,sharing=locked,id=fetch-apt-lists-gcloud \
    set -eux; \
      apt-get update -qqy; \
      apt-get install -qqy --no-install-recommends python3; \
      case "${TARGETARCH}" in \
        amd64) GCLOUD_ARCH=x86_64;; \
        arm64) GCLOUD_ARCH=arm;; \
        *) echo "Unsupported arch: ${TARGETARCH}" && exit 1;; \
      esac; \
      GCLOUD_TARBALL="google-cloud-cli-${GCLOUD_VERSION}-linux-${GCLOUD_ARCH}.tar.gz"; \
      curl -fsSL --retry 10 --retry-delay 5 --retry-all-errors --retry-max-time 120 -o "/tmp/${GCLOUD_TARBALL}" \
        "https://dl.google.com/dl/cloudsdk/channels/rapid/downloads/${GCLOUD_TARBALL}"; \
      mkdir -p /out/opt; \
      tar -xzf "/tmp/${GCLOUD_TARBALL}" -C /out/opt; \
      rm "/tmp/${GCLOUD_TARBALL}"; \
      /out/opt/google-cloud-sdk/bin/gcloud components install gke-gcloud-auth-plugin --quiet; \
      ln -s /opt/google-cloud-sdk/bin/gcloud /out/usr/local/bin/gcloud; \
      ln -s /opt/google-cloud-sdk/bin/gsutil /out/usr/local/bin/gsutil; \
      ln -s /opt/google-cloud-sdk/bin/bq /out/usr/local/bin/bq; \
      ln -s /opt/google-cloud-sdk/bin/gke-gcloud-auth-plugin /out/usr/local/bin/gke-gcloud-auth-plugin; \
      /out/opt/google-cloud-sdk/bin/gcloud --version; \
      /out/opt/google-cloud-sdk/bin/gke-gcloud-auth-plugin --version; \
      # Size trim. Keeps gcloud/gsutil/bq/gke-gcloud-auth-plugin functional.
      find /out/opt/google-cloud-sdk -type d -name __pycache__ -prune -exec rm -rf {} + 2>/dev/null || true; \
      find /out/opt/google-cloud-sdk -type f -name '*.pyc' -delete 2>/dev/null || true; \
      find /out/opt/google-cloud-sdk -type d \( -name tests -o -name test \) -prune -exec rm -rf {} + 2>/dev/null || true; \
      find /out/opt/google-cloud-sdk -type f \( -name '*.md' -o -name '*.markdown' -o -name 'CHANGELOG*' -o -name 'README*' \) -delete 2>/dev/null || true; \
      rm -rf /out/opt/google-cloud-sdk/platform/bundledpythonunix 2>/dev/null || true; \
      rm -rf /out/opt/google-cloud-sdk/platform/anthoscli 2>/dev/null || true

# Oh-My-Zsh + 4 custom plugins, cloned into /out/home/node — the final-stage
# user-setup layer runs `usermod -d /home/toolbox -m toolbox`, which relocates
# the dir (and refuses if /home/toolbox already exists). End state:
# /home/toolbox/.oh-my-zsh.
#
# `.git` retained (per R-05, SPEC ZSH-02 acceptance: `git rev-parse HEAD`
# equals OMZ_COMMIT). Delta ~24 MB, within 50 MB budget.
#
# Pattern: `git init + remote add + fetch --depth=1 <sha> + checkout FETCH_HEAD`.
# `git clone --depth=1 --branch <sha>` FAILS — --branch accepts named refs only.
# GitHub enables `uploadpack.allowReachableSHA1InWant=true`, so full-SHA fetches
# work. Abbreviated SHAs do NOT work — always use the full 40-char form.
FROM fetch-base AS fetch-omz
ARG OMZ_COMMIT
ARG ZSH_AUTOSUGGESTIONS_COMMIT
ARG ZSH_SYNTAX_HIGHLIGHTING_COMMIT
ARG ZSH_COMPLETIONS_COMMIT
ARG FZF_TAB_COMMIT
RUN set -eux; \
      git -c init.defaultBranch=master init /out/home/node/.oh-my-zsh; \
      git -C /out/home/node/.oh-my-zsh remote add origin https://github.com/ohmyzsh/ohmyzsh.git; \
      git -C /out/home/node/.oh-my-zsh fetch --depth=1 origin "${OMZ_COMMIT}"; \
      git -C /out/home/node/.oh-my-zsh checkout FETCH_HEAD; \
      for triplet in \
        "zsh-autosuggestions|https://github.com/zsh-users/zsh-autosuggestions.git|${ZSH_AUTOSUGGESTIONS_COMMIT}" \
        "zsh-syntax-highlighting|https://github.com/zsh-users/zsh-syntax-highlighting.git|${ZSH_SYNTAX_HIGHLIGHTING_COMMIT}" \
        "zsh-completions|https://github.com/zsh-users/zsh-completions.git|${ZSH_COMPLETIONS_COMMIT}" \
        "fzf-tab|https://github.com/Aloxaf/fzf-tab.git|${FZF_TAB_COMMIT}" ; do \
        name=$(echo "$triplet" | cut -d'|' -f1); \
        url=$(echo "$triplet"  | cut -d'|' -f2); \
        sha=$(echo "$triplet"  | cut -d'|' -f3); \
        dir="/out/home/node/.oh-my-zsh/custom/plugins/$name"; \
        git init "$dir"; \
        git -C "$dir" remote add origin "$url"; \
        git -C "$dir" fetch --depth=1 origin "$sha"; \
        git -C "$dir" checkout FETCH_HEAD; \
      done; \
      echo '# managed by toolbox — see /etc/zsh/zshrc' > /out/home/node/.zshrc

# Homebrew (Linuxbrew). Shallow tag clone at the DEFAULT prefix — bottles
# only work at /home/linuxbrew/.linuxbrew; the official installer is unusable
# in a RUN (refuses root, clones unpinned `main`). Layout mirrors what the
# installer produces: repo at …/Homebrew + bin/brew symlink (relative — valid
# after COPY). Pre-built _brew zsh completion ships from the clone (bat/fd/eza
# precedent). Variable host UID handling: world-writable prefix (same pattern
# as /home/toolbox); the system-level safe.directory for the root-owned clone
# is added in the final stage (~/.gitconfig may be a RO host mount — must not
# be edited). brew refuses to RUN as root → presence check only;
# smoke-test.sh runs `brew --version` as the runtime user.
# NO_ANALYTICS/NO_AUTO_UPDATE env set in the final stage next to the PATH ENV.
FROM fetch-base AS fetch-brew
ARG HOMEBREW_VERSION
RUN set -eux; \
      git clone --depth 1 --branch "${HOMEBREW_VERSION}" \
        https://github.com/Homebrew/brew /out/home/linuxbrew/.linuxbrew/Homebrew; \
      mkdir -p /out/home/linuxbrew/.linuxbrew/bin; \
      ln -s ../Homebrew/bin/brew /out/home/linuxbrew/.linuxbrew/bin/brew; \
      cp /out/home/linuxbrew/.linuxbrew/Homebrew/completions/zsh/_brew \
        /out/usr/share/zsh/vendor-completions/_brew; \
      chmod -R a+rwX /out/home/linuxbrew; \
      test -x /out/home/linuxbrew/.linuxbrew/bin/brew

# ============================== Final stage ==================================
FROM node:24-bookworm-slim
# Pipes in RUN fail the build when any segment fails (hadolint DL4006).
SHELL ["/bin/bash", "-o", "pipefail", "-c"]

ARG TARGETARCH
ARG DEBIAN_FRONTEND=noninteractive
# Versions consumed by final-stage RUN layers (apt/pip/npm — cannot fan out).
ARG PNPM_VERSION
ARG CLAUDE_CODE_VERSION
ARG CODEX_CLI_VERSION
ARG CODEGRAPH_VERSION
ARG CF_CLI_VERSION
ARG WRANGLER_VERSION
ARG PYRIGHT_VERSION
ARG TYPESCRIPT_VERSION
ARG TYPESCRIPT_LANGUAGE_SERVER_VERSION
ARG AZURE_CLI_VERSION
ARG OCI_VERSION
ARG GRAPHIFY_VERSION
ARG PLAYWRIGHT_VERSION
ARG PLAYWRIGHT_CLI_VERSION

# consoledonottrack.com convention — honored by bun, playwright, and much of
# the JS toolchain users run in here (next, astro, turbo, …). claude is the
# one exemption: it treats DO_NOT_TRACK as a telemetry umbrella and shuts
# down the Statsig feature-flag channel (breaks Remote Control — /doctor
# flags it). Exemption lives in the claude install layer as an `env -u`
# wrapper around the real binary.
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

# Fetched tools land here. Each COPY --link is an independent layer: a version
# bump re-runs only its fetch stage + this COPY, and parent-layer changes
# never re-trigger them.
COPY --link --from=fetch-kubectl  /out/ /
COPY --link --from=fetch-kubectx  /out/ /
COPY --link --from=fetch-helm     /out/ /
COPY --link --from=fetch-tofu     /out/ /
COPY --link --from=fetch-gh       /out/ /
COPY --link --from=fetch-glab     /out/ /
COPY --link --from=fetch-gws      /out/ /
COPY --link --from=fetch-atuin    /out/ /
COPY --link --from=fetch-docker   /out/ /
COPY --link --from=fetch-compose  /out/ /
COPY --link --from=fetch-starship /out/ /
COPY --link --from=fetch-jq       /out/ /
COPY --link --from=fetch-yq       /out/ /
COPY --link --from=fetch-bat      /out/ /
COPY --link --from=fetch-fd       /out/ /
COPY --link --from=fetch-eza      /out/ /
COPY --link --from=fetch-shellcheck /out/ /
COPY --link --from=fetch-shfmt    /out/ /
COPY --link --from=fetch-bun      /out/ /
COPY --link --from=fetch-uv       /out/ /
COPY --link --from=fetch-sonar    /out/ /
COPY --link --from=fetch-zoxide   /out/ /
COPY --link --from=fetch-tini     /out/ /
COPY --link --from=fetch-go       /out/ /
COPY --link --from=fetch-gcloud   /out/ /
COPY --link --from=fetch-omz      /out/ /
COPY --link --from=fetch-brew     /out/ /
# rtk (Rust CLI proxy). Binary built + version-checked in rtk-builder.
COPY --link --chmod=0755 --from=rtk-builder /out/bin/rtk /usr/local/bin/rtk

# Runtime knobs for the fetched tools above.
#   CHECKPOINT_DISABLE: opt out of HashiCorp Checkpoint pings (tofu,
#     inherited from Terraform).
#   GOOGLE_WORKSPACE_CLI_KEYRING_BACKEND=file: gws writes auth key to
#     ~/.config/gws/.encryption_key (no D-Bus inside container).
#   ATUIN_CONFIG_DIR consolidates config + DB under one bind-mount.
#   CLOUDSDK_CORE_DISABLE_USAGE_REPORTING: opt out of gcloud usage telemetry.
#   CLOUDSDK_COMPONENT_MANAGER_DISABLE_UPDATE_CHECK: suppress the
#     "Updates are available" nag on every gcloud call. Component upgrades
#     inside the container wouldn't persist past `toolbox stop` anyway — the
#     canonical upgrade path is bumping GCLOUD_VERSION (Renovate-managed).
#   CLOUDSDK_SURVEY_PROMPT_DISABLE: suppress the survey nag printed at the
#     tail of authenticated commands.
#   RTK_TELEMETRY_DISABLED=1: hard kill-switch surviving any `rtk telemetry
#     enable` rewrite of config.toml.
#   RTK_TEE=0: hard kill-switch for tee feature — writes failed-command
#     stdout/stderr (often carrying tokens) to ~/.local/share/rtk/tee/*.log.
#     Source-side override checked at src/core/tee.rs (v0.37.2 lines 152, 200).
#   SONARQUBE_CLI_KEYCHAIN_FILE: sonar stores tokens via Bun.secrets →
#     libsecret, which fails fatally without a D-Bus secret service; the
#     file backend (src/lib/keychain.ts) lands the token (plaintext JSON)
#     inside the ~/.sonar bind-mount so `sonar auth login` survives
#     `toolbox stop`.
ENV CHECKPOINT_DISABLE=1 \
    GOOGLE_WORKSPACE_CLI_KEYRING_BACKEND=file \
    SONARQUBE_CLI_KEYCHAIN_FILE=/home/toolbox/.sonar/sonarqube-cli/keychain.json \
    ATUIN_CONFIG_DIR=/home/toolbox/.local/share/atuin/config \
    CLOUDSDK_CORE_DISABLE_USAGE_REPORTING=true \
    CLOUDSDK_COMPONENT_MANAGER_DISABLE_UPDATE_CHECK=1 \
    CLOUDSDK_SURVEY_PROMPT_DISABLE=1 \
    RTK_TELEMETRY_DISABLED=1 \
    RTK_TEE=0
ENV PATH="/usr/local/go/bin:${PATH}"
# Per-user bun global bin in PATH.
ENV PATH="/home/toolbox/.bun/bin:${PATH}"

# From here on: layers that must run in the final stage (apt/pip/npm),
# ordered to minimize rebuild tail: cost-below × Renovate cadence. Heavy +
# rare first; cheap + frequent last. One inversion is deliberate: graphify
# (expensive pip+gcc, 25 bumps/6mo) sits ABOVE pnpm/claude (cheap, 11/25
# bumps) so its 2-minute layer is not re-run by their frequent bumps.

# zsh + fzf. OMZ + plugins arrive pre-cloned via fetch-omz above.
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked,id=apt-cache \
    --mount=type=cache,target=/var/lib/apt/lists,sharing=locked,id=apt-lists \
    set -eux; \
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
      apt-get update -qqy; \
      apt-get install -qqy --no-install-recommends zsh fzf; \
      mkdir -p /etc/zsh; \
      zsh --version; \
      fzf --version;
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
# NPM_CONFIG_FUND/AUDIT: suppress banner + audit on every npm install -g below.
ENV NPM_CONFIG_FUND=false \
    NPM_CONFIG_AUDIT=false

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
# OCI CLI via pip. No upstream binary release; --break-system-packages
# accepted for this single-purpose image (PEP 668).
RUN --mount=type=cache,target=/root/.cache/pip,id=pip-cache \
    set -eux; \
      pip install --break-system-packages \
        --timeout 300 --retries 5 \
        "oci-cli==${OCI_VERSION}"; \
      oci --version; \
      find /usr/local/lib/python* -type d -name __pycache__ -prune -exec rm -rf {} + 2>/dev/null || true;
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
# playwright-cli (agent companion). Binary name has `-cli` suffix. The skill is
# installed per-repo at runtime on opt-in (see init.d/40-playwright-cli.sh) by
# copying the templates bundled in the package — `skills/playwright-cli/` and
# the `playwright-core` `cli-client/skill/` copy. Those template trees are
# .md-only, so the generic `*.md` weight prune below MUST spare them (the
# leading `-path … -prune -o`); without that exclusion the prune empties the
# skill source and `playwright-cli install --skills` produces an empty skill.
RUN --mount=type=cache,target=/root/.npm,id=npm-cache \
    set -eux; \
      npm install -g "@playwright/cli@${PLAYWRIGHT_CLI_VERSION}"; \
      playwright-cli --version; \
      find /usr/local/lib/node_modules/@playwright \
        -type d \( -path '*/cli/skills' -o -path '*/cli-client/skill' \) -prune -o \
        \( -type f \( -name '*.map' -o -name '*.md' -o -name '*.markdown' -o -name 'CHANGELOG*' -o -name 'AUTHORS*' \) \
        -o -type d \( -name docs -o -name examples -o -name example -o -name __tests__ -o -name .github \) \) \
        -prune -exec rm -rf {} +; \
      test -s /usr/local/lib/node_modules/@playwright/cli/node_modules/playwright-core/lib/tools/cli-client/skill/SKILL.md;
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
# CodeGraph (deterministic code-symbol graph: tree-sitter -> SQLite/FTS5,
# exposed as an MCP server + CLI). The npm meta-package is a thin shim; the
# matching @colbymchenry/codegraph-<os>-<arch> optional dep carries the
# bundled runtime + compiled library, so the prune is scoped to the shim
# package only — never the platform package that holds the binary.
RUN --mount=type=cache,target=/root/.npm,id=npm-cache \
    set -eux; \
      npm install -g "@colbymchenry/codegraph@${CODEGRAPH_VERSION}"; \
      codegraph --version; \
      find /usr/local/lib/node_modules/@colbymchenry/codegraph \
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
# TypeScript language server (TS/JS LSP for Claude Code typescript-lsp plugin).
# Global typescript alongside: the server resolves the workspace's own
# typescript when present, global tsserver is the fallback for checkouts
# without node_modules.
RUN --mount=type=cache,target=/root/.npm,id=npm-cache \
    set -eux; \
      npm install -g typescript@${TYPESCRIPT_VERSION} \
        typescript-language-server@${TYPESCRIPT_LANGUAGE_SERVER_VERSION}; \
      tsc --version; \
      typescript-language-server --version; \
      find /usr/local/lib/node_modules/typescript \
           /usr/local/lib/node_modules/typescript-language-server \
        \( -type f \( -name '*.map' -o -name '*.md' -o -name '*.markdown' -o -name 'CHANGELOG*' -o -name 'AUTHORS*' \) \
        -o -type d \( -name docs -o -name examples -o -name example -o -name __tests__ -o -name .github \) \) \
        -prune -exec rm -rf {} +;
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
# pnpm via corepack. Cache mount on COREPACK_HOME so the pnpm tarball is
# served from cache across this layer's frequent rebuilds (11 bumps/6mo).
RUN --mount=type=cache,target=/root/.cache/node/corepack,id=corepack-cache \
    set -eux; \
      corepack enable; \
      corepack prepare pnpm@${PNPM_VERSION} --activate; \
      pnpm --version;
# Precompute zsh completions to vendor-completions (Debian fpath default —
# the dir itself arrives via the fetch-stage COPYs above).
# tofu/gcloud excluded — their OMZ plugins handle completion separately.
# Fail-fast: a broken `completion zsh` subcommand fails the build.
RUN set -eux; \
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
# Claude Code CLI. Runtime env knobs live AFTER the RUN tail (see the
# "Claude Code env knobs" ENV block below): none of them affect the install,
# and ENV-before-RUN would bust this layer's cache on every knob tweak.
RUN --mount=type=cache,target=/root/.npm,id=npm-cache \
    set -eux; \
      npm install -g @anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}; \
      claude --version; \
      find /usr/local/lib/node_modules/@anthropic-ai \
        \( -type f \( -name '*.map' -o -name '*.md' -o -name '*.markdown' -o -name 'CHANGELOG*' -o -name 'AUTHORS*' \) \
        -o -type d \( -name docs -o -name examples -o -name example -o -name __tests__ -o -name .github \) \) \
        -prune -exec rm -rf {} +; \
      # DO_NOT_TRACK exemption: swap the npm symlink for a wrapper that
      # strips the var for the claude process only. Rationale: ENV comment
      # near the top of this stage; known cost: runtime-notes.
      target="$(readlink -f /usr/local/bin/claude)"; \
      rm /usr/local/bin/claude; \
      printf '#!/bin/sh\n# Wrapper: DO_NOT_TRACK kills the Statsig feature-flag channel\n# (Remote Control, preview rollouts) — strip it for claude only.\nexec env -u DO_NOT_TRACK "%s" "$@"\n' "$target" \
        > /usr/local/bin/claude; \
      chmod 0755 /usr/local/bin/claude; \
      claude --version;
# xterm-ghostty terminfo. ncurses 6.4 (bookworm) predates the entry added
# in 6.5. Bundled .src is a fully-resolved infocmp dump of the terminfo
# Ghostty itself ships (version in the .src provenance header) — no use=
# fragments to resolve.
RUN --mount=type=bind,source=xterm-ghostty.src,target=/tmp/xterm-ghostty.src \
    set -eux; \
    tic -x -o /usr/share/terminfo /tmp/xterm-ghostty.src
# System-level safe.directory for the root-owned Homebrew clone (variable
# host UID; ~/.gitconfig may be a RO host mount — must not be edited).
RUN set -eux; \
    git config --system --add safe.directory /home/linuxbrew/.linuxbrew/Homebrew; \
    test -x /home/linuxbrew/.linuxbrew/bin/brew

# User setup. Container runs --user <host-uid>:<host-gid>, so /home/toolbox
# made world-writable. Pre-create .cache/.config/.local/share so Docker
# doesn't auto-mkdir them as root:root 0755 (blocks sibling subdir writes).
# /etc/passwd + /etc/group world-writable: entrypoint injects runtime UID
# entry (OpenShift pattern; safe — ephemeral, no secrets).
# `usermod -d -m` relocates /home/node (incl. the fetch-omz clone) and
# refuses if /home/toolbox already exists.
RUN set -eux; \
    usermod -l toolbox node; \
    groupmod -n toolbox node; \
    usermod -d /home/toolbox -m toolbox; \
    mkdir -p \
      /home/toolbox/.npm-global \
      /home/toolbox/.toolbox-startup.d \
      /home/toolbox/.cache \
      /home/toolbox/.config \
      /home/toolbox/.local/share \
      /home/toolbox/.toolbox/bridge; \
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

# Claude Code env knobs — runtime-only (the claude binary reads them at
# exec; no RUN consumes them), so the block sits after the RUN tail where
# a knob tweak re-runs zero layers:
#   DISABLE_AUTOUPDATER: CLI binary lives under root-owned /usr/local/lib —
#     self-update would always fail. Renovate drives version bumps instead.
#   FORCE_AUTOUPDATE_PLUGINS: plugins live in user-writable ~/.claude/plugins
#     bind — update path works there, keep it on.
#   DISABLE_FEEDBACK/ERROR_REPORTING: granular privacy switches.
#     CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC umbrella correlates with
#     intermittent /login prompts on long-lived shells — use granular set
#     instead. DISABLE_TELEMETRY intentionally NOT set: the Statsig channel
#     doubles as feature-flag delivery, and blocking it is reported to break
#     Remote Control entitlements and preview rollouts
#     (anthropics/claude-code#58383); it is product analytics, not training
#     data — the model-improvement opt-out lives in the claude.ai account
#     privacy settings, not in this flag. Same reason the image-wide
#     DO_NOT_TRACK=1 is stripped for claude via the /usr/local/bin/claude
#     wrapper (install layer above): claude honors it as a telemetry
#     umbrella and it kills Statsig exactly like DISABLE_TELEMETRY would
#     (/doctor flags it).
#   CLAUDE_CONFIG_DIR: relocate .credentials.json + .claude.json into the
#     ~/.claude bind so OAuth survives container recreates.
#   USE_BUILTIN_RIPGREP=0: image ships apt ripgrep (base layer) — use it
#     instead of the npm-bundled binary (pin-everything policy).
#   CLAUDE_BASH_MAINTAIN_PROJECT_WORKING_DIR: return to the project dir
#     after every Bash call — kills cd-drift across tool invocations.
#   BASH_*_TIMEOUT_MS: 5m default / 20m ceiling (upstream 2m/10m) — image
#     builds and test suites run inside the container routinely exceed 2m.
#   CLAUDE_CODE_ENABLE_TASKS: structured cross-session task tools; state
#     lives in ~/.claude/tasks under the bind, survives recreates.
#   CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS: opt into agent teams (still
#     experimental upstream; in-process mode works without tmux). Per-user
#     opt-out: "env" map in the ~/.claude/settings.json bind overrides this.
ENV DISABLE_AUTOUPDATER=1 \
    FORCE_AUTOUPDATE_PLUGINS=1 \
    DISABLE_FEEDBACK_COMMAND=1 \
    DISABLE_ERROR_REPORTING=1 \
    CLAUDE_CONFIG_DIR=/home/toolbox/.claude \
    USE_BUILTIN_RIPGREP=0 \
    CLAUDE_BASH_MAINTAIN_PROJECT_WORKING_DIR=1 \
    BASH_DEFAULT_TIMEOUT_MS=300000 \
    BASH_MAX_TIMEOUT_MS=1200000 \
    CLAUDE_CODE_ENABLE_TASKS=1 \
    CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1

# LANG (not LC_ALL, so host env passthrough can still override the locale):
# debian-slim defaults to the POSIX locale, under which zsh's line editor
# cannot decode UTF-8 bytes (history entries, prompt glyphs) and renders each
# one as `<ffffffff>`. C.UTF-8 is built into glibc — no `locales` package.
ENV HOME=/home/toolbox \
    NPM_CONFIG_PREFIX=/home/toolbox/.npm-global \
    EDITOR=vi \
    KUBE_EDITOR=vi \
    STARSHIP_CONFIG=/etc/toolbox/starship.toml \
    LANG=C.UTF-8
ENV PATH="/home/toolbox/.npm-global/bin:${PATH}"
# Homebrew on PATH for non-interactive `docker exec` too; interactive zsh
# additionally evals `brew shellenv` (idempotent w.r.t. this entry) for
# HOMEBREW_PREFIX/MANPATH/INFOPATH. Analytics off (privacy posture, rtk
# telemetry-lockdown precedent); auto-update off (pin-everything policy —
# also avoids the shallow-clone `brew update` trip on every install).
ENV PATH="/home/linuxbrew/.linuxbrew/bin:/home/linuxbrew/.linuxbrew/sbin:${PATH}" \
    HOMEBREW_NO_ANALYTICS=1 \
    HOMEBREW_NO_AUTO_UPDATE=1

# Static config/assets. COPY --link: independent layers, never re-triggered
# by parent-layer changes; --chmod restores exec bits (embed.FS strips to 0444).
# zshrc overrides the file the zsh package ships at /etc/zsh/zshrc.
COPY --link zshrc.sh /etc/zsh/zshrc
# Curated starship config. Per-user override via STARSHIP_CONFIG in a
# ~/.toolbox-startup.d/ hook.
COPY --link starship.toml /etc/toolbox/starship.toml
# SSH host-key trust for git-over-ssh (issue #366). The ~/.ssh credential mount
# is read-only by design, so the Debian default (StrictHostKeyChecking=ask +
# read-only ~/.ssh/known_hosts) deadlocks: it prompts on first contact and
# can't persist the answer. accept-new + a writable known_hosts on the state
# mount fixes it without making the RO ssh mount writable. The smoke test
# asserts the policy is effective via `ssh -G`.
COPY --link ssh_config.toolbox /etc/ssh/ssh_config.d/10-toolbox.conf
# Guarantee the drop-in is loaded: ssh uses the first-obtained value per
# keyword, so the Include must sit at the top of /etc/ssh/ssh_config (ahead of
# any built-in default) for accept-new to win. Debian's base config has it
# there, but make it explicit + idempotent rather than silently depending on
# upstream not moving it.
RUN grep -qxF 'Include /etc/ssh/ssh_config.d/*.conf' /etc/ssh/ssh_config \
    || sed -i '1i Include /etc/ssh/ssh_config.d/*.conf' /etc/ssh/ssh_config
COPY --link --chmod=0755 entrypoint.sh /usr/local/bin/entrypoint
# Per-tool init manifest.
COPY --link --chmod=0755 init.d/ /usr/local/lib/toolbox/init.d/
# Bridge shim transport, sourced by the xdg-open/code/proximo shims
# below (state-dir location + readiness checks + curl POST in one place).
COPY --link --chmod=0644 bin/bridge-lib.sh /usr/local/lib/toolbox/bridge-lib.sh
# Browser-bridge wrapper. Shadows /usr/bin/xdg-open (+synonyms) so URL
# opens hand off to host daemon via host.docker.internal; without the daemon
# it degrades to printing the URL (cf/wrangler/any OAuth CLI shells out to
# xdg-open; container has no browser).
COPY --link --chmod=0755 bin/xdg-open /usr/local/bin/xdg-open
# Browser-bridge editor shims. `code`/`codium` (codium = symlink, editor
# inferred from basename) translate /workspace paths to host paths via
# TOOLBOX_HOST_WORKSPACE and hand them to the daemon's /edit endpoint so the
# host editor opens them (container ships no GUI editor).
COPY --link --chmod=0755 bin/code /usr/local/bin/code
# Browser-bridge proximo shim. proximo itself runs on the host (its stack
# bind-mounts host paths); the shim forwards up/down/status to the daemon's
# /proximo endpoint and propagates output + exit code. install/uninstall need
# interactive sudo and are rejected with a run-on-host hint.
COPY --link --chmod=0755 bin/proximo /usr/local/bin/proximo
# Git housekeeping helper. `git-*` on PATH is a git subcommand, so this is
# invoked as `git prune-dead`: deletes local branches with a gone upstream
# (squash-merged PRs) + merged worktree-agent-* leftovers, then prunes stale
# worktree entries. Never touches the current or default branch.
COPY --link --chmod=0755 bin/git-prune-dead /usr/local/bin/git-prune-dead
# proximo /etc/hosts sync. Re-discovers the proximo.hosts labels from the host
# daemon (mounted docker.sock) and rewrites a managed /etc/hosts block pinning
# each routed *.test name to the host-gateway — the runtime complement to the
# one-shot --add-host pins fixed at container create, so stacks started after
# `toolbox shell` become reachable without recreating the container.
COPY --link --chmod=0755 bin/proximo-hosts /usr/local/bin/proximo-hosts
RUN set -eux; \
    ln -sf /usr/local/bin/xdg-open /usr/local/bin/sensible-browser; \
    ln -sf /usr/local/bin/xdg-open /usr/local/bin/gnome-open; \
    ln -sf /usr/local/bin/xdg-open /usr/local/bin/x-www-browser; \
    ln -sf /usr/local/bin/xdg-open /usr/local/bin/www-browser; \
    ln -sf /usr/local/bin/xdg-open /usr/local/bin/open; \
    ln -sf /usr/local/bin/code /usr/local/bin/codium
ENV BROWSER=xdg-open

USER toolbox
WORKDIR /workspace
ENTRYPOINT ["/usr/bin/tini", "-g", "--", "/usr/local/bin/entrypoint"]
# See the fetch-tini stage comment for why CMD is `sleep infinity` rather
# than a shell.
CMD ["sleep", "infinity"]
