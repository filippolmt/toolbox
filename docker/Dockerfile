# ============================================================================
# Toolbox — ambiente di sviluppo containerizzato
# Build: docker build -f docker/Dockerfile .
# ============================================================================

FROM ghcr.io/astral-sh/uv:0.11.7 AS uv-source

FROM node:22-bookworm-slim

# -- Version pinning (D-09) ---------------------------------------------------
ARG TARGETARCH
ARG GH_VERSION=2.90.0
ARG GLAB_VERSION=1.92.1
ARG KUBECTL_VERSION=v1.35.4
ARG HELM_VERSION=v4.1.4
ARG TOFU_VERSION=1.11.6
ARG DOCKER_CLI_VERSION=29.4.0
ARG JQ_VERSION=1.8.1
ARG YQ_VERSION=4.52.5
ARG STARSHIP_VERSION=1.24.2
ARG PNPM_VERSION=10.33.0

# -- Layer 1: apt-get ---------------------------------------------------------
RUN set -eux; \
    apt-get update -qqy; \
    apt-get install -qqy --no-install-recommends \
      curl \
      ca-certificates \
      git \
      bash-completion \
      python3 \
      python3-pip \
      gnupg; \
    apt-get clean; \
    rm -rf /var/lib/apt/lists/*

# -- Layer 2: kubectl (TOOL-05, IMG-02) ---------------------------------------
RUN set -eux; \
    curl -fsSLO "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${TARGETARCH}/kubectl"; \
    curl -fsSL -o kubectl.sha256 \
      "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${TARGETARCH}/kubectl.sha256"; \
    echo "$(cat kubectl.sha256)  kubectl" | sha256sum -c -; \
    chmod +x kubectl; \
    mv kubectl /usr/local/bin/kubectl; \
    rm kubectl.sha256; \
    kubectl version --client

# -- Layer 3: helm (TOOL-05, IMG-02) ------------------------------------------
RUN set -eux; \
    HELM_TARBALL="helm-${HELM_VERSION}-linux-${TARGETARCH}.tar.gz"; \
    curl -fsSL -o "/tmp/${HELM_TARBALL}" \
      "https://get.helm.sh/${HELM_TARBALL}"; \
    curl -fsSL -o /tmp/helm.sha256 \
      "https://get.helm.sh/${HELM_TARBALL}.sha256sum"; \
    cd /tmp && grep "${HELM_TARBALL}" helm.sha256 | sha256sum -c -; \
    tar -xzf "/tmp/${HELM_TARBALL}" -C /usr/local/bin "linux-${TARGETARCH}/helm" --strip-components=1; \
    rm "/tmp/${HELM_TARBALL}" /tmp/helm.sha256; \
    helm version

# -- Layer 4: tofu (TOOL-05, IMG-02) ------------------------------------------
RUN set -eux; \
    TOFU_TARBALL="tofu_${TOFU_VERSION}_linux_${TARGETARCH}.tar.gz"; \
    curl -fsSL -o "/tmp/${TOFU_TARBALL}" \
      "https://github.com/opentofu/opentofu/releases/download/v${TOFU_VERSION}/${TOFU_TARBALL}"; \
    curl -fsSL -o /tmp/tofu_SHA256SUMS \
      "https://github.com/opentofu/opentofu/releases/download/v${TOFU_VERSION}/tofu_${TOFU_VERSION}_SHA256SUMS"; \
    cd /tmp && grep "${TOFU_TARBALL}" tofu_SHA256SUMS | sha256sum -c -; \
    tar -xzf "/tmp/${TOFU_TARBALL}" -C /usr/local/bin tofu; \
    rm "/tmp/${TOFU_TARBALL}" /tmp/tofu_SHA256SUMS; \
    tofu version

# -- Layer 5: gh (TOOL-02, IMG-02) --------------------------------------------
RUN set -eux; \
    curl -fsSL -o /tmp/gh.tar.gz \
      "https://github.com/cli/cli/releases/download/v${GH_VERSION}/gh_${GH_VERSION}_linux_${TARGETARCH}.tar.gz"; \
    curl -fsSL -o /tmp/gh_checksums.txt \
      "https://github.com/cli/cli/releases/download/v${GH_VERSION}/gh_${GH_VERSION}_checksums.txt"; \
    grep "gh_${GH_VERSION}_linux_${TARGETARCH}.tar.gz" /tmp/gh_checksums.txt \
      | awk '{print $1"  /tmp/gh.tar.gz"}' | sha256sum -c -; \
    tar -xzf /tmp/gh.tar.gz -C /usr/local --strip-components=1 "gh_${GH_VERSION}_linux_${TARGETARCH}/bin/gh"; \
    rm /tmp/gh.tar.gz /tmp/gh_checksums.txt; \
    gh --version

# -- Layer 6: glab (TOOL-02, IMG-02) ------------------------------------------
RUN set -eux; \
    GLAB_TARBALL="glab_${GLAB_VERSION}_linux_${TARGETARCH}.tar.gz"; \
    curl -fsSL -o "/tmp/${GLAB_TARBALL}" \
      "https://gitlab.com/api/v4/projects/gitlab-org%2Fcli/packages/generic/glab/${GLAB_VERSION}/${GLAB_TARBALL}"; \
    curl -fsSL -o /tmp/glab_checksums.txt \
      "https://gitlab.com/api/v4/projects/gitlab-org%2Fcli/packages/generic/glab/${GLAB_VERSION}/checksums.txt"; \
    cd /tmp && grep "${GLAB_TARBALL}" glab_checksums.txt | sha256sum -c -; \
    tar -xzf "/tmp/${GLAB_TARBALL}" -C /usr/local/bin bin/glab --strip-components=1; \
    rm "/tmp/${GLAB_TARBALL}" /tmp/glab_checksums.txt; \
    glab version

# -- Layer 7: Docker CLI standalone (TOOL-06, IMG-02) -------------------------
# NOTE: Docker does not publish per-artifact .sha256 files for static binaries
# (download.docker.com returns 404). Checksum verification is not possible here.
# Accepted risk: T-01-08. Mitigated by HTTPS + version pinning.
RUN set -eux; \
    case "${TARGETARCH}" in amd64) DOCKER_ARCH=x86_64;; arm64) DOCKER_ARCH=aarch64;; *) DOCKER_ARCH=${TARGETARCH};; esac; \
    curl -fsSL -o /tmp/docker.tgz \
      "https://download.docker.com/linux/static/stable/${DOCKER_ARCH}/docker-${DOCKER_CLI_VERSION}.tgz"; \
    tar -xzf /tmp/docker.tgz -C /usr/local/bin --strip-components=1 docker/docker; \
    rm /tmp/docker.tgz; \
    docker --version

# -- Layer 8: starship (D-05) -------------------------------------------------
RUN set -eux; \
    case "${TARGETARCH}" in \
      amd64) STARSHIP_TARBALL="starship-x86_64-unknown-linux-gnu.tar.gz";; \
      arm64) STARSHIP_TARBALL="starship-aarch64-unknown-linux-musl.tar.gz";; \
      *) echo "Unsupported arch: ${TARGETARCH}" && exit 1;; \
    esac; \
    curl -fsSL -o /tmp/starship.tar.gz \
      "https://github.com/starship/starship/releases/download/v${STARSHIP_VERSION}/${STARSHIP_TARBALL}"; \
    tar -xzf /tmp/starship.tar.gz -C /usr/local/bin starship; \
    rm /tmp/starship.tar.gz; \
    starship --version

# -- Layer 9: jq (TOOL-07, IMG-02) --------------------------------------------
RUN set -eux; \
    curl -fsSL -o /usr/local/bin/jq \
      "https://github.com/jqlang/jq/releases/download/jq-${JQ_VERSION}/jq-linux-${TARGETARCH}"; \
    curl -fsSL -o /tmp/jq_sha256 \
      "https://github.com/jqlang/jq/releases/download/jq-${JQ_VERSION}/sha256sum.txt"; \
    grep "jq-linux-${TARGETARCH}$" /tmp/jq_sha256 \
      | awk '{print $1"  /usr/local/bin/jq"}' | sha256sum -c -; \
    chmod +x /usr/local/bin/jq; \
    rm /tmp/jq_sha256; \
    jq --version

# -- Layer 10: yq (TOOL-07, IMG-02) -------------------------------------------
# yq checksums file contains 31 hash algorithms per binary (see checksums_hashes_order).
# Field layout: $1=filename, $2=CRC32, ..., $19=SHA-256, ..., $32=BLAKE2B.
# We extract the SHA-256 column ($19) which is algorithm #18 in checksums_hashes_order.
RUN set -eux; \
    curl -fsSL -o /usr/local/bin/yq \
      "https://github.com/mikefarah/yq/releases/download/v${YQ_VERSION}/yq_linux_${TARGETARCH}"; \
    curl -fsSL -o /tmp/yq_checksums \
      "https://github.com/mikefarah/yq/releases/download/v${YQ_VERSION}/checksums"; \
    EXPECTED=$(grep "^yq_linux_${TARGETARCH} " /tmp/yq_checksums | awk '{print $19}'); \
    test -n "${EXPECTED}" || { echo "ERROR: SHA-256 hash not found in checksums file"; exit 1; }; \
    echo "${EXPECTED}  /usr/local/bin/yq" | sha256sum -c -; \
    chmod +x /usr/local/bin/yq; \
    rm /tmp/yq_checksums; \
    yq --version

# -- Layer 11: pnpm via corepack (TOOL-03) ------------------------------------
RUN set -eux; \
    corepack enable; \
    corepack prepare pnpm@${PNPM_VERSION} --activate; \
    pnpm --version

# -- Layer 12: uv via COPY --from (TOOL-04) -----------------------------------
COPY --from=uv-source /uv /uvx /usr/local/bin/

# -- Layer 13: Claude Code (TOOL-01) ------------------------------------------
RUN set -eux; \
    npm install -g @anthropic-ai/claude-code; \
    claude --version

# -- Layer 14: COPY scripts ---------------------------------------------------
COPY docker/bashrc.sh /etc/toolbox-bashrc.sh
RUN set -eux; \
    echo '' >> /etc/bash.bashrc; \
    echo '# toolbox shell UX (alias + completions + starship)' >> /etc/bash.bashrc; \
    echo '[ -f /etc/toolbox-bashrc.sh ] && source /etc/toolbox-bashrc.sh' \
      >> /etc/bash.bashrc

COPY docker/entrypoint.sh /usr/local/bin/entrypoint
RUN chmod +x /usr/local/bin/entrypoint

# -- Layer 15: User setup (IMG-03) --------------------------------------------
RUN set -eux; \
    usermod -l toolbox node; \
    groupmod -n toolbox node; \
    usermod -d /home/toolbox -m toolbox

USER toolbox
WORKDIR /workspace
ENTRYPOINT ["/usr/local/bin/entrypoint"]
CMD ["bash"]
