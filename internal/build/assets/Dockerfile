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
ARG GH_VERSION=2.90.0
ARG GLAB_VERSION=1.92.1
ARG KUBECTL_VERSION=v1.35.4
ARG HELM_VERSION=v4.1.4
ARG TOFU_VERSION=1.11.6
ARG DOCKER_CLI_VERSION=29.4.0
ARG JQ_VERSION=1.8.1
ARG YQ_VERSION=4.53.2
ARG STARSHIP_VERSION=1.25.0
ARG PNPM_VERSION=10.33.0
ARG UV_VERSION=0.11.7
ARG CLAUDE_CODE_VERSION=2.1.114
ARG GCLOUD_VERSION=565.0.0
ARG AZURE_CLI_VERSION=2.85.0
ARG OCI_VERSION=3.79.0
ARG PLAYWRIGHT_VERSION=1.59.1
ARG PLAYWRIGHT_CLI_VERSION=0.1.8
ARG COMPOSE_VERSION=2.40.3

# -- Opt-out flags (one per tool, driven by tools.<key> in .toolbox.yaml) -----
ARG INSTALL_KUBECTL=true
ARG INSTALL_HELM=true
ARG INSTALL_TOFU=true
ARG INSTALL_GH=true
ARG INSTALL_GLAB=true
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
ENV CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1
RUN set -eux; \
    if [ "${INSTALL_CLAUDE_CODE}" != "true" ]; then \
      echo "Skipping Claude Code (tools.claude=false)"; \
    else \
      npm install -g @anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}; \
      claude --version; \
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
      ln -s /opt/google-cloud-sdk/bin/gcloud /usr/local/bin/gcloud; \
      ln -s /opt/google-cloud-sdk/bin/gsutil /usr/local/bin/gsutil; \
      ln -s /opt/google-cloud-sdk/bin/bq /usr/local/bin/bq; \
      gcloud --version; \
    fi

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

COPY entrypoint.sh /usr/local/bin/entrypoint
RUN chmod +x /usr/local/bin/entrypoint

# -- Layer 21: User setup (IMG-03) --------------------------------------------
# The container is launched with --user <host-uid>:<host-gid> (see
# internal/container/lifecycle.go) so bind-mounted host files retain correct
# ownership. The runtime uid usually won't match the "toolbox" uid baked here,
# so /home/toolbox is made writable to every uid.
RUN set -eux; \
    usermod -l toolbox node; \
    groupmod -n toolbox node; \
    usermod -d /home/toolbox -m toolbox; \
    mkdir -p /home/toolbox/.npm-global /home/toolbox/.toolbox-startup.d; \
    chmod -R a+rwX /home/toolbox

ENV HOME=/home/toolbox \
    NPM_CONFIG_PREFIX=/home/toolbox/.npm-global
ENV PATH="/home/toolbox/.npm-global/bin:${PATH}"
USER toolbox
WORKDIR /workspace
ENTRYPOINT ["/usr/local/bin/entrypoint"]
CMD ["bash"]
