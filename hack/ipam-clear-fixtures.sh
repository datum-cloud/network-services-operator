#!/usr/bin/env bash
# Release the addresses a previous run left held in the fixture projects.
#
# Only IPClaims and IPAllocations. That is measured, not assumed:
#
#   * A full suite run starting with 15 stale IPPools and 5 IPClasses and no
#     leftover claims passes 25/25. Seeding uses `apply`, so re-applying
#     identical classes and pools is a no-op, and cascade-provisioned pools are
#     keyed by (class, scope digest) and get reused rather than duplicated —
#     the pool count converges instead of growing.
#   * A full suite run starting with one leftover IPClaim fails, twice over.
#     Suites that enumerate the project's claims see the stranger and assert
#     against it: "the rebuilt interface publishes 2001:db8:a005::/96 where
#     IPAM holds 2001:db8:a006::/96".
#
# So pools and classes are stable and need no clearing; claims and allocations
# hold addresses and change what the next run sees.
#
# Order matters. An IPAllocation bound to an IPClaim refuses deletion —
# "IPAllocation is bound to IPClaim ...; delete the claim instead" — so claims
# go first and the allocations they held follow.
#
# Usage: ipam-clear-fixtures.sh [project...]
#
# The kube-context argument this used to take is gone: each project is reached
# through its own control-plane path on Milo, so there is no ambient context to
# choose.

set -euo pipefail

PROJECTS=("$@")
if [ ${#PROJECTS[@]} -eq 0 ]; then
  PROJECTS=(project-alpha project-beta)
fi

DELETE_TIMEOUT="${IPAM_CLEAR_TIMEOUT:-60s}"
KINDS="ipclaims ipallocations"

here="$(cd "$(dirname "$0")" && pwd)"

# Every call reaches IPAM through a project's control-plane path; a request
# that named no project would read nothing, which would make an empty result
# look like a clean tenant.
k() {
  "${here}/ipam-project-kubectl.sh" "$@"
}

# Positive control. Reading a type that always resolves proves the aggregated
# API is reachable through this project's path. Without it, an unreachable
# front door would make every delete below a no-op and this script would report
# a clean tenant it never touched.
verify_reachable() {
  local proj="$1" out
  if ! out="$(k "$proj" get ipclasses 2>&1)"; then
    echo "❌ cannot reach IPAM through ${proj}'s control-plane path; refusing to report a clean tenant" >&2
    echo "   ${out}" >&2
    exit 1
  fi
}

attempt_delete() {
  local proj="$1" kind="$2" out rc
  set +e
  out="$(k "$proj" delete "$kind" --all --all-namespaces --ignore-not-found \
    --timeout="$DELETE_TIMEOUT" 2>&1)"
  rc=$?
  set -e
  if [ "$rc" -ne 0 ] && ! printf '%s' "$out" | grep -qiE 'not found|no matches for'; then
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

  for kind in $KINDS; do
    attempt_delete "$proj" "$kind"
  done

  left=""
  for kind in $KINDS; do
    found="$(residue "$proj" "$kind")"
    [ -n "$found" ] && left="${left}${kind}: ${found}"$'\n'
  done
  if [ -n "$left" ]; then
    echo "❌ ${proj} still holds addresses after clearing:" >&2
    printf '%s' "$left" >&2
    exit 1
  fi

  echo "cleared held addresses in ${proj}"
done
