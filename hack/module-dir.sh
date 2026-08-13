#!/usr/bin/env bash
# Print the source directory of a Go module in the build list, downloading it
# into the module cache first if it is not already extracted there.
#
# `go list -m -f {{.Dir}}` does NOT download. On a cold module cache it prints
# an empty line and exits 0 — a silent failure, which is the dangerous part:
# interpolated into a path it produces something like "/config/crd/bases/..."
# with a leading slash, and the error surfaces as a missing file somewhere
# unrelated to the actual cause. That is exactly what broke CI, on runners whose
# cache happened to be cold, while passing everywhere the cache was warm.
#
# Usage: module-dir.sh <module-path>
#   e.g. module-dir.sh go.miloapis.com/dns-operator

set -euo pipefail

MODULE="${1:?usage: module-dir.sh <module-path>}"

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$repo_root"

resolve() {
  # Discard stderr: a cold cache makes `go list` chatter about downloading the
  # toolchain, which would otherwise end up in the path.
  go list -m -f '{{.Dir}}' "$MODULE" 2>/dev/null || true
}

dir="$(resolve)"

if [ -z "$dir" ]; then
  echo "module ${MODULE} is not extracted in the module cache; downloading" >&2
  if ! go mod download "$MODULE" >&2; then
    echo "❌ could not download ${MODULE}." >&2
    echo "   Either it is not in this module's build list — \`go list -m ${MODULE}\`" >&2
    echo "   should name it — or the module proxy is unreachable." >&2
    exit 1
  fi
  dir="$(resolve)"
fi

if [ -z "$dir" ]; then
  echo "❌ could not resolve a directory for ${MODULE} after downloading it." >&2
  echo "   Is it in the build list? \`go list -m ${MODULE}\` should name it." >&2
  exit 1
fi

if [ ! -d "$dir" ]; then
  echo "❌ ${MODULE} resolved to ${dir}, which is not a directory" >&2
  exit 1
fi

echo "$dir"
