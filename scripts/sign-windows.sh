#!/usr/bin/env bash
# Authenticode signing for personal builds and configured release certificates.
set -euo pipefail

project_root=$(cd "$(dirname "$0")/.." && pwd)
signing_mode=${WINDOWS_SIGNING_MODE:-none}
signing_dir=${WINDOWS_SIGNING_DIR:-"$project_root/.cache/windows-signing"}
timestamp_url=${WINDOWS_TIMESTAMP_URL-}

die() { echo "sign-windows: $*" >&2; exit 1; }

initialize() {
  [[ $signing_mode == none ]] && return
  [[ $signing_mode == self-signed || $signing_mode == certificate ]] || die 'WINDOWS_SIGNING_MODE must be self-signed, certificate, or none'
  umask 077
  mkdir -p "$signing_dir"
  exec 9> "$signing_dir/.lock"
  flock 9
  if [[ $signing_mode == certificate ]]; then
    if [[ -n ${WINDOWS_SIGNING_PFX_BASE64:-} || -n ${WINDOWS_SIGNING_PASSWORD:-} ]]; then
      [[ -n ${WINDOWS_SIGNING_PFX_BASE64:-} && -n ${WINDOWS_SIGNING_PASSWORD:-} ]] || die 'provide both WINDOWS_SIGNING_PFX_BASE64 and WINDOWS_SIGNING_PASSWORD'
      printf '%s' "$WINDOWS_SIGNING_PFX_BASE64" | base64 --decode > "$signing_dir/signing.pfx"
      printf '%s' "$WINDOWS_SIGNING_PASSWORD" > "$signing_dir/password"
    fi
    [[ -s $signing_dir/signing.pfx && -s $signing_dir/password ]] || die 'certificate mode requires an existing signing.pfx/password directory or both signing secrets'
  elif [[ ! -f $signing_dir/signing.pfx ]]; then
    [[ ! -e $signing_dir/password && ! -e $signing_dir/key.pem ]] || die 'incomplete signing identity; inspect the signing directory before retrying'
    openssl rand -hex 32 > "$signing_dir/password"
    openssl req -x509 -newkey rsa:3072 -nodes -sha256 -days 1825 \
      -subj '/CN=garminctl Personal Build/O=DevWolk' \
      -addext 'basicConstraints=critical,CA:FALSE' \
      -addext 'keyUsage=critical,digitalSignature' \
      -addext 'extendedKeyUsage=codeSigning' \
      -keyout "$signing_dir/key.pem" -out "$signing_dir/certificate.pem" \
      2> "$signing_dir/generate.log"
    openssl pkcs12 -export -inkey "$signing_dir/key.pem" -in "$signing_dir/certificate.pem" \
      -passout "file:$signing_dir/password" -out "$signing_dir/signing.pfx"
    rm "$signing_dir/key.pem"
  fi
  [[ -s $signing_dir/password ]] || die 'missing signing password file'
  chmod 700 "$signing_dir"
  chmod 600 "$signing_dir/signing.pfx" "$signing_dir/password"
  openssl pkcs12 -in "$signing_dir/signing.pfx" -passin "file:$signing_dir/password" \
    -nokeys -out "$signing_dir/certificate.pem"
  cat /etc/ssl/certs/ca-certificates.crt "$signing_dir/certificate.pem" > "$signing_dir/verification-ca.pem"
}

sign_file() {
  local target=$1 temporary temporary_dir log
  [[ -f $target ]] || die "file not found: $target"
  temporary_dir=$(mktemp -d "${target}.signing.XXXXXX")
  temporary="$temporary_dir/signed.exe"
  log=$(mktemp "$signing_dir/sign.XXXXXX.log")
  local -a arguments=(sign -pkcs12 "$signing_dir/signing.pfx" -readpass "$signing_dir/password" -h sha256 -n garminctl -i https://github.com/DevWolk/garminctl)
  [[ -z $timestamp_url ]] || arguments+=(-ts "$timestamp_url")
  if ! osslsigncode "${arguments[@]}" -in "$target" -out "$temporary" > "$log" 2>&1; then
    cat "$log" >&2
    rm -f "$temporary" "$log"
    rmdir "$temporary_dir"
    die "signing failed: $target"
  fi
  if ! osslsigncode verify -CAfile "$signing_dir/verification-ca.pem" -ignore-cdp -in "$temporary" >> "$log" 2>&1; then
    cat "$log" >&2
    rm -f "$temporary" "$log"
    rmdir "$temporary_dir"
    die "signature verification failed: $target"
  fi
  chmod --reference="$target" "$temporary"
  mv -f "$temporary" "$target"
  rmdir "$temporary_dir"
  rm "$log"
  echo "Signed and verified: $target"
}

command=${1:-}
if [[ $command == --platform ]]; then
  [[ $# -ge 3 ]] || die 'usage: sign-windows.sh --platform OS FILE...'
  platform=$2
  shift 2
  [[ $platform == windows ]] || exit 0
  command=${1:-}
fi
if [[ $command == init ]]; then
  initialize
  exit
fi
[[ $# -gt 0 ]] || die 'usage: sign-windows.sh init | FILE...'
if [[ $signing_mode == none ]]; then
  echo 'Windows signing explicitly disabled.'
  exit
fi
initialize
for target in "$@"; do sign_file "$target"; done
