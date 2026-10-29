# eriphe-agent-opencode: OpenCode's native binary (glibc), from its GitHub
# release, and nothing else. One build per architecture: on amd64 the
# "baseline" one, which runs on any x86-64 CPU (the other needs AVX2).
# eriphe-node injects /opt/eriphe of this image into a task container
# (worker/README.md).
FROM alpine:3.22 AS fetch
ARG OPENCODE_VERSION=1.19.4
# The release assets' SHA-256, as GitHub lists them:
#   gh api repos/anomalyco/opencode/releases/tags/v<version> \
#     --jq '.assets[] | select(.name | test("^opencode-linux-(x64-baseline|arm64).tar.gz$")) | [.name, .digest] | @tsv'
ARG OPENCODE_SHA256_AMD64=1697ef40558d378bd2f72efa8fafe1bed1f0e664f09247b9357556b95dbc7cbc
ARG OPENCODE_SHA256_ARM64=8568230ccd847dc93e95b9f6894379077d3e014a16b48040743573e77c23e85f
ARG TARGETARCH
RUN apk add --no-cache curl
RUN set -eu; \
    case "${TARGETARCH:-$(uname -m)}" in \
      amd64|x86_64) build=linux-x64-baseline sum=$OPENCODE_SHA256_AMD64 ;; \
      arm64|aarch64) build=linux-arm64 sum=$OPENCODE_SHA256_ARM64 ;; \
      *) echo "unsupported architecture" >&2; exit 1 ;; \
    esac; \
    cd /tmp; \
    curl -fsSL "https://github.com/anomalyco/opencode/releases/download/v${OPENCODE_VERSION}/opencode-${build}.tar.gz" -o opencode.tar.gz; \
    echo "$sum  opencode.tar.gz" | sha256sum -c -; \
    tar xzf opencode.tar.gz; \
    mkdir -p /out/opt/eriphe/bin; \
    install -m 755 opencode /out/opt/eriphe/bin/opencode

FROM scratch
COPY --from=fetch /out/ /
ARG OPENCODE_VERSION
LABEL eriphe.agent="opencode" eriphe.agent.version="${OPENCODE_VERSION}"
