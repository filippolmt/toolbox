IMAGE   := toolbox
TAG     := local
FULL    := $(IMAGE):$(TAG)

# Go toolchain runs inside a container so Go is not required on the host.
# A named Docker volume caches the module + build cache across runs.
GO_IMAGE        := golang:1.26
GOLANGCI_IMAGE  := golangci/golangci-lint:v2.11.4-alpine
GO_MOD_VOL      := toolbox-gomod
GO_RUN          := docker run --rm \
	-v "$(CURDIR)":/src \
	-v $(GO_MOD_VOL):/go \
	-w /src \
	-e GOFLAGS=-mod=mod \
	-e CGO_ENABLED=0 \
	$(GO_IMAGE)

.PHONY: build test shell clean help go-build go-test go-test-verbose go-lint go-shell go-clean-cache

build: ## Build the toolbox image
	docker build -f docker/Dockerfile -t $(FULL) .

test: build ## Build the image and run the smoke test
	docker/smoke-test.sh $(FULL)

shell: build ## Build the image and open an interactive shell in it
	docker run --rm -it $(FULL) bash

clean: ## Remove the toolbox image
	docker rmi $(FULL) 2>/dev/null || true

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*##' $(MAKEFILE_LIST) | awk -F ':.*## ' '{printf "  %-18s %s\n", $$1, $$2}'

# --- Go CLI (containerised) ---
BINARY := toolbox

go-build: ## Build the Go CLI binary inside a golang container
	$(GO_RUN) go build -o $(BINARY) .

go-test: ## Run Go tests inside a golang container
	$(GO_RUN) go test ./... -count=1

go-test-verbose: ## Run Go tests with -v and race detection (requires CGO)
	docker run --rm \
		-v "$(CURDIR)":/src \
		-v $(GO_MOD_VOL):/go \
		-w /src \
		-e GOFLAGS=-mod=mod \
		$(GO_IMAGE) \
		go test -v -race ./...

go-lint: ## Run golangci-lint inside a container
	docker run --rm \
		-v "$(CURDIR)":/src \
		-v $(GO_MOD_VOL):/go \
		-w /src \
		$(GOLANGCI_IMAGE) \
		golangci-lint run ./...

go-shell: ## Open a shell in the golang container for ad-hoc commands
	docker run --rm -it \
		-v "$(CURDIR)":/src \
		-v $(GO_MOD_VOL):/go \
		-w /src \
		-e GOFLAGS=-mod=mod \
		-e CGO_ENABLED=0 \
		$(GO_IMAGE) bash

go-clean-cache: ## Remove the shared Go module/build cache volume
	docker volume rm $(GO_MOD_VOL) 2>/dev/null || true
