#!/usr/bin/env bash
# Tags are the only source of version truth; existing tag workflow publishes.
set -euo pipefail
die(){ echo "$*" >&2; exit 1; }
case "${bump:-}" in PATCH|patch|MINOR|minor|MAJOR|major) ;; *) die 'Usage: make release bump=PATCH|MINOR|MAJOR';; esac
case "${1:-}" in ""|--dry-run) ;; *) die 'Unknown release argument';; esac
[[ $# -le 1 ]] || die 'Too many arguments'
# Use the invoking repository root (also enables isolated release tests).
cd "$(git rev-parse --show-toplevel)"
[[ "$(git symbolic-ref --quiet --short HEAD)" == main ]] || die 'Release requires main'
[[ -z "$(git status --porcelain)" ]] || die 'Release requires a clean working tree'
git fetch --tags origin main
[[ "$(git rev-parse HEAD)" == "$(git rev-parse FETCH_HEAD)" ]] || die 'Release requires HEAD equal to origin main'
latest=v0.0.0
while IFS= read -r tag; do
  if [[ "$tag" =~ ^v(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$ ]]; then
    # An unpublished local release tag may represent a failed prior push.
    # Do not skip it or calculate the next version from it silently.
    remote=$(git ls-remote --tags --refs origin "refs/tags/$tag")
    [[ "$remote" == "$(git rev-parse "refs/tags/$tag")"
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
if [[ "${1:-}" == --dry-run ]]; then exit 0; fi
git tag -a "$next" -m "Release $next"
if ! git push origin "refs/tags/$next:refs/tags/$next"; then
  die "Tag push failed; local tag $next retained. Inspect remote before retrying or removing it."
fi
echo "Pushed $next; the existing GitHub Release workflow builds and publishes artifacts."
\t'"refs/tags/$tag" ]] || die "Stable tag $tag is not identical on origin; reconcile it before releasing"
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
if [[ "${1:-}" == --dry-run ]]; then exit 0; fi
git tag -a "$next" -m "Release $next"
if ! git push origin "refs/tags/$next:refs/tags/$next"; then
  die "Tag push failed; local tag $next retained. Inspect remote before retrying or removing it."
fi
echo "Pushed $next; the existing GitHub Release workflow builds and publishes artifacts."
