#!/usr/bin/env bash
# Generate artifacts only; publishing is handled by the tag workflow.
set -euo pipefail
cd "$(dirname "$0")/.."
tag=${1:?release tag (vMAJOR.MINOR.PATCH[-prerelease]) required}
[[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$ ]] || { echo "Invalid release tag" >&2; exit 1; }
version=${tag#v}
# Prereleases retain the existing packaging override; stable tags must match source metadata.
if [[ "$version" != *-* ]]; then
  python3 scripts/chart-version.py check "$version"
fi
mkdir -p dist
go generate ./internal/dashboard
for platform in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do
  os=${platform%/*}; arch=${platform#*/}
  name="agentiplex-$tag-$os-$arch"
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -mod=readonly -trimpath -o "$tmp/agentiplexd" ./cmd/agentiplexd
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -mod=readonly -trimpath -o "$tmp/agentiplex" ./cmd/agentiplex
  cp LICENSE README.md config.example.yaml "$tmp/"
  mkdir -p "$tmp/third_party/amis"
  cp third_party/amis/LICENSE "$tmp/third_party/amis/"
  tar -czf "dist/$name.tar.gz" -C "$tmp" .
  rm -rf "$tmp"
  trap - EXIT
done
helm package charts/agentiplex --version "$version" --app-version "$version" --destination dist
(cd dist && sha256sum ./*.tar.gz ./*.tgz > SHA256SUMS)
