#!/usr/bin/env bash
set -euo pipefail
script="$(cd "$(dirname "$0")" && pwd)/release.sh"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
git init -q --bare "$tmp/remote.git"
git init -q -b main "$tmp/work"
cd "$tmp/work"
git config user.name 'Release test'
git config user.email 'release@example.invalid'
git commit -q --allow-empty -m initial
git remote add origin "$tmp/remote.git"
git push -q -u origin main
bump=MINOR bash "$script" --dry-run | grep -q 'v0.0.0 -> v0.1.0'
[[ -z "$(git tag --list)" ]]
bump=MINOR bash "$script"
[[ "$(git cat-file -t v0.1.0)" == tag ]]
git ls-remote --exit-code --tags origin refs/tags/v0.1.0 >/dev/null
bump=PATCH bash "$script" --dry-run | grep -q 'v0.1.0 -> v0.1.1'
bump=MAJOR bash "$script" --dry-run | grep -q 'v0.1.0 -> v1.0.0'
git tag v0.1.1
if bump=PATCH bash "$script" --dry-run; then exit 1; fi
git tag -d v0.1.1 >/dev/null
git tag v0.2.0-rc.1
bump=MINOR bash "$script" --dry-run | grep -q 'v0.1.0 -> v0.2.0'
if bump=INVALID bash "$script"; then exit 1; fi
touch dirty
if bump=PATCH bash "$script"; then exit 1; fi
rm dirty
git switch -qc feature
if bump=PATCH bash "$script"; then exit 1; fi
git switch -q main
git commit -q --allow-empty -m unpushed
if bump=PATCH bash "$script"; then exit 1; fi
[[ "$(git tag --list 'v0.1.1')" == "" ]]
echo 'PASS: release bump, dry run, annotated tag push and safety guards'
