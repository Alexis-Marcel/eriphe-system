# eriphe-agent-claude: Claude Code's native binary, as Anthropic publishes
# it, and nothing else. eriphe-node injects /opt/eriphe of this image into a
# task container (worker/README.md); it is never run as a container itself.
FROM alpine:3.22 AS fetch
ARG CLAUDE_CODE_VERSION=2.1.310
# Anthropic's Claude Code release signing key (code.claude.com/docs/en/setup,
# "Binary integrity and code signing").
ARG KEY_FINGERPRINT=31DDDE24DDFAB679F42D7BD2BAA929FF1A7ECACE
ARG TARGETARCH
RUN apk add --no-cache curl gnupg jq
# The manifest's signature is checked against the key of that fingerprint,
# then the binary against the manifest's checksum for this platform.
RUN set -eu; \
    case "${TARGETARCH:-$(uname -m)}" in \
      amd64|x86_64) platform=linux-x64 ;; \
      arm64|aarch64) platform=linux-arm64 ;; \
      *) echo "unsupported architecture" >&2; exit 1 ;; \
    esac; \
    base="https://downloads.claude.ai/claude-code-releases/${CLAUDE_CODE_VERSION}"; \
    cd /tmp; \
    export GNUPGHOME=/tmp/gnupg; mkdir -m 700 "$GNUPGHOME"; \
    curl -fsSL https://downloads.claude.ai/keys/claude-code.asc | gpg --batch --quiet --import; \
    curl -fsSLO "$base/manifest.json"; \
    curl -fsSLO "$base/manifest.json.sig"; \
    gpg --batch --status-fd 1 --verify manifest.json.sig manifest.json > status; \
    grep -q "^\[GNUPG:\] VALIDSIG .*${KEY_FINGERPRINT}\$" status; \
    [ "$(jq -r .version manifest.json)" = "$CLAUDE_CODE_VERSION" ]; \
    sum=$(jq -er --arg p "$platform" '.platforms[$p].checksum' manifest.json); \
    curl -fsSL "$base/$platform/claude" -o claude; \
    echo "$sum  claude" | sha256sum -c -; \
    mkdir -p /out/opt/eriphe/bin; \
    install -m 755 claude /out/opt/eriphe/bin/claude

FROM scratch
COPY --from=fetch /out/ /
ARG CLAUDE_CODE_VERSION
# The runtime it is for (node/inject.go) and the agent's own version.
LABEL eriphe.agent="claude-code" eriphe.agent.version="${CLAUDE_CODE_VERSION}"
