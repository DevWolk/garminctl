#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
command -v powershell.exe >/dev/null || { echo 'test-windows requires Windows PowerShell on Windows or through WSL interop.' >&2; exit 1; }
if command -v wslpath >/dev/null; then
  windows_root=$(wslpath -w "$PWD")
elif command -v cygpath >/dev/null; then
  windows_root=$(cygpath -w "$PWD")
else
  echo 'Cannot translate the checkout path for Windows.' >&2
  exit 1
fi
arguments=(-NoProfile -NonInteractive -WindowStyle Hidden -ExecutionPolicy Bypass
  -File "$windows_root\scripts\test-windows.ps1" -Root "$windows_root")
if [[ -n ${WINDOWS_TEST_CERT_THUMBPRINT:-} ]]; then
  arguments+=(-CertificateThumbprint "$WINDOWS_TEST_CERT_THUMBPRINT")
fi
exec powershell.exe "${arguments[@]}"
