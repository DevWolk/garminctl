FROM golang:1.27.4-bookworm@sha256:4fa7c968ba93f1635670522dbb01a2c78150443e1385746924566079f36570ea AS go
FROM node:24-bookworm-slim@sha256:01d566e8d94d8c619591d78652babfccd3c6992a3dffa9ed5ab39e11cf6c70e9 AS node
FROM ubuntu:24.04@sha256:73b999393d07f2bf65a40373778a44dc9a6d54ad7718d29247d8ff9413c4d6a8

RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    ca-certificates curl python3 build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev \
    libusb-1.0-0-dev nsis librsvg2-bin dpkg-dev rpm xvfb xauth dbus-x11 \
    openssl osslsigncode \
    && rm -rf /var/lib/apt/lists/*
COPY --from=go /usr/local/go /usr/local/go
# Headless UI contract tests use the built-in runner, without npm or a bundler.
COPY --from=node /usr/local/bin/node /usr/local/bin/node
ENV PATH="/usr/local/go/bin:${PATH}"
ARG UID=1000
ARG GID=1000
RUN if ! getent group "$GID" >/dev/null; then groupadd -g "$GID" builder; fi \
    && if ! getent passwd "$UID" >/dev/null; then useradd -m -u "$UID" -g "$GID" -s /bin/bash builder; fi
WORKDIR /src
USER ${UID}:${GID}
CMD ["bash"]
