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
# eriphe-cred, the credential helper every task gets in /opt/eriphe/bin
# (cred/, a module of its own with no dependency): for this image's
# architecture, and for both Linux ones, for the Mac's tasks (Linux VMs).
RUN cd cred && for arch in amd64 arm64; do \
      CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /linux-$arch/eriphe-cred . ; \
    done

# The docker CLI talks to the host's Docker through the mounted socket.
FROM docker:27-cli
ARG TARGETARCH
COPY --from=build /eriphe-node /usr/local/bin/eriphe-node
COPY --from=build /darwin-arm64/eriphe-node /usr/local/share/eriphe/darwin-arm64/eriphe-node
COPY --from=build /linux-amd64/eriphe-cred /usr/local/share/eriphe/linux-amd64/eriphe-cred
COPY --from=build /linux-arm64/eriphe-cred /usr/local/share/eriphe/linux-arm64/eriphe-cred
# What the node lends every task, read-only, at /opt/eriphe/bin: eriphe-cred
# for git (credential.helper) and, as gh, the wrapper of the real gh.
COPY --from=build /linux-${TARGETARCH}/eriphe-cred /opt/eriphe/bin/eriphe-cred
RUN ln -s eriphe-cred /opt/eriphe/bin/gh
# eriphe-core reads it on the registry to tell when a node is behind.
ARG VERSION=dev
LABEL org.opencontainers.image.version=$VERSION
EXPOSE 8790
ENTRYPOINT ["/usr/local/bin/eriphe-node"]
