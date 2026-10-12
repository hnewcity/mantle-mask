#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
version="${1:-dev}"
if [[ ! "$version" =~ ^[A-Za-z0-9][A-Za-z0-9.+-]*$ ]]; then
  printf 'Invalid release version: %s\n' "$version" >&2
  exit 1
fi
mkdir -p dist
stage=$(mktemp -d)
trap 'rm -rf -- "$stage"' EXIT
for arch in amd64 arm64; do
  name="mantle-mask_${version}_linux_${arch}"
  mkdir -p "$stage/$name"
  CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -ldflags="-s -w" -o "$stage/$name/mantle-mask" ./cmd/mantle-mask
  cp LICENSE NOTICE README.md .env.example "$stage/$name/"
  tar -C "$stage" -czf "dist/$name.tar.gz" "$name"
done
(cd dist && sha256sum "mantle-mask_${version}_linux_"*.tar.gz > "mantle-mask_${version}_checksums.txt")
