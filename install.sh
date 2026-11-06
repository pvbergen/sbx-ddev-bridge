#!/bin/sh
# Installs or updates ddev-bridge on this machine (Linux, WSL2, macOS):
#   tar xzf ddev-bridge.tar.gz && ./ddev-bridge/install.sh
# Then, in each project the agent may use: ddev bridge enable
set -eu

min=1.25.5 # global hooks
here=$(cd "$(dirname "$0")" && pwd)

fail() { echo "install.sh: $*" >&2; exit 1; }

command -v ddev >/dev/null 2>&1 || fail "ddev not found in PATH"
info=$(ddev version -j 2>/dev/null) || fail "'ddev version -j' failed; is ddev working?"
ver=$(printf '%s\n' "$info" | sed -n 's/.*"DDEV version": *"v\{0,1\}\([0-9][0-9.]*\).*/\1/p' | head -n 1)
gdir=$(printf '%s\n' "$info" | sed -n 's/.*"global-ddev-dir": *"\([^"]*\)".*/\1/p' | head -n 1)
[ -n "$ver" ] || fail "could not read the ddev version from 'ddev version -j'"
awk -v a="$ver" -v b="$min" 'BEGIN { split(a, x, "."); split(b, y, ".");
  for (i = 1; i <= 3; i++) { if (x[i] + 0 > y[i] + 0) exit 0; if (x[i] + 0 < y[i] + 0) exit 1 } exit 0 }' ||
  fail "ddev $ver is too old; ddev-bridge needs ddev v$min or newer (global hooks)"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in linux|darwin) ;; *) fail "unsupported OS $os (use Linux, WSL2 or macOS)" ;; esac
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) fail "unsupported CPU $(uname -m)" ;;
esac
b="$here/ddev-bridge-dist/host/$os-$arch/ddev-bridge"
[ -x "$b" ] || fail "$b not found; unpack the whole ddev-bridge.tar.gz"

if [ -n "$gdir" ]; then
  "$b" setup --global-dir "$gdir" "$@"
else
  "$b" setup "$@"
fi
# An invalid global_config.yaml stops every ddev command; catch that here.
ddev version >/dev/null 2>&1 || fail "ddev fails after setup; check the hooks in ${gdir:-the ddev global dir}/global_config.yaml"
