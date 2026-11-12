# syntax=docker/dockerfile:1.7
# The builder runs on the build machine and cross-compiles for the target.
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /eriphe-signal .

# signal-cli's native build (GraalVM): x86_64 Linux only. On arm64 the image
# has no signal-cli, and eriphe-signal says so (servers/signal/README.md).
# Bump the version and its checksum together (the release's
# signal-cli-<version>-Linux-native.tar.gz).
FROM --platform=$BUILDPLATFORM debian:bookworm-slim AS signal-cli
ARG TARGETARCH
ARG SIGNAL_CLI_VERSION=0.14.10
ARG SIGNAL_CLI_SHA256=db9297789c5bbc6131df505d2f37abe43efa0b54200585f5e56b478e1cd93916
ADD --checksum=sha256:${SIGNAL_CLI_SHA256} \
    https://github.com/AsamK/signal-cli/releases/download/v${SIGNAL_CLI_VERSION}/signal-cli-${SIGNAL_CLI_VERSION}-Linux-native.tar.gz /signal-cli.tar.gz
RUN mkdir /out && if [ "$TARGETARCH" = amd64 ]; then tar xzf /signal-cli.tar.gz -C /out signal-cli; fi

# The native build needs glibc and zlib: zlib from Debian, of the target.
FROM debian:bookworm-slim AS zlib
RUN mkdir /out && cp -a /usr/lib/*-linux-gnu/libz.so.1* /out/

# glibc and libgcc (libsignal's library, which signal-cli unpacks in /tmp, needs
# it) on distroless, no shell, non-root. The account and its keys are in
# /data (a volume); /tmp is the node's tmpfs.
FROM gcr.io/distroless/cc-debian12:nonroot
COPY --from=zlib /out/ /usr/local/lib/eriphe/
COPY --from=signal-cli /out/ /usr/local/bin/
COPY --from=build /eriphe-signal /eriphe-signal
ENV LD_LIBRARY_PATH=/usr/local/lib/eriphe SIGNAL_DATA=/data
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/eriphe-signal"]
