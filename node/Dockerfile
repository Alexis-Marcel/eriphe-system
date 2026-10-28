# The builder runs on the build machine and cross-compiles for the target.
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /eriphe-node .

# The docker CLI talks to the host's Docker through the mounted socket.
FROM docker:27-cli
COPY --from=build /eriphe-node /usr/local/bin/eriphe-node
# eriphe-core reads it on the registry to tell when a node is behind.
ARG VERSION=dev
LABEL org.opencontainers.image.version=$VERSION
EXPOSE 8790
ENTRYPOINT ["/usr/local/bin/eriphe-node"]
