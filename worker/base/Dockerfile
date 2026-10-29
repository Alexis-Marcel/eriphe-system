# eriphe-base: what every task container starts from. The agent is not in
# it: eriphe-node injects it at /opt/eriphe/bin (worker/agents/), and the
# agent installs what else it needs (apt, uv, npm), as root in its
# disposable container, through the node's egress relay.
FROM ghcr.io/astral-sh/uv:0.13.6 AS uv

# Debian trixie with Node.js 24 and npm.
FROM node:24-trixie-slim

# Python 3 (the system's, for scripts and apt's own tools); uv brings any
# other version. No git, gh or compilers: the agent installs them when a task
# needs them, and the node's cache keeps the packages.
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates curl python3 \
    && rm -rf /var/lib/apt/lists/* /var/cache/apt/*.bin \
    && npm uninstall -g corepack && rm -rf /opt/yarn-* /usr/local/bin/yarn /usr/local/bin/yarnpkg
COPY --from=uv /uv /uvx /usr/local/bin/

# apt for a task: as root without capabilities (no sandbox user to switch
# to), its downloads and lists in /cache, kept for the next task. The Debian
# image's docker-clean would delete them after each install.
COPY apt.conf /etc/apt/apt.conf.d/90eriphe
RUN rm -f /etc/apt/apt.conf.d/docker-clean

# The node image's UID 1000 user, renamed: a non-root user for whoever wants
# one (tasks run as root, see node/README.md).
RUN usermod -l eriphe -d /home/eriphe -m node && groupmod -n eriphe node \
    && mkdir -p /work /cache

# The agent eriphe-node injects, and its helpers, come first in PATH.
# Package caches go to /cache, a volume the node keeps between tasks.
ENV PATH=/opt/eriphe/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
    LANG=C.UTF-8 \
    DEBIAN_FRONTEND=noninteractive \
    DISABLE_AUTOUPDATER=1 \
    OPENCODE_DISABLE_AUTOUPDATE=1 \
    npm_config_cache=/cache/npm \
    PIP_CACHE_DIR=/cache/pip \
    UV_CACHE_DIR=/cache/uv \
    UV_PYTHON_INSTALL_DIR=/cache/uv/python \
    UV_LINK_MODE=copy \
    GOMODCACHE=/cache/go/mod \
    GOCACHE=/cache/go/build \
    XDG_CACHE_HOME=/cache/xdg

# The agents eriphe-node can inject in this image (node/inject.go); acp is
# any other ACP agent, by its command.
LABEL eriphe.runtimes="claude-code,opencode,codex,acp"

WORKDIR /work
