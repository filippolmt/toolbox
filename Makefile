# `make build` tags the runtime image directly with the canonical registry
# reference (see internal/build/tag.go::DefaultRegistryImage) so a freshly
# built image is the one `./toolbox shell` resolves locally without any
# retag step. CI / GHCR push the same tag — locally-built and registry-built
# share the namespace by design. Run `docker pull` to restore the upstream
# image after a local rebuild.
IMAGE := ghcr.io/filippolmt/toolbox
TAG   := latest
FULL  := $(IMAGE):$(TAG)

# Go toolchain runs inside a container so Go is not required on the host.
# A named Docker volume caches the module + build cache across runs.
GO_IMAGE        := golang:1.26
GOLANGCI_IMAGE  := golangci/golangci-lint:v2.12.2-alpine
GO_MOD_VOL      := toolbox-gomod
# When running inside a toolbox shell we are talking to the host daemon over
# the bind-mounted socket (DooD): the literal in-container path ($(CURDIR),
# usually /workspace) is not resolvable by that daemon. Fall back to the
# absolute host path exposed via TOOLBOX_HOST_WORKSPACE when it is set.
HOST_SRC        := $(if $(TOOLBOX_HOST_WORKSPACE),$(TOOLBOX_HOST_WORKSPACE),$(CURDIR))

# Shared docker-run fragments. Every Go-side target reuses GO_MOUNT and
# GO_BUILD_ENV; CGO is off by default (race detector opt-in adds it back).
GO_MOUNT     := -v "$(HOST_SRC)":/src -v $(GO_MOD_VOL):/go -w /src
GO_BUILD_ENV := -e GOFLAGS="-mod=mod -buildvcs=false"
GO_RUN       := docker run --rm $(GO_MOUNT) $(GO_BUILD_ENV) -e CGO_ENABLED=0 $(GO_IMAGE)

.PHONY: build test shell shell-bash clean help go-build go-test go-test-verbose go-lint go-shell go-clean-cache go-run go-run-clean

build: ## Build the toolbox runtime image (tag: ghcr.io/filippolmt/toolbox:latest)
	docker build -f internal/build/assets/Dockerfile -t $(FULL) internal/build/assets

test: build ## Build the image and run the smoke test
	internal/build/assets/smoke-test.sh $(FULL)

shell: build ## Build the image and open an interactive zsh shell in it
	docker run --rm -it $(FULL) zsh

shell-bash: build ## Build the image and open an interactive bash shell (override default zsh)
	docker run --rm -it $(FULL) bash

clean: ## Remove the toolbox image
	docker rmi $(FULL) 2>/dev/null || true

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*##' $(MAKEFILE_LIST) | awk -F ':.*## ' '{printf "  %-18s %s\n", $$1, $$2}'

# --- Go CLI (containerised) ---
BINARY := toolbox

# Cross-compile for the host so the binary is runnable after `make go-build`.
# The build still happens inside the Linux golang container, but GOOS/GOARCH
# target the host's platform (darwin/arm64 on M-series Macs, linux/amd64 on
# typical CI runners, …).
HOST_OS    := $(shell uname -s | tr '[:upper:]' '[:lower:]')
HOST_ARCH  := $(shell uname -m | sed -e 's/x86_64/amd64/' -e 's/aarch64/arm64/')

go-build: ## Build the Go CLI binary for the host platform
	$(GO_RUN) env GOOS=$(HOST_OS) GOARCH=$(HOST_ARCH) go build -o $(BINARY) .

go-test: ## Run Go tests inside a golang container
	$(GO_RUN) go test ./... -count=1

go-test-verbose: ## Run Go tests with -v and race detection (requires CGO)
	docker run --rm $(GO_MOUNT) $(GO_BUILD_ENV) $(GO_IMAGE) go test -v -race ./...

go-lint: ## Run golangci-lint inside a container
	docker run --rm $(GO_MOUNT) $(GOLANGCI_IMAGE) golangci-lint run ./...

go-shell: ## Open a shell in the golang container for ad-hoc commands
	docker run --rm -it $(GO_MOUNT) $(GO_BUILD_ENV) -e CGO_ENABLED=0 $(GO_IMAGE) bash

go-clean-cache: ## Remove the shared Go module/build cache volume
	docker volume rm $(GO_MOD_VOL) 2>/dev/null || true

# go-run / go-run-clean are HOST-ONLY dogfood targets. Building the CLI and
# then invoking it from inside a toolbox shell re-enters DooD and hits the
# same bind-mount surprise the CLI itself is trying to smooth over, so these
# are meant to be run from a native host terminal.
go-run: go-build ## Build the CLI and open a toolbox shell via the freshly built binary (host-only)
	./$(BINARY) shell

go-run-clean: go-build ## Like go-run but stop the existing container first so ContainerCreate-time changes (Env, mounts) take effect
	-./$(BINARY) stop
	./$(BINARY) shell
