#!/usr/bin/env bash
# Tags determine versions; synchronize tracked Helm metadata before publishing.
set -euo pipefail
die(){ echo "$*" >&2; exit 1; }
case "${bump:-}" in PATCH|patch|MINOR|minor|MAJOR|major) ;; *) die 'Usage: make release bump=PATCH|MINOR|MAJOR';; esac
case "${1:-}" in ""|--dry-run) ;; *) die 'Unknown release argument';; esac
[[ $# -le 1 ]] || die 'Too many arguments'
script_dir="$(cd "$(dirname "$0")" && pwd)"
cd "$(git rev-parse --show-toplevel)"
[[ "$(git symbolic-ref --quiet --short HEAD)" == main ]] || die 'Release requires main'
[[ -z "$(git status --porcelain)" ]] || die 'Release requires a clean working tree'
git fetch --tags origin main
[[ "$(git rev-parse HEAD)" == "$(git rev-parse FETCH_HEAD)" ]] || die 'Release requires HEAD equal to origin main'
latest=v0.0.0
while IFS= read -r tag; do
  if [[ "$tag" =~ ^v(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$ ]]; then
    remote=$(git ls-remote --tags --refs origin "refs/tags/$tag")
    expected=$(printf '%s\trefs/tags/%s' "$(git rev-parse "refs/tags/$tag")" "$tag")
    [[ "$remote" == "$expected" ]] || die "Stable tag $tag is not identical on origin; reconcile it before releasing"
    latest=$tag
    break
  fi
done < <(git tag --list --sort=-version:refname 'v*')
IFS=. read -r major minor patch <<< "${latest#v}"
case "$bump" in
  PATCH|patch) patch=$((patch+1));;
  MINOR|minor) minor=$((minor+1)); patch=0;;
  MAJOR|major) major=$((major+1)); minor=0; patch=0;;
esac
[[ $major -le 999999999 && $minor -le 999999999 && $patch -le 999999999 ]] || die 'Version component too large'
next=v$major.$minor.$patch
git show-ref --verify --quiet "refs/tags/$next" && die "Tag already exists: $next"
echo "Release $latest -> $next at $(git rev-parse --short HEAD)"
chart=deploy/helm/agentiplex/Chart.yaml
version=${next#v}
git ls-files --error-unmatch "$chart" >/dev/null
python3 "$script_dir/chart-version.py" preview "$version"
if [[ "${1:-}" == --dry-run ]]; then exit 0; fi
python3 "$script_dir/chart-version.py" update "$version"
if ! git diff --quiet -- "$chart"; then
  git add -- "$chart"
  git commit -m "chore(release): $next" -- "$chart"
fi
python3 "$script_dir/chart-version.py" check "$version"
[[ -z "$(git status --porcelain)" ]] || die 'Release requires a clean tree after metadata commit'
git tag -a "$next" -m "Release $next"
if ! git push --atomic origin HEAD:refs/heads/main "refs/tags/$next:refs/tags/$next"; then
  die "Atomic release push failed; local commit (if created) and tag $next retained. Inspect remote main and tag before recovery. Do not force push."
fi
echo "Pushed main and $next atomically; the existing GitHub Release workflow builds and publishes artifacts."
