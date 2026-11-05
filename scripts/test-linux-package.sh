#!/usr/bin/env bash
# Run as root in a fresh Ubuntu/Fedora container; execute the app as a normal user.
set -euo pipefail
format=${1:?usage: test-linux-package.sh deb|rpm PACKAGE VERSION}
package=${2:?missing package path}
version=${3:?missing package version}
case $format in
  deb)
    apt-get update
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      "$package" xvfb xauth dbus-x11 python3 desktop-file-utils python3-pyatspi at-spi2-core xdotool
    ;;
  rpm)
    dnf install -y "$package" xorg-x11-server-Xvfb xorg-x11-xauth dbus-x11 python3 desktop-file-utils util-linux python3-pyatspi at-spi2-core xdotool
    ;;
  *) echo 'Expected deb or rpm' >&2; exit 1 ;;
esac
desktop-file-validate /usr/share/applications/garminctl.desktop
test -s /usr/lib/udev/rules.d/69-garminctl.rules
useradd --create-home --uid 1500 garmin-smoke
if [[ -n ${NATIVE_RESULTS_DIR:-} ]]; then
  mkdir -p "$NATIVE_RESULTS_DIR"
  chown 1500:1500 "$NATIVE_RESULTS_DIR"
fi
runuser -u garmin-smoke -- env GTK_MODULES=atk-bridge NO_AT_BRIDGE=0 LANG=C.UTF-8 \
  dbus-run-session -- xvfb-run -a \
  python3 /src/scripts/test-package.py --repo /src --version "$version" \
  --desktop /usr/bin/garmin-desktop --cli /usr/bin/garminctl --native-ui
case $format in
  deb) apt-get remove -y garminctl ;;
  rpm) dnf remove -y garminctl ;;
esac
test ! -e /usr/bin/garmin-desktop
