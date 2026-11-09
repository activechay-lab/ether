#!/bin/sh
# build-web.sh — build the React/Vite owner console into internal/admin/dist,
# where internal/admin embeds it with //go:embed all:dist.
set -eu

# shellcheck disable=SC1007
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)

command -v pnpm >/dev/null 2>&1 || {
  echo "build-web.sh: pnpm is required to build the web console (https://pnpm.io/installation)" >&2
  exit 1
}

# Vite bundles CodeMirror, TypeScript VFS and 19 locale tables (>2 GiB peak heap);
# ensure 4 GiB V8 ceiling on <=4 GiB containers/VMs unless the caller set one.
case "${NODE_OPTIONS:-}" in
  *max-old-space-size*) ;;
  *) export NODE_OPTIONS="${NODE_OPTIONS:+$NODE_OPTIONS }--max-old-space-size=4096" ;;
esac

cd "$ROOT/web"
pnpm install --frozen-lockfile
pnpm build

if [ ! -f "$ROOT/internal/admin/dist/index.html" ]; then
  echo "build-web.sh: expected $ROOT/internal/admin/dist/index.html after pnpm build" >&2
  exit 1
fi
