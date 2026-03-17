# Define target platforms, image builder and the fully qualified image name.
TARGET_PLATFORMS ?= linux/amd64,linux/arm64

REPO ?= rancher
IMAGE = $(REPO)/rancher-ai-mcp:$(TAG)
DIRTY=
if [ -n "$(git status --porcelain --untracked-files=no)" ]; then
    DIRTY="-dirty"
fi

COMMIT=${COMMIT:-$(git rev-parse --short HEAD)}
GIT_TAG=${GIT_TAG:-$(git tag -l --contains HEAD | head -n 1)}

if [[ -z "$DIRTY" && -n "$GIT_TAG" ]]; then
    VERSION=$GIT_TAG
else
    VERSION="0.0.0-${COMMIT}${DIRTY}"
fi

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
