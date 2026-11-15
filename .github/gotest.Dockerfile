# Development-only runner for the Go suite: the pinned golang image plus the
# interpreters the shell-driven tests execute, which ci.yml's runner provides
# (jq preinstalled, zsh from its "Install test tools" step). Without them the
# local gate passes on tests it never ran.
ARG GO_IMAGE
FROM ${GO_IMAGE:-scratch}
RUN apt-get update -qq \
    && apt-get install -y --no-install-recommends jq zsh \
    && rm -rf /var/lib/apt/lists/*
