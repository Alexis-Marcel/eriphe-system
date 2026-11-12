# eriphe-agent-vibe: Mistral Vibe's ACP server (vibe-acp), as its GitHub
# release ships it for Linux, and nothing else. The release is a PyInstaller
# directory: the program and its _internal/ (Python and the libraries), kept
# together at /opt/eriphe/vibe. eriphe-node injects /opt/eriphe of this
# image into a task container (worker/README.md).
FROM alpine:3.22 AS fetch
ARG VIBE_VERSION=3.1.1
# The release assets' SHA-256, as GitHub lists them:
#   gh api repos/mistralai/mistral-vibe/releases/tags/v<version> \
#     --jq '.assets[] | select(.name | test("^vibe-acp-linux-.*tar.gz$")) | [.name, .digest] | @tsv'
ARG VIBE_SHA256_AMD64=ea3b33b2faebbd5cd2c4bc1c5226e865a8cafeb7486fb7fbd869418b4e4f6fec
ARG VIBE_SHA256_ARM64=c2dbf99928f80b368a795af5ae870c6ccf2137ac06ad5478d52c44856ea6a219
ARG TARGETARCH
RUN apk add --no-cache curl
# Only files and directories come out: eriphe-node unpacks nothing else.
RUN set -eu; \
    case "${TARGETARCH:-$(uname -m)}" in \
      amd64|x86_64) build=linux-x86_64 sum=$VIBE_SHA256_AMD64 ;; \
      arm64|aarch64) build=linux-aarch64 sum=$VIBE_SHA256_ARM64 ;; \
      *) echo "unsupported architecture" >&2; exit 1 ;; \
    esac; \
    cd /tmp; \
    curl -fsSL "https://github.com/mistralai/mistral-vibe/releases/download/v${VIBE_VERSION}/vibe-acp-${build}-${VIBE_VERSION}.tar.gz" -o vibe.tar.gz; \
    echo "$sum  vibe.tar.gz" | sha256sum -c -; \
    mkdir -p /out/opt/eriphe/vibe; \
    tar xzf vibe.tar.gz -C /out/opt/eriphe/vibe --no-same-owner; \
    [ -z "$(find /out -type l -o -type p -o -type s -o -type b -o -type c)" ]; \
    test -x /out/opt/eriphe/vibe/vibe-acp

FROM scratch
COPY --from=fetch /out/ /
ARG VIBE_VERSION
# The runtime it is for (node/inject.go) and the agent's own version.
LABEL eriphe.agent="vibe" eriphe.agent.version="${VIBE_VERSION}"
