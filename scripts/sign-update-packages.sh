#!/usr/bin/env bash
# Reuse the configured Windows PFX for full-file updater signatures.
set -euo pipefail

die() { echo "sign-update-packages: $*" >&2; exit 1; }
[[ $# -ge 2 && $# -le 3 && ( $1 == sign || $1 == verify ) ]] || die 'usage: sign-update-packages.sh sign|verify ASSET_DIR [--native-fixtures]'
[[ $# == 2 || ${3:-} == --native-fixtures ]] || die 'unsupported signing option'
if [[ ${3:-} == --native-fixtures && ${GITHUB_ACTIONS:-} != true ]]; then
  die 'native fixtures are restricted to CI'
fi
operation=$1
assets=$(cd "$2" && pwd)
project_root=$(cd "$(dirname "$0")/.." && pwd)
shopt -s nullglob

setups=("$assets"/garminctl_*_windows_amd64_setup.exe)
[[ ${#setups[@]} == 1 ]] || die 'exactly one Windows Setup is required'
tag=${setups[0]##*/}
tag=${tag#garminctl_}
tag=${tag%_windows_amd64_setup.exe}
[[ $tag =~ ^v?[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]] || die 'unsupported package version'
version=${tag#v}
linux_version=${version//-/.}
names=(
  "garminctl_${tag}_windows_amd64_setup.exe"
  "garminctl_${linux_version}_linux_amd64.deb"
  "garminctl_${linux_version}_linux_amd64.rpm"
)
if [[ ${3:-} != --native-fixtures ]]; then
  names+=(
    "garminctl_${version}_linux_amd64.tar.gz"
    "garminctl_${version}_linux_arm64.tar.gz"
    "garminctl_${version}_windows_amd64.zip"
    "garminctl_${version}_windows_arm64.zip"
  )
fi

is_payload_name() {
  local expected
  for expected in "${names[@]}"; do [[ $expected != "$1" ]] || return 0; done
  return 1
}

check_size() {
  local file=$1 size
  [[ -f $file && ! -L $file ]] || die "regular file required: ${file##*/}"
  size=$(stat -c %s "$file")
  if [[ ${2:-package} == signature ]]; then
    [[ $size == 384 ]] || die "signature must be 384 bytes: ${file##*/}"
  else
    [[ $size -gt 0 && $size -le 536870912 ]] || die "package size outside 1..512 MiB: ${file##*/}"
  fi
}

for path in "$assets"/*; do
  [[ -f $path && ! -L $path ]] || die "unexpected nonregular asset: ${path##*/}"
  name=${path##*/}
  case $name in
    checksums.txt|windows-signing-certificate.pem) ;;
    *.sig) is_payload_name "${name%.sig}" || die "unexpected signature: $name" ;;
    *) is_payload_name "$name" || die "unexpected payload: $name" ;;
  esac
done
for name in "${names[@]}"; do check_size "$assets/$name"; done

umask 077
work=$(mktemp -d "$assets/.updater-sign-XXXXXX")
trap 'rm -rf "$work"' EXIT
base64 --decode "$project_root/build/update-public-key.b64" > "$work/public.der"
openssl pkey -pubin -inform DER -in "$work/public.der" -out "$work/public.pem"

verify_pair() {
  local package=$1 signature=$2
  check_size "$signature" signature
  openssl dgst -sha256 -verify "$work/public.pem" -sigopt rsa_padding_mode:pkcs1 \
    -signature "$signature" "$package" > /dev/null
}

if [[ $operation == sign ]]; then
  [[ ${WINDOWS_SIGNING_MODE:-none} == certificate ]] || die 'update signing requires existing certificate mode'
  bash "$project_root/scripts/sign-windows.sh" init
  signing_dir=${WINDOWS_SIGNING_DIR:-"$project_root/.cache/windows-signing"}
  exec 9> "$signing_dir/.lock"
  flock 9
  openssl pkcs12 -in "$signing_dir/signing.pfx" -passin "file:$signing_dir/password" \
    -clcerts -nokeys -out "$work/certificate.pem"
  openssl x509 -in "$work/certificate.pem" -pubkey -noout > "$work/certificate-public.pem"
  openssl pkey -pubin -in "$work/certificate-public.pem" -outform DER > "$work/certificate-public.der"
  cmp -s "$work/public.der" "$work/certificate-public.der" || die 'existing PFX does not match compiled public key'

  for name in "${names[@]}"; do
    if [[ -e $assets/$name.sig ]]; then verify_pair "$assets/$name" "$assets/$name.sig"; fi
    openssl dgst -sha256 -keyform P12 -sign "$signing_dir/signing.pfx" \
      -passin "file:$signing_dir/password" -sigopt rsa_padding_mode:pkcs1 \
      -out "$work/$name.sig" "$assets/$name"
    verify_pair "$assets/$name" "$work/$name.sig"
  done
  for name in "${names[@]}"; do mv -f "$work/$name.sig" "$assets/$name.sig"; done
fi

for name in "${names[@]}"; do verify_pair "$assets/$name" "$assets/$name.sig"; done
if [[ ${3:-} == --native-fixtures ]]; then
  echo 'Native fixture payload signatures verified against the compiled public key.'
else
  echo 'All updater payload signatures verified against the compiled public key.'
fi
