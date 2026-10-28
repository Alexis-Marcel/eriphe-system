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

FROM alpine:3.22
RUN apk add --no-cache ca-certificates
COPY --from=build /eriphe-core /usr/local/bin/eriphe-core
# Built in; kept here for configurations that name it in brain.prompt_file.
COPY brain-prompt.md /etc/eriphe/brain-prompt.md
# Without a configuration file, everything eriphe-core makes is in /data.
# The installers, served as https://eriphe.com/install.sh and install.ps1:
# the portal's image copies them from here. They come from the build context
# "install" (the repository's install/): docker build --build-context install=install core
COPY --from=install install.sh install.ps1 /usr/share/eriphe/install/
RUN mkdir -p /data && chown 1993:1993 /data && chmod 700 /data
# Read on the registry to tell when an eriphe-core is behind (updates.go).
ARG VERSION=dev
LABEL org.opencontainers.image.version=$VERSION
VOLUME /data
USER 1993:1993
EXPOSE 8800 8443
ENTRYPOINT ["/usr/local/bin/eriphe-core"]
