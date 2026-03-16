# To avoid poluting the Makefile, versions and checksums for tooling and 
# dependencies are defined at hack/make/deps.mk.
include hack/make/deps.mk

# Include logic that can be reused across projects.
include hack/make/build.mk

# Define target platforms, image builder and the fully qualified image name.
TARGET_PLATFORMS ?= linux/amd64,linux/arm64

REPO ?= rancher
IMAGE = $(REPO)/rancher-ai-mcp:$(TAG)

build-image: buildx-machine ## build (and load) the container image targeting the current platform.
	$(IMAGE_BUILDER) build -f package/Dockerfile \
		--builder $(MACHINE) $(IMAGE_ARGS) \
		--build-arg VERSION=$(VERSION) -t "$(IMAGE)" $(BUILD_ACTION) .
	@echo "Built $(IMAGE)"

push-image: buildx-machine ## build the container image targeting all platforms defined by TARGET_PLATFORMS and push to a registry.
	$(IMAGE_BUILDER) build -f package/Dockerfile \
		--builder $(MACHINE) $(IMAGE_ARGS) $(IID_FILE_FLAG) $(BUILDX_ARGS) \
		--build-arg VERSION=$(VERSION) --platform=$(TARGET_PLATFORMS) -t "$(IMAGE)" --push .
	@echo "Pushed $(IMAGE)"
