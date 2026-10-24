#!/usr/bin/env bash
# Compile in the shared Linux toolchain; execution happens on a Windows host.
set -euo pipefail
cd "$(dirname "$0")/.."
directory=bin/windows-tests
mkdir -p "$directory"
manifest="$directory/manifest.tsv"
: > "$manifest"
while IFS='|' read -r package path; do
  [[ -n $package ]] || continue
  relative=${path#"$PWD/"}
  binary="$directory/${relative//\//_}.test.exe"
  GOOS=windows CGO_ENABLED=0 go test -c -o "$binary" "$package"
  printf '%s\t%s\n' "$relative" "$binary" >> "$manifest"
done < <(GOOS=windows CGO_ENABLED=0 go list -f '{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}|{{.Dir}}{{end}}' ./...)
