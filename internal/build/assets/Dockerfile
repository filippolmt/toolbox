# ============================================================================
# Toolbox — containerized development environment
# Build:  docker build -f internal/build/assets/Dockerfile internal/build/assets
# Test:   make test
#
# Every optional tool below is guarded by an INSTALL_<TOOL>=true build arg.
# The toolbox CLI translates the user's `tools:` config into these build args.
# When INSTALL_<TOOL>=false, the layer is a no-op — the tool is not installed
# and its files are absent from the final image.
# ============================================================================

FROM node:24-bookworm-slim

# -- Version pinning (D-09) ---------------------------------------------------
# Bumps are driven by Renovate (see renovate.json for the per-ARG mapping).
# The verification steps below fetch the matching SHA256 from upstream at
# build time, so changing only the version here keeps checksums correct.
ARG TARGETARCH
ARG GH_VERSION=2.91.0
ARG GLAB_VERSION=1.92.1
ARG KUBECTL_VERSION=v1.35.4
ARG HELM_VERSION=v4.1.4
ARG TOFU_VERSION=1.11.6
ARG DOCKER_CLI_VERSION=29.4.1
ARG JQ_VERSION=1.8.1
ARG YQ_VERSION=4.53.2
ARG STARSHIP_VERSION=1.25.0
ARG PNPM_VERSION=10.33.0
ARG UV_VERSION=0.11.7
ARG CLAUDE_CODE_VERSION=2.1.117
ARG PYRIGHT_VERSION=1.1.409
ARG GCLOUD_VERSION=565.0.0
ARG AZURE_CLI_VERSION=2.85.0
ARG OCI_VERSION=3.80.0
ARG PLAYWRIGHT_VERSION=1.59.1
ARG PLAYWRIGHT_CLI_VERSION=0.1.8
ARG COMPOSE_VERSION=5.1.3
ARG GO_VERSION=1.26.2
ARG GOPLS_VERSION=0.21.1
ARG GOIMPORTS_VERSION=0.44.0
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
# Baseline SHAs verified 2026-04-20 from RESEARCH.md Standard Stack table;
# Renovate's first scan after merge will rewrite them to current HEAD.
ARG OMZ_COMMIT=349b9e49ced7682e27927ffb34b6522f011f3e74
ARG ZSH_AUTOSUGGESTIONS_COMMIT=85919cd1ffa7d2d5412f6d3fe437ebdbeeec4fc5
ARG ZSH_SYNTAX_HIGHLIGHTING_COMMIT=1d85c692615a25fe2293bdd44b34c217d5d2bf04
ARG ZSH_COMPLETIONS_COMMIT=47e40c923ca038b5a0259ea0460326c56344dc64
ARG FZF_TAB_COMMIT=0983009f8666f11e91a2ee1f88cfdb748d14f656
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
ARG INSTALL_GO=true
ARG INSTALL_DOCKER_CLI=true
ARG INSTALL_STARSHIP=true
ARG INSTALL_JQ=true
ARG INSTALL_YQ=true
ARG INSTALL_PNPM=true
ARG INSTALL_UV=true
ARG INSTALL_CLAUDE_CODE=true
ARG INSTALL_GCLOUD=true
ARG INSTALL_AZURE=true
ARG INSTALL_OCI=true
ARG INSTALL_PLAYWRIGHT=true
ARG INSTALL_PLAYWRIGHT_CLI=true
ARG INSTALL_COMPOSE=true
ARG INSTALL_ZSH=true
ARG INSTALL_BAT=true
ARG INSTALL_PYRIGHT=true

# -- Layer 1: apt-get (always-on base) ----------------------------------------
# These are transitive deps of almost every other tool (TLS, git clone, Python
# for uv/gcloud, bash completions). Kept unconditional so opt-outs don't have
# to carry cascade rules.
RUN set -eux; \
    apt-get update -qqy; \
    apt-get install -qqy --no-install-recommends \
      curl \
      ca-certificates \
      git \
      openssh-client \
      bash-completion \
      make \
      python3 \
      python3-pip \
      gnupg; \
    apt-get clean; \
    rm -rf /var/lib/apt/lists/*

# -- Layer 2: kubectl (TOOL-05, IMG-02) ---------------------------------------
RUN set -eux; \
    if [ "${INSTALL_KUBECTL}" != "true" ]; then \
      echo "Skipping kubectl (tools.kubectl=false)"; \
    else \
      curl -fsSLO "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${TARGETARCH}/kubectl"; \
      curl -fsSL -o kubectl.sha256 \
        "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${TARGETARCH}/kubectl.sha256"; \
      echo "$(cat kubectl.sha256)  kubectl" | sha256sum -c -; \
      chmod +x kubectl; \
      mv kubectl /usr/local/bin/kubectl; \
      rm kubectl.sha256; \
      kubectl version --client; \
    fi

# -- Layer 3: helm (TOOL-05, IMG-02) ------------------------------------------
RUN set -eux; \
    if [ "${INSTALL_HELM}" != "true" ]; then \
      echo "Skipping helm (tools.helm=false)"; \
    else \
      HELM_TARBALL="helm-${HELM_VERSION}-linux-${TARGETARCH}.tar.gz"; \
      curl -fsSL -o "/tmp/${HELM_TARBALL}" \
        "https://get.helm.sh/${HELM_TARBALL}"; \
      curl -fsSL -o /tmp/helm.sha256 \
        "https://get.helm.sh/${HELM_TARBALL}.sha256sum"; \
      cd /tmp && grep "${HELM_TARBALL}" helm.sha256 | sha256sum -c -; \
      tar -xzf "/tmp/${HELM_TARBALL}" -C /usr/local/bin "linux-${TARGETARCH}/helm" --strip-components=1; \
      rm "/tmp/${HELM_TARBALL}" /tmp/helm.sha256; \
      helm version; \
    fi

# -- Layer 4: tofu (TOOL-05, IMG-02) ------------------------------------------
RUN set -eux; \
    if [ "${INSTALL_TOFU}" != "true" ]; then \
      echo "Skipping tofu (tools.tofu=false)"; \
    else \
      TOFU_TARBALL="tofu_${TOFU_VERSION}_linux_${TARGETARCH}.tar.gz"; \
      curl -fsSL -o "/tmp/${TOFU_TARBALL}" \
        "https://github.com/opentofu/opentofu/releases/download/v${TOFU_VERSION}/${TOFU_TARBALL}"; \
      curl -fsSL -o /tmp/tofu_SHA256SUMS \
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
      curl -fsSL -o /tmp/gh.tar.gz \
        "https://github.com/cli/cli/releases/download/v${GH_VERSION}/gh_${GH_VERSION}_linux_${TARGETARCH}.tar.gz"; \
      curl -fsSL -o /tmp/gh_checksums.txt \
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
      curl -fsSL -o "/tmp/${GLAB_TARBALL}" \
        "https://gitlab.com/api/v4/projects/gitlab-org%2Fcli/packages/generic/glab/${GLAB_VERSION}/${GLAB_TARBALL}"; \
      curl -fsSL -o /tmp/glab_checksums.txt \
        "https://gitlab.com/api/v4/projects/gitlab-org%2Fcli/packages/generic/glab/${GLAB_VERSION}/checksums.txt"; \
      cd /tmp && grep "${GLAB_TARBALL}" glab_checksums.txt | sha256sum -c -; \
      tar -xzf "/tmp/${GLAB_TARBALL}" -C /usr/local/bin bin/glab --strip-components=1; \
      rm "/tmp/${GLAB_TARBALL}" /tmp/glab_checksums.txt; \
      glab version; \
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
      curl -fsSL -o /tmp/docker.tgz \
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
      curl -fsSL -o /tmp/starship.tar.gz \
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
      curl -fsSL -o /usr/local/bin/jq \
        "https://github.com/jqlang/jq/releases/download/jq-${JQ_VERSION}/jq-linux-${TARGETARCH}"; \
      curl -fsSL -o /tmp/jq_sha256 \
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
      curl -fsSL -o /usr/local/bin/yq \
        "https://github.com/mikefarah/yq/releases/download/v${YQ_VERSION}/yq_linux_${TARGETARCH}"; \
      curl -fsSL -o /tmp/yq_checksums \
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
      curl -fsSL -o "/tmp/${BAT_TARBALL}" \
        "https://github.com/sharkdp/bat/releases/download/v${BAT_VERSION}/${BAT_TARBALL}"; \
      echo "${BAT_SHA256}  /tmp/${BAT_TARBALL}" | sha256sum -c -; \
      tar -xzf "/tmp/${BAT_TARBALL}" -C /usr/local/bin --strip-components=1 \
        "bat-v${BAT_VERSION}-${BAT_ARCH}/bat"; \
      chmod +x /usr/local/bin/bat; \
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

# -- Layer 12: uv (TOOL-04) ---------------------------------------------------
# Migrated from the multi-stage `COPY --from=uv-source` pattern to a pinned
# tarball install so the layer can be gated by INSTALL_UV like the others.
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
      curl -fsSL -o "/tmp/${UV_TARBALL}" \
        "https://github.com/astral-sh/uv/releases/download/${UV_VERSION}/${UV_TARBALL}"; \
      curl -fsSL -o /tmp/uv.sha256 \
        "https://github.com/astral-sh/uv/releases/download/${UV_VERSION}/${UV_TARBALL}.sha256"; \
      EXPECTED=$(awk '{print $1}' /tmp/uv.sha256); \
      echo "${EXPECTED}  /tmp/${UV_TARBALL}" | sha256sum -c -; \
      tar -xzf "/tmp/${UV_TARBALL}" -C /usr/local/bin --strip-components=1 \
        "uv-${UV_ARCH}/uv" "uv-${UV_ARCH}/uvx"; \
      rm "/tmp/${UV_TARBALL}" /tmp/uv.sha256; \
      uv --version; \
    fi

# -- Layer 13: Claude Code (TOOL-01) ------------------------------------------
# Disable auto-update: image versions are intentionally pinned; toolbox user lacks
# write access to /usr/local/lib/node_modules (installed as root).
#
# CLAUDE_CONFIG_DIR relocates both .credentials.json and .claude.json into the
# single bind-mounted /home/toolbox/.claude dir. Without it, .claude.json lives
# at $HOME/.claude.json — outside the mount — so OAuth session state is lost
# on every container recreate and the user has to /login each shell.
ENV CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1 \
    CLAUDE_CONFIG_DIR=/home/toolbox/.claude
RUN set -eux; \
    if [ "${INSTALL_CLAUDE_CODE}" != "true" ]; then \
      echo "Skipping Claude Code (tools.claude=false)"; \
    else \
      npm install -g @anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}; \
      claude --version; \
    fi

# -- Layer 13a: Pyright language server (TOOL-01) ----------------------------
# Python LSP used by the pyright-lsp Claude Code plugin. Installed as a global
# npm package so `pyright-langserver` lands in /usr/local/bin without needing
# pipx/venv wiring. Gated by INSTALL_PYRIGHT (tools.pyright).
RUN set -eux; \
    if [ "${INSTALL_PYRIGHT}" != "true" ]; then \
      echo "Skipping pyright (tools.pyright=false)"; \
    else \
      npm install -g pyright@${PYRIGHT_VERSION}; \
      pyright --version; \
    fi

# -- Layer 14: gcloud (TOOL-02, IMG-02) ---------------------------------------
# NOTE: Google does not publish per-version SHA256 files for Cloud SDK archives
# (the docs list checksums only for the unversioned "latest" URL, which shift on
# every release). Accepted risk following the DOCKER_CLI_VERSION precedent
# (T-01-08): mitigated by HTTPS + version pinning.
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
      curl -fsSL -o "/tmp/${GCLOUD_TARBALL}" \
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
    fi

# -- Layer 14a: Go toolchain + gopls + goimports (GO-01, GO-02, GO-03) --------
# Single RUN gated by INSTALL_GO: downloads the Go tarball (SHA256-verified
# against the hash published in go.dev's release JSON index), extracts to
# /usr/local/go, then runs `go install` for gopls (LSP used by Claude Code)
# and goimports (formatter).
#
# Pattern rationale (see .planning/phases/05-go-toolchain-lsp/05-CONTEXT.md):
#   - D-03: tarball -> verify -> extract -> go install (gopls + goimports) -> cleanup, all in one RUN
#   - D-06: -ldflags="-s -w" strips debug info + symbol table on gopls/goimports (~30% smaller)
#   - D-08/D-09: no ENV GOPATH / GOROOT -- Go defaults resolve correctly
#     (GOPATH=$HOME/go=/home/toolbox/go matches the bind-mount target;
#     GOROOT is auto-detected from /usr/local/go/bin/go exec path).
#   - D-10: HOME=/tmp/go-install applies ONLY to this RUN's `go install`
#     (inline var), so the global HOME=/home/toolbox (Layer 21) stays intact.
#
# SHA256 source deviation (Rule 1, documented in 05-01-SUMMARY.md):
# The plan assumed `https://go.dev/dl/<tarball>.sha256` returns a raw hash,
# but that URL currently redirects to the HTML documentation page
# (meta refresh to /dl/#<tarball>.sha256). The hash is still published by
# upstream -- canonically in the JSON index at https://go.dev/dl/?mode=json.
# We fetch the JSON, extract the sha256 via jq (installed in Layer 9), then
# verify with `sha256sum -c -`. Same trust anchor (HTTPS to go.dev), same
# cryptographic guarantee.
#
# Trigger: Claude Code LSP failing with 'Executable not found in $PATH: "gopls"'.
RUN set -eux; \
    if [ "${INSTALL_GO}" != "true" ]; then \
      echo "Skipping Go toolchain (tools.go=false)"; \
    else \
      GO_TARBALL="go${GO_VERSION}.linux-${TARGETARCH}.tar.gz"; \
      curl -fsSL -o "/tmp/${GO_TARBALL}" \
        "https://go.dev/dl/${GO_TARBALL}"; \
      curl -fsSL "https://go.dev/dl/?mode=json&include=all" \
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
      HOME=/tmp/go-install \
      GOPATH=/tmp/go-install \
      GOCACHE=/tmp/go-install/cache \
      GOMODCACHE=/tmp/go-install/pkg/mod \
      GOBIN=/usr/local/bin \
      /usr/local/go/bin/go install -ldflags="-s -w" \
        "golang.org/x/tools/gopls@v${GOPLS_VERSION}"; \
      HOME=/tmp/go-install \
      GOPATH=/tmp/go-install \
      GOCACHE=/tmp/go-install/cache \
      GOMODCACHE=/tmp/go-install/pkg/mod \
      GOBIN=/usr/local/bin \
      /usr/local/go/bin/go install -ldflags="-s -w" \
        "golang.org/x/tools/cmd/goimports@v${GOIMPORTS_VERSION}"; \
      rm -rf /tmp/go-install; \
      /usr/local/go/bin/go version; \
      gopls version; \
      # goimports at runtime invokes `go list`, so it needs `go` on PATH.
      # The ENV PATH that prepends /usr/local/go/bin is applied by the next
      # Dockerfile line (after this RUN), so a functional invocation like
      # `echo "" | goimports` would fail here. Presence-check is enough —
      # smoke-test.sh runs the functional check against the final image.
      test -x /usr/local/bin/goimports; \
    fi

# Go binaries in PATH for every subsequent layer + runtime.
# Applied unconditionally: when INSTALL_GO=false the path simply resolves
# to a non-existent dir and has no effect (matches npm-global PATH idiom).
ENV PATH="/usr/local/go/bin:${PATH}"

# -- Layer 15: Azure CLI (TOOL-02, IMG-02) ------------------------------------
# Installed from Microsoft's Debian apt repository (the upstream-recommended
# path — no standalone binary release is published). Version pinned against
# the repo; trust anchor is the Microsoft GPG key on the repo metadata.
RUN set -eux; \
    if [ "${INSTALL_AZURE}" != "true" ]; then \
      echo "Skipping azure-cli (tools.azure=false)"; \
    else \
      install -d -m 0755 /etc/apt/keyrings; \
      curl -fsSL https://packages.microsoft.com/keys/microsoft.asc \
        | gpg --dearmor -o /etc/apt/keyrings/microsoft.gpg; \
      echo "deb [arch=${TARGETARCH} signed-by=/etc/apt/keyrings/microsoft.gpg] https://packages.microsoft.com/repos/azure-cli/ bookworm main" \
        > /etc/apt/sources.list.d/azure-cli.list; \
      apt-get update -qqy; \
      apt-get install -qqy --no-install-recommends "azure-cli=${AZURE_CLI_VERSION}-1~bookworm"; \
      apt-get clean; \
      rm -rf /var/lib/apt/lists/*; \
      az --version; \
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
RUN set -eux; \
    if [ "${INSTALL_PLAYWRIGHT}" != "true" ]; then \
      echo "Skipping playwright (tools.playwright=false)"; \
    else \
      npm install -g "playwright@${PLAYWRIGHT_VERSION}"; \
      playwright --version; \
      apt-get update -qqy; \
      playwright install-deps chromium; \
      apt-get clean; \
      rm -rf /var/lib/apt/lists/*; \
    fi

# -- Layer 18: Playwright CLI with SKILLS (TOOL-01) ---------------------------
# Agent-oriented companion to the `playwright` CLI above. Installs as the
# `playwright-cli` binary (note the `-cli` suffix — different from the
# `playwright` binary in Layer 17). Ships pre-made Claude Code skills for
# browser automation; install them at runtime with `playwright-cli install
# --skills`. Kept as a separate opt-out because not everyone using Playwright
# wants the agent flow, and vice-versa.
RUN set -eux; \
    if [ "${INSTALL_PLAYWRIGHT_CLI}" != "true" ]; then \
      echo "Skipping @playwright/cli (tools.playwright_cli=false)"; \
    else \
      npm install -g "@playwright/cli@${PLAYWRIGHT_CLI_VERSION}"; \
      playwright-cli --version; \
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
      curl -fsSL -o /usr/local/libexec/docker/cli-plugins/docker-compose \
        "https://github.com/docker/compose/releases/download/v${COMPOSE_VERSION}/${COMPOSE_BIN}"; \
      curl -fsSL -o /tmp/compose.sha256 \
        "https://github.com/docker/compose/releases/download/v${COMPOSE_VERSION}/${COMPOSE_BIN}.sha256"; \
      EXPECTED=$(awk '{print $1}' /tmp/compose.sha256); \
      echo "${EXPECTED}  /usr/local/libexec/docker/cli-plugins/docker-compose" | sha256sum -c -; \
      chmod +x /usr/local/libexec/docker/cli-plugins/docker-compose; \
      rm /tmp/compose.sha256; \
      /usr/local/libexec/docker/cli-plugins/docker-compose version; \
    fi

# -- Layer 20: COPY scripts ---------------------------------------------------
COPY bashrc.sh /etc/toolbox-bashrc.sh
RUN set -eux; \
    echo '' >> /etc/bash.bashrc; \
    echo '# toolbox shell UX (alias + completions + starship)' >> /etc/bash.bashrc; \
    echo '[ -f /etc/toolbox-bashrc.sh ] && source /etc/toolbox-bashrc.sh' \
      >> /etc/bash.bashrc

# -- Layer 20a: tini (PID 1 init / signal handler) ----------------------------
# zsh is the CMD and becomes PID 1 via `exec "$@"` in entrypoint.sh. Interactive
# shells (zsh/bash) ignore SIGTERM when they own the TTY with job control, so
# `docker stop` hangs until the 10s timeout and falls back to SIGKILL. tini is
# a minimal init that forwards signals properly and reaps zombies. `-g`
# forwards signals to the entire process group (not just the direct child), so
# shells receive SIGTERM and exit cleanly.
#
# Not opt-out: the binary is ~400 KB and correcting PID-1 semantics is a
# cross-cutting concern (every shell/exec/attach path benefits).
RUN set -eux; \
    curl -fsSL -o /usr/bin/tini \
      "https://github.com/krallin/tini/releases/download/v${TINI_VERSION}/tini-static-${TARGETARCH}"; \
    curl -fsSL -o /tmp/tini.sha256sum \
      "https://github.com/krallin/tini/releases/download/v${TINI_VERSION}/tini-static-${TARGETARCH}.sha256sum"; \
    EXPECTED=$(awk '{print $1}' /tmp/tini.sha256sum); \
    echo "${EXPECTED}  /usr/bin/tini" | sha256sum -c -; \
    chmod +x /usr/bin/tini; \
    rm /tmp/tini.sha256sum; \
    /usr/bin/tini --version

COPY entrypoint.sh /usr/local/bin/entrypoint
RUN chmod +x /usr/local/bin/entrypoint

# -- Layer 20b: zsh + Oh-My-Zsh + 4 custom plugins (ZSH-01/02/03/04/07) -------
# One RUN layer covers: apt-install zsh+fzf, clone OMZ at pinned SHA, clone the
# 3 custom plugins + fzf-tab at pinned SHAs, install zshrc.sh system-wide.
#
# The COPY is UNCONDITIONAL and stages to /etc/toolbox-zshrc.sh (mirrors the
# Layer 20 /etc/toolbox-bashrc.sh pattern). It CANNOT go directly to
# /etc/zsh/zshrc because /etc/zsh/ does not exist in node:24-bookworm-slim
# until `apt-get install zsh` creates it — so the gated RUN either (a) rm's
# the staged file on opt-out, or (b) `mkdir -p /etc/zsh && mv` into place on
# opt-in after the apt install has run.
#
# `.git` is retained (per R-05): SPEC ZSH-02 acceptance asserts
# `git -C /home/toolbox/.oh-my-zsh rev-parse HEAD` equals OMZ_COMMIT.
# Delta ~24 MB, within the 50 MB budget documented in SPEC constraints.
#
# IMPORTANT — home directory: we write into `/home/node/.oh-my-zsh` (the base
# image's current home for the `node` user) NOT `/home/toolbox/`. Layer 21
# runs `usermod -d /home/toolbox -m toolbox` which relocates the home dir and
# its contents. `usermod -m` refuses to move if `/home/toolbox` already
# exists, so any directory we create under the target path here would break
# the user-setup step. End state after Layer 21: `/home/toolbox/.oh-my-zsh`.
#
# Pattern: `git init + remote add + fetch --depth=1 <sha> + checkout FETCH_HEAD`.
# `git clone --depth=1 --branch <sha>` FAILS — --branch accepts named refs only.
# GitHub enables `uploadpack.allowReachableSHA1InWant=true`, so full-SHA fetches
# work. Abbreviated SHAs do NOT work — always use the full 40-char form.
COPY zshrc.sh /etc/toolbox-zshrc.sh
RUN set -eux; \
    if [ "${INSTALL_ZSH}" != "true" ]; then \
      echo "Skipping zsh + Oh-My-Zsh (tools.zsh=false)"; \
      rm -f /etc/toolbox-zshrc.sh; \
    else \
      apt-get update -qqy; \
      apt-get install -qqy --no-install-recommends zsh fzf; \
      apt-get clean; rm -rf /var/lib/apt/lists/*; \
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
      mv /etc/toolbox-zshrc.sh /etc/zsh/zshrc; \
      echo '# managed by toolbox — see /etc/zsh/zshrc' > /home/node/.zshrc; \
      zsh --version; \
      fzf --version; \
    fi

# -- Layer 20c: zoxide binary (ZSH-07) ----------------------------------------
# Pattern variant: tarball + SHA256 verification against a build-arg pinned
# hash (NOT a per-release checksums file — zoxide v0.9.9 upstream does not
# publish one; see the Rule 1 deviation in 04-03-SUMMARY.md). Both arches use
# musl (static binary — glibc availability on x86_64 is irrelevant and upstream
# only publishes musl for arm64).
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
      curl -fsSL -o "/tmp/${ZOXIDE_TARBALL}" \
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
      ls /usr/share/zsh/vendor-completions; \
    fi

# -- Layer 21: User setup (IMG-03) --------------------------------------------
# The container is launched with --user <host-uid>:<host-gid> (see
# internal/container/lifecycle.go) so bind-mounted host files retain correct
# ownership. The runtime uid usually won't match the "toolbox" uid baked here,
# so /home/toolbox is made writable to every uid.
#
# .cache and .config are pre-created here because Docker would otherwise
# create them as root:root 0755 at runtime (as the parent of the
# playwright-cache / gh / glab-cli / gcloud bind mounts), blocking any
# tool that writes a sibling subdir — e.g. starship under ~/.cache,
# helm under ~/.config.
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
      /home/toolbox/.config; \
    chmod -R a+rwX /home/toolbox; \
    chmod 0666 /etc/passwd /etc/group

ENV HOME=/home/toolbox \
    NPM_CONFIG_PREFIX=/home/toolbox/.npm-global
ENV PATH="/home/toolbox/.npm-global/bin:${PATH}"
USER toolbox
WORKDIR /workspace
ENTRYPOINT ["/usr/bin/tini", "-g", "--", "/usr/local/bin/entrypoint"]
CMD ["zsh"]
