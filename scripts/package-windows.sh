#!/usr/bin/env bash
# Package signed Windows binaries and the signed NSIS uninstaller.
set -euo pipefail
cd "$(dirname "$0")/.."
release_version=${1:?usage: package-windows.sh VERSION}
numeric_version=0.0.0.0
if [[ $release_version =~ ^v?([0-9]+)\.([0-9]+)\.([0-9]+) ]]; then
  numeric_version="${BASH_REMATCH[1]}.${BASH_REMATCH[2]}.${BASH_REMATCH[3]}.0"
fi
bash scripts/sign-windows.sh bin/garminctl.exe bin/garmin-desktop.exe
if [[ ! -s .cache/MicrosoftEdgeWebview2Setup.exe ]]; then
  curl --fail --location --retry 3 --output .cache/MicrosoftEdgeWebview2Setup.exe.download \
    'https://go.microsoft.com/fwlink/p/?LinkId=2124703'
  mv .cache/MicrosoftEdgeWebview2Setup.exe.download .cache/MicrosoftEdgeWebview2Setup.exe
fi
makensis -V2 -DVERSION="$release_version" -DPRODUCT_VERSION="$numeric_version" build/windows/installer.nsi
bash scripts/sign-windows.sh "dist/garminctl_${release_version}_windows_amd64_setup.exe"
if [[ ${WINDOWS_SIGNING_MODE:-none} != none ]]; then
  signing_dir=${WINDOWS_SIGNING_DIR:-.cache/windows-signing}
  cp "$signing_dir/certificate.pem" dist/windows-signing-certificate.pem
else
  rm -f dist/windows-signing-certificate.pem
fi
