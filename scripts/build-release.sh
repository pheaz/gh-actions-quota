#!/usr/bin/env bash
set -euo pipefail

version="${1:?Usage: scripts/build-release.sh v1.0.0}"
if [[ ! "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$ ]]; then
  echo 'Expected a semantic version tag, for example v1.0.0' >&2
  exit 1
fi

mkdir -p release
for target in darwin-arm64 darwin-amd64 linux-amd64 linux-arm64 windows-amd64; do
  os="${target%-*}"
  arch="${target#*-}"
  suffix=''
  if [[ "$os" == windows ]]; then suffix='.exe'; fi
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath \
    -ldflags "-s -w -X main.version=$version" \
    -o "release/gh-actions-quota_${version}_${target}${suffix}" ./cmd/gh-actions-quota
done

# gh recognizes individual executable assets by their OS/architecture suffix.
# Explicit filenames keep older local builds out of the checksum manifest.
files=(
  "gh-actions-quota_${version}_darwin-arm64"
  "gh-actions-quota_${version}_darwin-amd64"
  "gh-actions-quota_${version}_linux-amd64"
  "gh-actions-quota_${version}_linux-arm64"
  "gh-actions-quota_${version}_windows-amd64.exe"
)
(
  cd release
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "${files[@]}" > checksums.txt
  else
    shasum -a 256 "${files[@]}" > checksums.txt
  fi
)
