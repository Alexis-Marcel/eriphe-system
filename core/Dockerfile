# The builder runs on the build machine and cross-compiles for the target.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go brain-prompt.md ./
COPY web ./web
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /eriphe-core .
# The Mac's own: on Apple silicon with macOS 26, install.sh --core-mode runs
# eriphe-core on macOS itself and takes it from this image.
RUN CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /darwin-arm64/eriphe-core .

FROM alpine:3.22
RUN apk add --no-cache ca-certificates
COPY --from=build /eriphe-core /usr/local/bin/eriphe-core
COPY --from=build /darwin-arm64/eriphe-core /usr/local/share/eriphe/darwin-arm64/eriphe-core
# Built in; kept here for configurations that name it in brain.prompt_file.
COPY brain-prompt.md /etc/eriphe/brain-prompt.md
# Without a configuration file, everything eriphe-core makes is in /data.
RUN mkdir -p /data && chown 1993:1993 /data && chmod 700 /data
# Read on the registry to tell when an eriphe-core is behind (updates.go).
ARG VERSION=dev
LABEL org.opencontainers.image.version=$VERSION
VOLUME /data
USER 1993:1993
EXPOSE 8800 8443
ENTRYPOINT ["/usr/local/bin/eriphe-core"]
