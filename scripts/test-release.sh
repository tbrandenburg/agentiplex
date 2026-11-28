#!/usr/bin/env bash
set -euo pipefail
script_dir="$(cd "$(dirname "$0")" && pwd)"
script="$script_dir/release.sh"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
git init -q --bare "$tmp/remote.git"
git init -q -b main "$tmp/work"
cd "$tmp/work"
git config user.name 'Release test'
git config user.email 'release@example.invalid'
chart=deploy/helm/agentiplex/Chart.yaml
mkdir -p "$(dirname "$chart")"
cat > "$chart" <<'EOF'
apiVersion: v2
name: agentiplex
version: 0.0.0
appVersion: "0.0.0"
description: Keep this metadata unchanged
EOF
git add "$chart"
git commit -qm initial
git remote add origin "$tmp/remote.git"
git push -q -u origin main
initial=$(git rev-parse HEAD)
plan=$(bump=MINOR bash "$script" --dry-run)
grep -q 'v0.0.0 -> v0.1.0' <<< "$plan"
grep -q 'appVersion 0.0.0 -> 0.1.0' <<< "$plan"
[[ "$(git rev-parse HEAD)" == "$initial" ]]
[[ -z "$(git status --porcelain)" && -z "$(git tag --list)" ]]
bump=MINOR bash "$script"
[[ "$(git cat-file -t v0.1.0)" == tag ]]
[[ "$(git rev-parse 'v0.1.0^{commit}')" == "$(git rev-parse HEAD)" ]]
[[ "$(git --git-dir="$tmp/remote.git" rev-parse main)" == "$(git rev-parse HEAD)" ]]
[[ "$(git --git-dir="$tmp/remote.git" rev-parse refs/tags/v0.1.0)" == "$(git rev-parse refs/tags/v0.1.0)" ]]
[[ "$(git diff-tree --no-commit-id --name-only -r HEAD)" == "$chart" ]]
grep -qx 'version: 0.1.0' "$chart"
grep -qx 'appVersion: "0.1.0"' "$chart"
grep -qx 'description: Keep this metadata unchanged' "$chart"
python3 "$script_dir/chart-version.py" check 0.1.0
if python3 "$script_dir/chart-version.py" check 0.1.1; then exit 1; fi
bump=PATCH bash "$script" --dry-run | grep 'v0.1.0 -> v0.1.1' >/dev/null
bump=MAJOR bash "$script" --dry-run | grep 'v0.1.0 -> v1.0.0' >/dev/null
git tag v0.1.1
if bump=PATCH bash "$script" --dry-run; then exit 1; fi
git tag -d v0.1.1 >/dev/null
git tag v0.2.0-rc.1
bump=MINOR bash "$script" --dry-run | grep 'v0.1.0 -> v0.2.0' >/dev/null
if bump=INVALID bash "$script"; then exit 1; fi
touch dirty
if bump=PATCH bash "$script"; then exit 1; fi
rm dirty
git switch -qc feature
if bump=PATCH bash "$script"; then exit 1; fi
git switch -q main
git commit -q --allow-empty -m unpushed
if bump=PATCH bash "$script"; then exit 1; fi
git reset --hard -q origin/main
[[ "$(git tag --list 'v0.1.1')" == "" ]]

# Malformed or duplicate version fields must fail without editing the chart.
printf 'version: 0.1.0\n' >> "$chart"
before=$(git hash-object "$chart")
if python3 "$script_dir/chart-version.py" update 0.1.1; then exit 1; fi
[[ "$(git hash-object "$chart")" == "$before" ]]
git restore "$chart"

# Reject only the tag on the remote: atomic push must also leave main unchanged.
cat > "$tmp/remote.git/hooks/update" <<'EOF'
#!/bin/sh
case "$1" in refs/tags/v0.1.1) exit 1;; esac
exit 0
EOF
chmod +x "$tmp/remote.git/hooks/update"
before=$(git rev-parse HEAD)
if bump=PATCH bash "$script"; then exit 1; fi
[[ "$(git --git-dir="$tmp/remote.git" rev-parse main)" == "$before" ]]
if git --git-dir="$tmp/remote.git" show-ref --verify --quiet refs/tags/v0.1.1; then exit 1; fi
[[ "$(git rev-parse 'v0.1.1^{commit}')" == "$(git rev-parse HEAD)" ]]
python3 "$script_dir/chart-version.py" check 0.1.1
[[ -z "$(git status --porcelain)" ]]
# Only the isolated test repository is reset; real release failures retain local state.
git tag -d v0.1.1 >/dev/null
git reset --hard -q "$before"
rm "$tmp/remote.git/hooks/update"

bump=PATCH bash "$script"
python3 "$script_dir/chart-version.py" check 0.1.1
bump=MINOR bash "$script"
python3 "$script_dir/chart-version.py" check 0.2.0
bump=MAJOR bash "$script"
python3 "$script_dir/chart-version.py" check 1.0.0
[[ "$(git --git-dir="$tmp/remote.git" rev-parse main)" == "$(git rev-parse 'v1.0.0^{commit}')" ]]
[[ -z "$(git status --porcelain)" ]]

# Already synchronized metadata should not create an empty release commit.
python3 "$script_dir/chart-version.py" update 1.0.1
git add "$chart"
git commit -qm 'Prepare metadata'
git push -q origin main
before=$(git rev-parse HEAD)
bump=PATCH bash "$script"
[[ "$(git rev-parse HEAD)" == "$before" ]]
[[ "$(git rev-parse 'v1.0.1^{commit}')" == "$before" ]]
echo 'PASS: semantic bump, metadata, dry run, atomic push and release safety guards'
