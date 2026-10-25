FROM golang:1.27.3-bookworm AS go
FROM ubuntu:24.04

RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    ca-certificates curl python3 build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev \
    libusb-1.0-0-dev nsis librsvg2-bin dpkg-dev rpm xvfb xauth dbus-x11 \
    openssl osslsigncode \
    && rm -rf /var/lib/apt/lists/*
COPY --from=go /usr/local/go /usr/local/go
ENV PATH="/usr/local/go/bin:${PATH}"
ARG UID=1000
ARG GID=1000
RUN if ! getent group "$GID" >/dev/null; then groupadd -g "$GID" builder; fi \
    && if ! getent passwd "$UID" >/dev/null; then useradd -m -u "$UID" -g "$GID" -s /bin/bash builder; fi
WORKDIR /src
USER ${UID}:${GID}
CMD ["bash"]
