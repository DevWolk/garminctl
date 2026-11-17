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

# Setup embeds the WebView2 bootstrapper and runs it on machines without the
# runtime, so only a file Microsoft signed is packaged. The download is
# evergreen: its Authenticode signature is checked instead of a pinned hash.
# The check needs the signer's revocation list from Microsoft, so its result
# is kept as the hash of the checked file and reused while the file is unchanged.
bootstrapper=.cache/MicrosoftEdgeWebview2Setup.exe
microsoft_root=build/windows/microsoft-root-2011.pem

microsoft_signer() {
  local signer
  signer=$(sed -n 's/^[[:space:]]*Subject: *//p' <<< "$1" | sed -n '1s/[[:space:]]*$//p')
  if [[ $signer != *'/O=Microsoft Corporation/CN=Microsoft Corporation' ]]; then
    echo "package-windows: unexpected WebView2 bootstrapper signer: $signer" >&2
    return 1
  fi
}

verify_bootstrapper() {
  local file=$1 report stated
  # Index 0 is the primary signature; without it one valid signature of any
  # signer in the file would be enough.
  local -a check=(osslsigncode verify -CAfile "$microsoft_root" -index 0)
  if report=$("${check[@]}" -in "$file" 2>&1); then
    microsoft_signer "$report"
    return
  fi
  # Microsoft keeps distributing files whose signing certificate has expired;
  # their countersignature makes them valid. This osslsigncode cannot verify
  # Microsoft's countersignature, so the certificate is checked once more at
  # the time the countersignature states. That time is taken on trust: a file
  # signed with a stolen, expired Microsoft key and a false time would pass.
  stated=$(sed -n '/Timestamp time:/{s/^[[:space:]]*Timestamp time: *//p;q}' <<< "$report")
  if [[ -z $stated ]] || ! stated=$(date -u -d "$stated" +%s 2> /dev/null) ||
    ! report=$("${check[@]}" -time "$stated" -in "$file" 2>&1); then
    printf '%s\n' "$report" >&2
    return 1
  fi
  microsoft_signer "$report"
}

if ! sha256sum --check --status "$bootstrapper.verified" 2> /dev/null; then
  if [[ ! -s $bootstrapper ]] || ! verify_bootstrapper "$bootstrapper" 2> /dev/null; then
    curl --fail --location --retry 3 --output "$bootstrapper.download" \
      'https://go.microsoft.com/fwlink/p/?LinkId=2124703'
    if ! verify_bootstrapper "$bootstrapper.download"; then
      rm -f "$bootstrapper.download"
      echo 'package-windows: the downloaded WebView2 bootstrapper has no valid Microsoft signature' >&2
      exit 1
    fi
    mv "$bootstrapper.download" "$bootstrapper"
  fi
  sha256sum "$bootstrapper" > "$bootstrapper.verified"
fi

makensis -V2 -DVERSION="$release_version" -DPRODUCT_VERSION="$numeric_version" build/windows/installer.nsi
bash scripts/sign-windows.sh "dist/garminctl_${release_version}_windows_amd64_setup.exe"
if [[ ${WINDOWS_SIGNING_MODE:-none} != none ]]; then
  signing_dir=${WINDOWS_SIGNING_DIR:-.cache/windows-signing}
  cp "$signing_dir/certificate.pem" dist/windows-signing-certificate.pem
else
  rm -f dist/windows-signing-certificate.pem
fi
