#!/usr/bin/env bash
# Build native Linux packages inside the development container.
set -euo pipefail
cd "$(dirname "$0")/.."
version=${1#v}
version=${version//-/.}
[[ $version =~ ^[0-9][A-Za-z0-9.+~]*$ ]] || version="0.0.0.${version//[^A-Za-z0-9.]/.}"
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
root="$stage/root"
install -Dm755 bin/garmin-desktop "$root/usr/bin/garmin-desktop"
install -Dm755 bin/garminctl-linux "$root/usr/bin/garminctl"
install -Dm644 build/linux/garminctl.desktop "$root/usr/share/applications/garminctl.desktop"
install -Dm644 internal/web/ui/icon.svg "$root/usr/share/icons/hicolor/scalable/apps/garminctl.svg"
install -Dm644 build/linux/69-garminctl.rules "$root/usr/lib/udev/rules.d/69-garminctl.rules"
install -Dm644 LICENSE "$root/usr/share/doc/garminctl/LICENSE"
mkdir -p "$root/DEBIAN" dist
cat > "$root/DEBIAN/control" <<CONTROL
Package: garminctl
Version: $version
Architecture: amd64
Maintainer: DevWolk <devwolk@users.noreply.github.com>
Depends: libc6 (>= 2.34), libgtk-3-0t64, libwebkit2gtk-4.1-0, libusb-1.0-0, pkexec | policykit-1
Section: utils
Priority: optional
Description: Native Garmin watch manager with CLI and MCP integration
CONTROL
dpkg-deb --root-owner-group --build "$root" "dist/garminctl_${version}_linux_amd64.deb"
mkdir -p "$stage/rpm/BUILD" "$stage/rpm/RPMS" "$stage/rpm/SOURCES" "$stage/rpm/SPECS" "$stage/rpm/SRPMS"
cat > "$stage/rpm/SPECS/garminctl.spec" <<SPEC
Name: garminctl
Version: $version
Release: 1
Summary: Native Garmin watch manager with CLI and MCP integration
License: MIT
URL: https://github.com/DevWolk/garminctl
Requires: glibc >= 2.34, gtk3, webkit2gtk4.1, libusb1, /usr/bin/pkexec
AutoReqProv: no
%description
Manage Garmin watches from a native desktop window, CLI or an MCP client.
%install
mkdir -p %{buildroot}
cp -a $root/usr %{buildroot}/
%files
/usr/bin/garmin-desktop
/usr/bin/garminctl
/usr/share/applications/garminctl.desktop
/usr/share/icons/hicolor/scalable/apps/garminctl.svg
/usr/lib/udev/rules.d/69-garminctl.rules
/usr/share/doc/garminctl/LICENSE
SPEC
rpmbuild --define "_topdir $stage/rpm" --define '_build_id_links none' --define '__os_install_post %{nil}' -bb "$stage/rpm/SPECS/garminctl.spec"
cp "$stage"/rpm/RPMS/x86_64/*.rpm "dist/garminctl_${version}_linux_amd64.rpm"
