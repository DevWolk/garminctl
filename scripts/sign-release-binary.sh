#!/usr/bin/env bash
# GoReleaser runs on the host; the signer and its private key stay in the builder.
set -euo pipefail
platform=${1:?missing platform}
target=${2:?missing binary path}
[[ $platform == windows ]] || exit 0
cd "$(dirname "$0")/.."
project_root=$PWD
if [[ $target == /* ]]; then
  [[ $target == "$project_root/"* ]] || { echo 'Binary is outside the checkout' >&2; exit 1; }
  target=${target#"$project_root/"}
fi
exec docker compose -f docker/docker-compose.yml exec -T \
  -e WINDOWS_SIGNING_MODE -e WINDOWS_SIGNING_DIR -e WINDOWS_SIGNING_PFX_BASE64 -e WINDOWS_SIGNING_PASSWORD -e WINDOWS_TIMESTAMP_URL \
  go bash scripts/sign-windows.sh "$target"
