# eriphe-agent-codex: OpenAI Codex's static (musl) binary, from its GitHub
# release, and nothing else. eriphe-node injects /opt/eriphe of this image
# into a task container (worker/README.md).
FROM alpine:3.22 AS fetch
ARG CODEX_VERSION=0.164.0
# The release assets' SHA-256, as GitHub lists them:
#   gh api repos/openai/codex/releases/tags/rust-v<version> \
#     --jq '.assets[] | select(.name | test("^codex-(x86_64|aarch64)-unknown-linux-musl.tar.gz$")) | [.name, .digest] | @tsv'
ARG CODEX_SHA256_AMD64=90dd0b03b28afcb7fa733a4f73b413db2916a47347efd43fb669cc1c774ee8e4
ARG CODEX_SHA256_ARM64=29a507a2a3fd8f6cd94b96b762d281d299d15d7902dee6132d314b78cf9108eb
ARG TARGETARCH
RUN apk add --no-cache curl
RUN set -eu; \
    case "${TARGETARCH:-$(uname -m)}" in \
      amd64|x86_64) triple=x86_64-unknown-linux-musl sum=$CODEX_SHA256_AMD64 ;; \
      arm64|aarch64) triple=aarch64-unknown-linux-musl sum=$CODEX_SHA256_ARM64 ;; \
      *) echo "unsupported architecture" >&2; exit 1 ;; \
    esac; \
    cd /tmp; \
    curl -fsSL "https://github.com/openai/codex/releases/download/rust-v${CODEX_VERSION}/codex-${triple}.tar.gz" -o codex.tar.gz; \
    echo "$sum  codex.tar.gz" | sha256sum -c -; \
    tar xzf codex.tar.gz; \
    mkdir -p /out/opt/eriphe/bin; \
    install -m 755 "codex-${triple}" /out/opt/eriphe/bin/codex

FROM scratch
COPY --from=fetch /out/ /
ARG CODEX_VERSION
LABEL eriphe.agent="codex" eriphe.agent.version="${CODEX_VERSION}"
