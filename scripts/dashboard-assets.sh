#!/usr/bin/env bash
# Package the published, unmodified browser SDK. No npm install or lifecycle scripts.
set -euo pipefail
cd "$(dirname "$0")/.."
version=6.13.0
dest=internal/dashboard/static/vendor
if [[ -f "$dest/.version" && "$(cat "$dest/.version")" == "$version" && -s "$dest/sdk.js" && -s "$dest/LICENSE" ]]; then exit 0; fi
command -v npm >/dev/null || { echo 'Install Node.js/npm to package the dashboard SDK' >&2; exit 1; }
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
npm pack --ignore-scripts --registry=https://registry.npmjs.org --pack-destination "$tmp" "amis@$version" >/dev/null
tar -xzf "$tmp/amis-$version.tgz" -C "$tmp"
test -s "$tmp/package/sdk/sdk.js"
test -s "$tmp/package/LICENSE"
mkdir -p "$dest"
cp -R "$tmp/package/sdk/." "$dest/"
cp "$tmp/package/LICENSE" "$dest/LICENSE"
if [[ -f "$tmp/package/NOTICE" ]]; then cp "$tmp/package/NOTICE" "$dest/NOTICE"; fi
cp "$tmp/package/package.json" "$dest/package.json"
printf '%s\n' "$version" > "$dest/.version"
