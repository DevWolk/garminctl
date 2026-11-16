#!/usr/bin/env bash
# Run as root in a fresh Ubuntu/Fedora container; execute the app as a normal user.
set -euo pipefail
format=${1:?usage: test-linux-package.sh deb|rpm PACKAGE VERSION}
export PACKAGE_FORMAT=$format
package=${2:?missing package path}
version=${3:?missing package version}
previous_package=${4:-}
previous_version=${5:-}
if [[ -n $previous_package || -n $previous_version ]]; then
  [[ ${GITHUB_ACTIONS:-} == true && -n $previous_package && -n $previous_version && $version != "$previous_version" ]]
  test -d "/src/dist/native-package-catalog/$format"
fi
initial_package=${previous_package:-$package}
case $format in
  deb)
    apt-get update
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      "$initial_package" xvfb xauth dbus-x11 python3 desktop-file-utils python3-pyatspi at-spi2-core xdotool
    ;;
  rpm)
    dnf install -y "$initial_package" xorg-x11-server-Xvfb xorg-x11-xauth dbus-x11 python3 desktop-file-utils util-linux python3-pyatspi at-spi2-core xdotool
    ;;
  *) echo 'Expected deb or rpm' >&2; exit 1 ;;
esac
desktop-file-validate /usr/share/applications/garminctl.desktop
test -s /usr/lib/udev/rules.d/69-garminctl.rules
useradd --create-home --uid 1500 garmin-smoke
state=$(mktemp -d)
sequence_passed=false
cleanup() {
  if [[ -n $previous_package && $sequence_passed != true ]]; then
    echo 'Paired acceptance failed; retaining disposable state without replay or cleanup.' >&2
  else
    rm -rf "$state"
  fi
}
trap cleanup EXIT
mkdir -p "$state/config/garminctl"
printf 'preserve synthetic package state\n' > "$state/config/garminctl/package-acceptance.txt"
chown -R 1500:1500 "$state"
evidence=${NATIVE_RESULTS_DIR:-}
if [[ -n $previous_package ]]; then
  case $format in
    deb) apt-get install -y --no-install-recommends polkitd pkexec ;;
    rpm) dnf install -y polkit ;;
  esac
  install -m 0644 /src/scripts/fixtures/native-package.rules /etc/polkit-1/rules.d/00-garminctl-native-acceptance.rules
  mkdir -p /run/dbus
  dbus-daemon --system --fork --nopidfile
  /usr/lib/polkit-1/polkitd --no-debug > "$state/polkit.log" 2>&1 &
  for attempt in {1..50}; do
    if dbus-send --system --print-reply --dest=org.freedesktop.PolicyKit1 /org/freedesktop/PolicyKit1/Authority org.freedesktop.DBus.Peer.Ping > /dev/null 2>&1; then break; fi
    sleep 0.1
  done
  dbus-send --system --print-reply --dest=org.freedesktop.PolicyKit1 /org/freedesktop/PolicyKit1/Authority org.freedesktop.DBus.Peer.Ping > /dev/null
fi
check_version() {
  local expected=$1 phase=$2
  shift 2
  local results=
  if [[ -n $evidence ]]; then
    results=$evidence
    if [[ -n $previous_package ]]; then results="$results/$phase"; fi
    mkdir -p "$results"
    chown 1500:1500 "$results"
  fi
  runuser -u garmin-smoke -- env GTK_MODULES=atk-bridge NO_AT_BRIDGE=0 LANG=C.UTF-8 NATIVE_RESULTS_DIR="$results" \
    dbus-run-session -- xvfb-run -a \
    python3 /src/scripts/test-package.py --repo /src --version "$expected" --state "$state" \
    --desktop /usr/bin/garmin-desktop --cli /usr/bin/garminctl "$@"
  test "$(cat "$state/config/garminctl/package-acceptance.txt")" = 'preserve synthetic package state'
}
application_transition() {
  local current=$1 target=$2 phase=$3
  shift 3
  local work=${evidence:-$state}/application-$phase
  mkdir -p "$work"
  chown 1500:1500 "$work"
  runuser -u garmin-smoke -- env GTK_MODULES=atk-bridge NO_AT_BRIDGE=0 LANG=C.UTF-8 \
    dbus-run-session -- xvfb-run -a python3 /src/scripts/native_application.py \
    --repo /src --desktop /usr/bin/garmin-desktop --cli /usr/bin/garminctl --state "$state" \
    --work "$work" --current "$current" --target "$target" "$@"
}
if [[ -n $previous_package ]]; then
  check_version "$previous_version" previous
  application_transition "$previous_version" "$version" upgrade --catalog "/src/dist/native-package-catalog/$format" \
    --assets "$(dirname "$package")" --assets "$(dirname "$previous_package")"
  check_version "$version" upgrade --native-ui
  application_transition "$version" "$previous_version" rollback --rollback
  check_version "$previous_version" rollback
else
  check_version "$version" current --native-ui
fi
case $format in
  deb) apt-get remove -y garminctl ;;
  rpm) dnf remove -y garminctl ;;
esac
test ! -e /usr/bin/garmin-desktop
sequence_passed=true
