# The builder runs on the build machine and cross-compiles for the target.
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /eriphe-files .

# Static binary on distroless: CA roots and tzdata, no shell, non-root.
# Runs with --read-only and --cap-drop ALL: nothing is written to disk.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /eriphe-files /eriphe-files
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/eriphe-files"]
