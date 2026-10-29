# ghcr.io/alexis-marcel/eriphe: eriphe-launcher, Eriphe from one container
# (install/README.md, One container). It runs install.sh's Linux functions
# against the host's Docker, through the mounted socket: the docker CLI with
# compose, curl and jq to make its own container again on an update.
FROM docker:27-cli
RUN apk add --no-cache curl jq
COPY install.sh /usr/local/lib/eriphe/install.sh
COPY eriphe-launcher /usr/local/bin/eriphe-launcher
ARG VERSION=dev
ENV ERIPHE_LAUNCHER_VERSION=$VERSION
# The version, which eriphe-core and the updaters read; the source, which
# links the package to the repository on GHCR.
LABEL org.opencontainers.image.version=$VERSION \
      org.opencontainers.image.title="Eriphe" \
      org.opencontainers.image.description="Eriphe, a self-hosted personal assistant, installed from one container" \
      org.opencontainers.image.source="https://github.com/Alexis-Marcel/eriphe-system" \
      org.opencontainers.image.licenses="AGPL-3.0-only"
ENTRYPOINT ["/usr/local/bin/eriphe-launcher"]
CMD ["run"]
