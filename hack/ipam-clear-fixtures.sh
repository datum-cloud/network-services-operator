#!/usr/bin/env bash
# Delete every IPAM object in the fixture projects.
#
# IPPool and IPClass are cluster-scoped, so chainsaw's namespace teardown never
# reaches them. A suite that dies partway leaves its pools and classes behind,
# and the next run fails on "already exists" for a reason unrelated to what it
# was testing.
#
# Order matters: a bound allocation refuses deletion of the pool it came from,
# and a class with pools offering it refuses deletion too. Claims first,
# allocations next, then pools, then classes.
#
# This script exists to guarantee a clean slate, so it must never report one it
# did not produce. It proves it can reach each tenant before deleting anything,
# and it verifies all four kinds are empty afterwards.
#
# Adapted from github.com/milo-os/ipam test/e2e/lib/clear-tenant-fixtures.sh at
# ref 20865afe018c.
#
# Usage: ipam-clear-fixtures.sh <kube-context> [project...]

set -euo pipefail

CONTEXT="${1:?usage: ipam-clear-fixtures.sh <kube-context> [project...]}"
shift
PROJECTS=("$@")
if [ ${#PROJECTS[@]} -eq 0 ]; then
  PROJECTS=(project-alpha project-beta)
fi

DELETE_TIMEOUT="${IPAM_CLEAR_TIMEOUT:-60s}"
KINDS="ipclaims ipallocations ippools ipclasses"

here="$(cd "$(dirname "$0")" && pwd)"

# Every call reaches IPAM as a project; a request without the project extras
# reads nothing, which would make an empty result look like a clean tenant.
k() {
  IPAM_KUBE_CONTEXT="$CONTEXT" "${here}/ipam-tenant-kubectl.sh" "$@"
}

# Positive control. Reading a type that always resolves proves the aggregated
# API is reachable AND that this context's impersonation is accepted. Without
# it, an unreachable cluster or a wrong context name would make every delete
# below a no-op and this script would report a clean tenant it never touched.
verify_reachable() {
  local proj="$1" out
  if ! out="$(k "$proj" get ipclasses 2>&1)"; then
    echo "❌ cannot reach IPAM as ${proj}; refusing to report a clean tenant" >&2
    echo "   ${out}" >&2
    exit 1
  fi
}

# A delete that fails because the object is already gone is success. A pool
# pinned by a child that has not been deleted yet is expected mid-loop and is
# resolved by the next pass — the residue check at the end is what decides
# whether clearing actually worked. Anything else is surfaced rather than
# swallowed.
attempt_delete() {
  local proj="$1" kind="$2" out rc
  set +e
  out="$(k "$proj" delete "$kind" --all --all-namespaces --ignore-not-found \
    --timeout="$DELETE_TIMEOUT" 2>&1)"
  rc=$?
  set -e
  if [ "$rc" -ne 0 ] && ! printf '%s' "$out" | grep -qiE 'not found|no matches for|cannot delete IPPool'; then
    echo "   ⚠️  deleting ${kind} in ${proj}: ${out}" >&2
  fi
}

residue() {
  local proj="$1" kind="$2"
  k "$proj" get "$kind" --all-namespaces \
    -o jsonpath='{range .items[*]}{.metadata.namespace}/{.metadata.name} {end}' 2>/dev/null || true
}

for proj in "${PROJECTS[@]}"; do
  verify_reachable "$proj"

  attempt_delete "$proj" ipclaims
  attempt_delete "$proj" ipallocations

  # Pools need more than one pass. A cascade leaves child pools carved out of
  # their parent, and the parent refuses to go while a carve is outstanding —
  # but `delete --all` works through the list in name order, so a parent is
  # usually attempted before the children that pin it. Each pass frees one
  # level of the chain; the chain is at most a few deep.
  for _ in 1 2 3 4; do
    [ -z "$(residue "$proj" ippools)" ] && break
    attempt_delete "$proj" ippools
  done

  attempt_delete "$proj" ipclasses

  # The authoritative check, over every kind. Pools and classes block the next
  # run with "already exists"; a stranded claim or allocation is worse, because
  # a bound allocation refuses deletion of the pool it came from and wedges the
  # run after that one.
  left=""
  for kind in $KINDS; do
    found="$(residue "$proj" "$kind")"
    [ -n "$found" ] && left="${left}${kind}: ${found}"$'\n'
  done
  if [ -n "$left" ]; then
    echo "❌ ${proj} still holds IPAM fixtures after clearing:" >&2
    printf '%s' "$left" >&2
    exit 1
  fi

  echo "cleared IPAM fixtures in ${proj}"
done
