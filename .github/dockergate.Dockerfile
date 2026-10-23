# Development-only runner: reuse the repository's toolchain and runtime pins.
ARG GO_IMAGE
ARG RUNTIME_IMAGE
FROM ${RUNTIME_IMAGE:-scratch} AS runtime
FROM ${GO_IMAGE:-scratch}
COPY --from=runtime /usr/local/bin/docker /usr/local/bin/docker
RUN apt-get update -qq \
    && apt-get install -y --no-install-recommends python3 \
    && rm -rf /var/lib/apt/lists/*
