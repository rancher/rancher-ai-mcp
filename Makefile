# Define target platforms, image builder and the fully qualified image name.
TARGET_PLATFORMS ?= linux/amd64,linux/arm64
GOLANGCI_LINT_VERSION := v2.15.0
GOLANGCI_LINT ?= go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

REPO ?= rancher
DIRTY := $(shell if [ -n "$$(git status --porcelain --untracked-files=no)" ]; then echo "-dirty"; fi)
COMMIT ?= $(shell git rev-parse --short HEAD)
GIT_TAG ?= $(shell git tag -l --contains HEAD | head -n 1)

ifeq ($(DIRTY),)
ifneq ($(GIT_TAG),)
VERSION ?= $(GIT_TAG)
endif
endif
VERSION ?= 0.0.0-$(COMMIT)$(DIRTY)

TAG ?= $(VERSION)
IMAGE = $(REPO)/rancher-ai-mcp:$(TAG)

.PHONY: generate
generate:
	go generate ./...

push-image:
	docker buildx build \
		${IID_FILE_FLAG} \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--file package/Dockerfile \
		--platform=${TARGET_PLATFORMS} \
		--sbom=true \
		--attest type=provenance,mode=max \
		-t ${IMAGE} \
		--push \
		. 

.PHONY: lint
lint:	## Find any linting issues in the project
	$(GOLANGCI_LINT) run --timeout=5m

.PHONY: lint-fix
lint-fix:	## Fix any linting issues in the project
	$(GOLANGCI_LINT) run --fix --timeout=5m

BASE_BRANCH ?= main

.PHONY: lint-changed
lint-changed:	## Find linting issues only in files changed or added on this branch (relative to BASE_BRANCH)
	$(GOLANGCI_LINT) run --timeout=5m --new-from-rev=$$(git merge-base $(BASE_BRANCH) HEAD) --whole-files

.PHONY: lint-fix-changed
lint-fix-changed:	## Fix linting issues only in files changed or added on this branch (relative to BASE_BRANCH)
	$(GOLANGCI_LINT) run --fix --timeout=5m --new-from-rev=$$(git merge-base $(BASE_BRANCH) HEAD) --whole-files
