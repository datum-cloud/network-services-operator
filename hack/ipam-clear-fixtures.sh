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
# Usage: ipam-clear-fixtures.sh <impersonation-kubeconfig> [project...]

set -euo pipefail

KCFG="${1:?usage: ipam-clear-fixtures.sh <impersonation-kubeconfig> [project...]}"
shift
PROJECTS=("$@")
if [ ${#PROJECTS[@]} -eq 0 ]; then
  PROJECTS=(project-alpha project-beta)
fi

DELETE_TIMEOUT="${IPAM_CLEAR_TIMEOUT:-60s}"
KINDS="ipclaims ipallocations ippools ipclasses"

if [ ! -f "$KCFG" ]; then
  echo "❌ no impersonation kubeconfig at ${KCFG}" >&2
  exit 1
fi

k() {
  KUBECONFIG="$KCFG" kubectl --context "$1" "${@:2}"
}

# Positive control. Reading a type that always resolves proves the aggregated
# API is reachable AND that this context's impersonation is accepted. Without
# it, an unreachable cluster or a wrong context name would make every delete
# below a no-op and this script would report a clean tenant it never touched.
verify_reachable() {
  local ctx="$1" out
  if ! out="$(k "$ctx" get ipclasses 2>&1)"; then
    echo "❌ cannot reach IPAM as ${ctx}; refusing to report a clean tenant" >&2
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
  local ctx="$1" kind="$2" out rc
  set +e
  out="$(k "$ctx" delete "$kind" --all --all-namespaces --ignore-not-found \
    --timeout="$DELETE_TIMEOUT" 2>&1)"
  rc=$?
  set -e
  if [ "$rc" -ne 0 ] && ! printf '%s' "$out" | grep -qiE 'not found|no matches for|cannot delete IPPool'; then
    echo "   ⚠️  deleting ${kind} in ${ctx}: ${out}" >&2
  fi
}

residue() {
  local ctx="$1" kind="$2"
  k "$ctx" get "$kind" --all-namespaces \
    -o jsonpath='{range .items[*]}{.metadata.namespace}/{.metadata.name} {end}' 2>/dev/null || true
}

for proj in "${PROJECTS[@]}"; do
  ctx="tenant-${proj}"
  verify_reachable "$ctx"

  attempt_delete "$ctx" ipclaims
  attempt_delete "$ctx" ipallocations

  # Pools need more than one pass. A cascade leaves child pools carved out of
  # their parent, and the parent refuses to go while a carve is outstanding —
  # but `delete --all` works through the list in name order, so a parent is
  # usually attempted before the children that pin it. Each pass frees one
  # level of the chain; the chain is at most a few deep.
  for _ in 1 2 3 4; do
    [ -z "$(residue "$ctx" ippools)" ] && break
    attempt_delete "$ctx" ippools
  done

  attempt_delete "$ctx" ipclasses

  # The authoritative check, over every kind. Pools and classes block the next
  # run with "already exists"; a stranded claim or allocation is worse, because
  # a bound allocation refuses deletion of the pool it came from and wedges the
  # run after that one.
  left=""
  for kind in $KINDS; do
    found="$(residue "$ctx" "$kind")"
    [ -n "$found" ] && left="${left}${kind}: ${found}"$'\n'
  done
  if [ -n "$left" ]; then
    echo "❌ ${proj} still holds IPAM fixtures after clearing:" >&2
    printf '%s' "$left" >&2
    exit 1
  fi

  echo "cleared IPAM fixtures in ${proj}"
done
