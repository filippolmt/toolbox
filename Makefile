IMAGE   := toolbox
TAG     := local
FULL    := $(IMAGE):$(TAG)

.PHONY: build test shell clean

build: ## Build the toolbox image
	docker build -f docker/Dockerfile -t $(FULL) .

test: build ## Build and run smoke test
	docker/smoke-test.sh $(FULL)

shell: build ## Build and open interactive shell
	docker run --rm -it $(FULL) bash

clean: ## Remove the toolbox image
	docker rmi $(FULL) 2>/dev/null || true

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*##' $(MAKEFILE_LIST) | awk -F ':.*## ' '{printf "  %-18s %s\n", $$1, $$2}'

# --- Go CLI ---
BINARY := toolbox

go-build: ## Build the Go CLI binary
	go build -o $(BINARY) .

go-test: ## Run Go tests
	go test ./... -count=1

go-test-verbose: ## Run Go tests with verbose + race detection
	go test -v -race ./...
