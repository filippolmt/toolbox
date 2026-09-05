# `make build` tags the runtime image directly with the canonical registry
# reference (see internal/imageref/imageref.go::DefaultRegistryImage) so a freshly
# built image is the one `./toolbox shell` resolves locally without any
# retag step. CI / GHCR push the same tag — locally-built and registry-built
# share the namespace by design. Run `docker pull` to restore the upstream
# image after a local rebuild.
IMAGE := ghcr.io/filippolmt/toolbox
TAG   := latest
FULL  := $(IMAGE):$(TAG)
# BuildKit registry cache published by CI (docker-publish.yml, mode=max,
# multi-arch — includes the arm64 rtk cargo build). Seeding from it makes the
# first local build (or a rebuild after an upstream bump) mostly a layer pull.
# Cache-import failures are non-fatal warnings, so offline builds still work.
# Override: `make build CACHE_REF=...`.
CACHE_REF := $(IMAGE):buildcache-main

# Go toolchain runs inside a container so Go is not required on the host.
# A named Docker volume caches the module + build cache across runs.
#
# GO_VERSION is the Go toolchain the *runtime image* is built with: the
# `toolchain` directive in go.mod (Renovate-bumped by default), falling back to
# the `go` directive when no toolchain line is present. It reaches the image as
# a --build-arg naming a tarball on go.dev, which exists the moment the release
# does — same for CI's setup-go, which resolves go.mod against the Go release
# index. So this stays derived.
GO_VERSION      := $(shell awk '/^toolchain /{v=substr($$2,3)} /^go /{if(!g)g=$$2} END{print (v?v:g)}' go.mod)
# GO_IMAGE is deliberately NOT derived from GO_VERSION. Docker Hub publishes a
# golang:<patch> tag days after the Go release lands in the index, so deriving
# the tag turned a green Renovate go.mod bump into a broken `make go-test` on
# main (`golang:1.26.6: not found` while go.mod already said go1.26.6). Pinning
# it here puts the tag under Renovate's *docker* datasource, which can only
# open the bump once the image actually exists. A gap between the two is
# harmless: GOTOOLCHAIN fetches the newer toolchain inside the container.
# Bumped by the GO_IMAGE_VERSION customManager in renovate.json (docker
# datasource, package golang) — that manager is what does the work; do not add
# an inline `# renovate:` directive here, it would be a second, dead mechanism.
GO_IMAGE_VERSION := 1.27.1
GO_IMAGE        := golang:$(GO_IMAGE_VERSION)
GOLANGCI_VERSION := v2.13.2
GOLANGCI_IMAGE  := golangci/golangci-lint:$(GOLANGCI_VERSION)-alpine
GO_MOD_VOL      := toolbox-gomod
# When running inside a toolbox shell we are talking to the host daemon over
# the bind-mounted socket (DooD): the literal in-container path ($(CURDIR),
# usually /workspace) is not resolvable by that daemon. Fall back to the
# absolute host path exposed via TOOLBOX_HOST_WORKSPACE when it is set.
# TOOLBOX_HOST_WORKSPACE is the host path of the *workspace root* — the
# directory `toolbox shell` was opened from — which is the repo only when the
# shell was opened here. Open it one level up and every containerised target
# would mount the parent and fail with a misleading "module not found" /
# "README.md not found". /workspace is that root inside the container, so the
# suffix $(CURDIR) carries below it is exactly what the host path is missing.
# Both conditions are needed, and each covers a case the other cannot:
#   - the /workspace prefix, because a CURDIR outside /workspace is already a
#     host path — the workspace mirror bind (mountplan.WorkspaceMirrorPath) is
#     the shell's WorkingDir whenever it exists, so `cd` below it leaves CURDIR
#     host-valid — and rewriting it to the workspace root is what mounted the
#     parent and produced the misleading failure;
#   - TOOLBOX_HOST_WORKSPACE being set, because in a container predating that
#     var there is nothing to prefix with, and the translation would yield the
#     bare $(patsubst) remainder (`/workspace` alone yields the empty string,
#     i.e. `-v "":/src`).
# Neither holding, CURDIR is the honest answer.
HOST_SRC        := $(if $(and $(TOOLBOX_HOST_WORKSPACE),$(filter /workspace%,$(CURDIR))),$(TOOLBOX_HOST_WORKSPACE)$(patsubst /workspace%,%,$(CURDIR)),$(CURDIR))

# Shared docker-run fragments. Every Go-side target reuses GO_MOUNT and
# GO_BUILD_ENV; CGO is off by default (race detector opt-in adds it back).
GO_MOUNT     := -v "$(HOST_SRC)":/src -v $(GO_MOD_VOL):/go -w /src
GO_BUILD_ENV := -e GOFLAGS="-mod=mod -buildvcs=false"
GO_RUN       := docker run --rm $(GO_MOUNT) $(GO_BUILD_ENV) -e CGO_ENABLED=0 $(GO_IMAGE)

.PHONY: build test shell shell-bash clean help go-build go-build-macos go-test go-test-verbose go-lint go-check go-shell go-clean-cache go-run go-run-clean check-links update-skills

build: ## Build the toolbox runtime image (tag: ghcr.io/filippolmt/toolbox:latest)
	docker buildx build -f internal/build/assets/Dockerfile -t $(FULL) \
	  --build-arg GO_VERSION=$(GO_VERSION) \
	  --cache-from type=registry,ref=$(CACHE_REF) \
	  --load internal/build/assets

test: build ## Build the image and run the smoke test
	internal/build/assets/smoke-test.sh $(FULL)

shell: build ## Build the image and open an interactive zsh shell in it
	docker run --rm -it $(FULL) zsh

shell-bash: build ## Build the image and open an interactive bash shell (override default zsh)
	docker run --rm -it $(FULL) bash

clean: ## Remove the toolbox image
	docker rmi $(FULL) 2>/dev/null || true

# Offline = relative links + #fragment anchors only; external URLs are never
# checked, so the target works air-gapped and can't flake on third-party
# downtime. docs/superpowers is gitignored historical material — excluded.
LYCHEE_IMAGE := lycheeverse/lychee:latest

check-links: ## Validate Markdown links and anchors offline (lychee in Docker)
	docker run --rm -w /input -v "$(HOST_SRC)":/input $(LYCHEE_IMAGE) \
	  --offline --include-fragments --no-progress \
	  --exclude-path docs/superpowers \
	  README.md CLAUDE.md CONTRIBUTING.md CONTEXT.md docs .claude/rules .claude/skills

# Refresh the vendored third-party skills recorded in skills-lock.json to their
# latest upstream versions (source of truth = the lockfile). Needs node/npx on
# the host; no container — these are agent skills, not part of the Go/image build.
update-skills: ## Update vendored skills (skills-lock.json) to latest upstream
	npx --yes skills@latest update --project --yes

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*##' $(MAKEFILE_LIST) | awk -F ':.*## ' '{printf "  %-18s %s\n", $$1, $$2}'

# --- Go CLI (containerised) ---
BINARY := toolbox

# Cross-compile for the host so the binary is runnable after `make go-build`.
# The build still happens inside the Linux golang container, but GOOS/GOARCH
# target the host's platform (darwin/arm64 on M-series Macs, linux/amd64 on
# typical CI runners, …).
#
# uname runs wherever make runs, so from INSIDE a toolbox shell it reports the
# container (linux) and the build would silently yield a binary the host cannot
# execute. TOOLBOX_HOST_OS / TOOLBOX_HOST_ARCH carry the real values across that
# boundary — sessionplan injects them in every shell, read off the CLI's own
# runtime.GOOS/GOARCH — the same trick HOST_SRC plays with
# TOOLBOX_HOST_WORKSPACE. uname stays the fallback, for a native host terminal
# and for a container created before those vars existed.
HOST_OS    := $(if $(TOOLBOX_HOST_OS),$(TOOLBOX_HOST_OS),$(shell uname -s | tr '[:upper:]' '[:lower:]'))
HOST_ARCH  := $(if $(TOOLBOX_HOST_ARCH),$(TOOLBOX_HOST_ARCH),$(shell uname -m | sed -e 's/x86_64/amd64/' -e 's/aarch64/arm64/'))

# MACOS_ARCH is what go-build-macos targets. arm64 covers every M-series Mac;
# override it for an Intel one (`make go-build-macos MACOS_ARCH=amd64`).
MACOS_ARCH ?= arm64

go-build: ## Build the Go CLI binary for the host platform
	$(GO_RUN) env GOOS=$(HOST_OS) GOARCH=$(HOST_ARCH) go build -o $(BINARY) .

# Delegates to go-build rather than repeating its recipe, so the two can never
# drift on flags (GOFLAGS, CGO, -o). With TOOLBOX_HOST_OS in the env go-build
# already targets macOS on its own; this stays as the explicit override, for a
# container predating that var and for building a macOS binary from elsewhere.
go-build-macos: ## Build the Go CLI binary for macOS explicitly, ignoring the detected host (arch: MACOS_ARCH, default arm64)
	$(MAKE) go-build HOST_OS=darwin HOST_ARCH=$(MACOS_ARCH)

go-test: ## Run Go tests inside a golang container
	$(GO_RUN) go test ./... -count=1

go-test-verbose: ## Run Go tests with -v and race detection (requires CGO)
	docker run --rm $(GO_MOUNT) $(GO_BUILD_ENV) $(GO_IMAGE) go test -v -race ./...

# Absolute path, not bare `golangci-lint`: GO_MOUNT binds the shared GOPATH
# volume over /go, and the image PATH lists /go/bin before /usr/bin, so a stray
# golangci-lint left in the volume's /go/bin would shadow the image's own binary
# (and can be a wrong-arch build → `exec: no such file`).
go-lint: ## Run golangci-lint inside a container
	docker run --rm $(GO_MOUNT) $(GOLANGCI_IMAGE) /usr/bin/golangci-lint run ./...

go-check: go-test go-lint ## Quick Go gate: run the test suite then the linter (covers the CI test + lint jobs; run `make test` too when the change touches the image)

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
