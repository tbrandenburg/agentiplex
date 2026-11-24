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
npm pack --silent --json --ignore-scripts --registry=https://registry.npmjs.org --pack-destination "$tmp" "amis@$version" > "$tmp/pack.json"
node -e 'const p=require(process.argv[1])[0]; console.log("AMIS integrity:",p.integrity); if(p.shasum!=="6a32d6a1dc8e5f3758d56ee421ce640713bbbb74") process.exit(1)' "$tmp/pack.json"
printf '%s  %s\n' 6a32d6a1dc8e5f3758d56ee421ce640713bbbb74 "$tmp/amis-$version.tgz" | sha1sum -c -
tar -xzf "$tmp/amis-$version.tgz" -C "$tmp"
test -s "$tmp/package/sdk/sdk.js"
test -s third_party/amis/LICENSE
mkdir -p "$dest"
cp -R "$tmp/package/sdk/." "$dest/"
cp third_party/amis/LICENSE "$dest/LICENSE"
if [[ -f "$tmp/package/NOTICE" ]]; then cp "$tmp/package/NOTICE" "$dest/NOTICE"; fi
cp "$tmp/package/package.json" "$dest/package.json"
printf '%s\n' "$version" > "$dest/.version"
