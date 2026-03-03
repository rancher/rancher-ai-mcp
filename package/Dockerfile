FROM registry.suse.com/bci/golang:1.25 AS builder
ARG VERSION=dev
ARG COMMIT
ARG LINKFLAGS="-extldflags -static -s"
ARG LDFLAGS="-X github.com/rancher/rancher-ai-mcp/pkg/version.Version=${VERSION} -X github.com/rancher/rancher-ai-mcp/pkg/version.GitCommit=${COMMIT} ${LINKFLAGS}"

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY cmd/ cmd/
COPY internal/ internal/
COPY pkg/ pkg/
COPY main.go ./

RUN CGO_ENABLED=0 go build -ldflags "${LDFLAGS}" -o mcp

FROM registry.suse.com/bci/bci-micro:15.7
ARG VERSION=dev
ARG UID=1000
ARG GID=1000

# Create group and user 'ai-mcp' (UID/GID 1000) with no home directory and no shell.
RUN echo "ai-mcp:x:${GID}:" >> /etc/group && \
    echo "ai-mcp:x:${UID}:${GID}:ai-mcp-service-user:/nonexistent:/usr/bin/false" >> /etc/passwd

# Copy the binary with 750 permissions to ensure only `ai-mcp` user and group can access it.
COPY --from=builder --chown=${UID}:${GID} --chmod=750 /src/mcp /usr/local/bin/mcp

LABEL "org.opencontainers.image.title"="Rancher MCP Server." \
    "org.opencontainers.image.description"="Rancher MCP Server allows the Rancher AI agent to securely retrieve or update Kubernetes and Rancher resources across local and downstream clusters." \
    "org.opencontainers.image.source"="https://github.com/rancher/rancher-ai-mcp" \
    "org.opencontainers.image.version"=${VERSION}

# Run as `ai-mcp` user (numeric UID/GID for non-root and K8s compatibility).
USER ${UID}:${GID}

CMD ["/usr/local/bin/mcp"]
