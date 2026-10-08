# Build the manager binary
FROM --platform=$BUILDPLATFORM golang:1.26 AS builder
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG GIT_COMMIT=unknown
ARG GIT_TREE_STATE=unknown
ARG BUILD_DATE=unknown

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY cmd/main.go cmd/main.go
COPY cmd/alb-mcp/ cmd/alb-mcp/
COPY api/ api/
COPY internal/ internal/
# The knowledge and skills alb-mcp serves are embedded into it, so they are
# source, not documentation that can be left out of the build.
COPY docs/agent/ docs/agent/

# Build
# the GOARCH has not a default value to allow the binary be built according to the host where the command
# was called. For example, if we call make docker-build in a local env which has the Apple Silicon M1 SO
# the docker BUILDPLATFORM arg will be linux/arm64 when for Apple x86 it will be linux/amd64. Therefore,
# by leaving it empty we can ensure that the container and binary shipped on it will have the same platform.
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build \
    -ldflags "-s -w \
      -X main.version=${VERSION} \
      -X main.gitCommit=${GIT_COMMIT} \
      -X main.gitTreeState=${GIT_TREE_STATE} \
      -X main.buildDate=${BUILD_DATE}" \
    -o network-services cmd/main.go

# The MCP server ships in the same image as a second binary, selected with
# `command: [/alb-mcp]`. It reads the same API types and the same product
# decoding as the manager and the alb plugin, so a separate image would only
# add a second thing to keep in step.
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build \
    -ldflags "-s -w \
      -X main.version=${VERSION} \
      -X main.gitCommit=${GIT_COMMIT} \
      -X main.gitTreeState=${GIT_TREE_STATE} \
      -X main.buildDate=${BUILD_DATE}" \
    -o alb-mcp ./cmd/alb-mcp

# Use distroless as minimal base image to package the manager binary.
# static-debian12:nonroot is explicit about the Debian variant to avoid silent
# drift if the :nonroot alias resolves to a different Debian release in future.
# For reproducible builds, pin to a SHA digest via:
#   FROM gcr.io/distroless/static-debian12:nonroot@sha256:<digest>
# and update via Dependabot or `cosign verify`.
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=builder /workspace/network-services .
COPY --from=builder /workspace/alb-mcp .
USER 65532:65532

ENTRYPOINT ["/network-services"]
