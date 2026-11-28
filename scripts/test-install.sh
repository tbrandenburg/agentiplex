#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
cd "$tmp"
# Exercise the real Makefile install recipe without building or requiring root.
mkdir bin
if make -f "$root/Makefile" install DESTDIR="$tmp/stage" PREFIX=/opt/agentiplex; then
  echo 'install unexpectedly succeeded without binaries' >&2
  exit 1
fi
test ! -e "$tmp/stage"
for name in agentiplex agentiplexd; do
  printf '#!/bin/sh\necho %s\n' "$name" > "bin/$name"
  chmod 0755 "bin/$name"
done
make -f "$root/Makefile" install DESTDIR="$tmp/stage" PREFIX=/opt/agentiplex
for name in agentiplex agentiplexd; do
  dest="$tmp/stage/opt/agentiplex/bin/$name"
  cmp "bin/$name" "$dest"
  test -x "$dest"
  test "$("$dest")" = "$name"
done
make -f "$root/Makefile" install PREFIX="$tmp/user prefix"
cmp bin/agentiplex "$tmp/user prefix/bin/agentiplex"
cmp bin/agentiplexd "$tmp/user prefix/bin/agentiplexd"
echo 'Install checks passed.'
