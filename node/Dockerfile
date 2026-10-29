# The builder runs on the build machine and cross-compiles for the target.
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /eriphe-node .
# The Mac's own: on Apple silicon with macOS 26, install.sh runs eriphe-node
# on macOS itself and takes it from this image (no token, the same version).
RUN CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /darwin-arm64/eriphe-node .
# eriphe-cred, the credential helper (cred/, a module of its own with no
# dependency), for both Linux architectures: the node copies
# task-bin/linux-<arch>/ into every task's /opt/eriphe/bin (-task-bin), the
# Mac's install.sh takes linux-arm64 for its tasks' VMs. gh, gh's wrapper,
# is the same program under that name: a copy, as links are not copied.
RUN cd cred && for arch in amd64 arm64; do \
      CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /task-bin/linux-$arch/eriphe-cred . && \
      cp /task-bin/linux-$arch/eriphe-cred /task-bin/linux-$arch/gh || exit 1; \
    done

# The docker CLI talks to the host's Docker through the mounted socket.
FROM docker:27-cli
COPY --from=build /eriphe-node /usr/local/bin/eriphe-node
COPY --from=build /darwin-arm64/eriphe-node /usr/local/share/eriphe/darwin-arm64/eriphe-node
COPY --from=build /task-bin /usr/local/share/eriphe/task-bin
# eriphe-core reads it on the registry to tell when a node is behind.
ARG VERSION=dev
LABEL org.opencontainers.image.version=$VERSION
EXPOSE 8790
ENTRYPOINT ["/usr/local/bin/eriphe-node"]
