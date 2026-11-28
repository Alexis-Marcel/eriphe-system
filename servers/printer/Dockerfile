# The builder runs on the build machine and cross-compiles for the target.
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /eriphe-printer .

# poppler's pdftoppm draws a PDF's pages for a printer that does not read
# PDF (most AirPrint printers read only Apple raster). Debian, non-root;
# runs with --read-only and --cap-drop ALL: pages are drawn in /tmp, the
# device's tmpfs.
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends poppler-utils \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /eriphe-printer /eriphe-printer
ENV HOME=/tmp
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/eriphe-printer"]
