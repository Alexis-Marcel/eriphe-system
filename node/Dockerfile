# The builder runs on the build machine and cross-compiles for the target.
# The module and build caches are cache mounts, which CI keeps between runs
# (.github/workflows/image.yml). Each program is a stage of its own: they are
# built at the same time.
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS src
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .

FROM src AS build
ARG TARGETOS TARGETARCH VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /eriphe-node .

# The Mac's own: on Apple silicon with macOS 26, install.sh runs eriphe-node
# on macOS itself and takes it from this image (no token, the same version).
FROM src AS darwin
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /darwin-arm64/eriphe-node .

# eriphe-cred, the SSH agent and tunnel of tasks (cred/, a module of its own with no
# dependency), for both Linux architectures: the node copies
# task-bin/linux-<arch>/ into every task's /opt/eriphe/bin (-task-bin), the
# Mac's install.sh takes linux-arm64 for its tasks' VMs.
FROM src AS cred
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    cd cred && for arch in amd64 arm64; do \
      CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /task-bin/linux-$arch/eriphe-cred . || exit 1; \
    done

# eriphe-session, the Windows session helper (cmd/eriphe-session): install.ps1
# takes it from this image and runs it in the user's session. A GUI program
# (-H windowsgui): no console window.
FROM src AS session
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    for arch in amd64 arm64; do \
      CGO_ENABLED=0 GOOS=windows GOARCH=$arch go build -trimpath -ldflags="-s -w -H=windowsgui" -o /windows/$arch/eriphe-session.exe ./cmd/eriphe-session || exit 1; \
    done

# No Docker CLI: the node reaches containers through eriphe-node engine
# (ERIPHE_ENGINE_URL), which speaks to the Docker API itself. Alpine for its
# shell and cp (the Mac's install.sh copies files out with --entrypoint cp),
# and the CA certificates for the registry and the core.
FROM alpine:3.22
RUN apk add --no-cache ca-certificates
COPY --from=build /eriphe-node /usr/local/bin/eriphe-node
COPY --from=darwin /darwin-arm64/eriphe-node /usr/local/share/eriphe/darwin-arm64/eriphe-node
COPY --from=cred /task-bin /usr/local/share/eriphe/task-bin
COPY --from=session /windows /usr/local/share/eriphe/windows
# The installers of this build, for the updaters: one that updates to this
# image runs its install.sh --layout (or install.ps1's setup, in WSL) when its
# layout is newer than the machine's (install/README.md, The machines update
# themselves). From the repository's install/, given as the build context
# "installer": docker build --build-context installer=install node.
COPY --from=installer install.sh install.ps1 /usr/local/share/eriphe/
# eriphe-core reads it on the registry to tell when a node is behind.
ARG VERSION=dev
LABEL org.opencontainers.image.version=$VERSION
EXPOSE 8790
ENTRYPOINT ["/usr/local/bin/eriphe-node"]
