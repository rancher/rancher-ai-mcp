# Define target platforms, image builder and the fully qualified image name.
TARGET_PLATFORMS ?= linux/amd64,linux/arm64

REPO ?= rancher
IMAGE = $(REPO)/rancher-ai-mcp:$(TAG)

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
