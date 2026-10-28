FROM node:24-trixie-slim

ARG CLAUDE_CODE_VERSION=2.1.310
ARG OPENCODE_VERSION=1.19.4
ARG CODEX_VERSION=0.164.0
ARG GH_VERSION=2.103.2
ARG MISE_VERSION=2026.10.19
ARG TARGETARCH

RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates curl git ripgrep jq less procps openssh-client \
    && rm -rf /var/lib/apt/lists/*

# gh from its GitHub release, checked against the published checksums
RUN arch="${TARGETARCH:-$(dpkg --print-architecture)}" \
    && tarball="gh_${GH_VERSION}_linux_${arch}.tar.gz" \
    && cd /tmp \
    && curl -fsSLO "https://github.com/cli/cli/releases/download/v${GH_VERSION}/${tarball}" \
    && curl -fsSL "https://github.com/cli/cli/releases/download/v${GH_VERSION}/gh_${GH_VERSION}_checksums.txt" \
       | grep " ${tarball}\$" | sha256sum -c - \
    && tar xzf "${tarball}" \
    && mv "gh_${GH_VERSION}_linux_${arch}/bin/gh" /usr/local/bin/gh \
    && rm -rf /tmp/gh_*

# mise installs the tools a repository asks for, without root.
RUN arch="${TARGETARCH:-$(dpkg --print-architecture)}" \
    && case "$arch" in amd64) arch=x64 ;; esac \
    && tarball="mise-v${MISE_VERSION}-linux-${arch}.tar.gz" \
    && cd /tmp \
    && curl -fsSLO "https://github.com/jdx/mise/releases/download/v${MISE_VERSION}/${tarball}" \
    && curl -fsSL "https://github.com/jdx/mise/releases/download/v${MISE_VERSION}/SHASUMS256.txt" \
       | grep " \./${tarball}\$\| ${tarball}\$" | sha256sum -c - \
    && tar xzf "${tarball}" \
    && mv mise/bin/mise /usr/local/bin/mise \
    && rm -rf /tmp/mise*

# Codex's npm package carries its static (musl) binary for this platform.
RUN npm install -g "@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}" "opencode-ai@${OPENCODE_VERSION}" \
      "@openai/codex@${CODEX_VERSION}" \
    && npm cache clean --force \
    && opencode --version \
    && codex --version

# The node image's UID 1000 user, renamed. /cache is owned by it so that a
# named volume mounted there starts with the same owner.
RUN usermod -l claude -d /home/claude -m node && groupmod -n claude node \
    && mkdir -p /work /cache && chown claude:claude /work /cache

# Updates come from a new image. Tools and package caches go to /cache, a
# volume the node keeps between tasks.
ENV DISABLE_AUTOUPDATER=1 \
    OPENCODE_DISABLE_AUTOUPDATE=1 \
    HOME=/home/claude \
    PATH=/cache/mise/shims:$PATH \
    MISE_YES=1 \
    MISE_DATA_DIR=/cache/mise \
    MISE_CACHE_DIR=/cache/mise-cache \
    GOMODCACHE=/cache/go/mod \
    GOCACHE=/cache/go/build \
    npm_config_cache=/cache/npm \
    PIP_CACHE_DIR=/cache/pip \
    XDG_CACHE_HOME=/cache/xdg \
    LANG=C.UTF-8

# The agents eriphe-node can run in this image (see node/runtime.go). acp is
# any other ACP agent by its command: one installed here, or in an image of
# its own (an agent's image).
LABEL eriphe.runtimes="claude-code,opencode,codex,acp"

USER claude
WORKDIR /work
