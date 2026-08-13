#!/usr/bin/env bash
# Run kubectl against IPAM as a project tenant.
#
# IPAM scopes storage from three UserInfo.Extra keys. Milo's front gate supplies
# them as X-Remote-Extra-* requestheader extras; kubectl's --as-user-extra
# becomes Impersonate-Extra-*, which the front proxy re-emits as the same
# X-Remote-Extra-*, so it lands in UserInfo.Extra identically. A request
# carrying none of the three reads nothing and its writes are refused.
#
# This replaced a generated kubeconfig holding a flattened copy of the caller's
# credentials. kubectl takes the extras as flags directly, so there is no
# generated artefact to create before anything can talk to IPAM, and no token
# written to disk.
#
# Usage: ipam-tenant-kubectl.sh <project> [kubectl args...]
#   e.g. ipam-tenant-kubectl.sh project-alpha -n default get ipclaims

set -euo pipefail

PROJECT="${1:?usage: ipam-tenant-kubectl.sh <project> [kubectl args...]}"
shift

# The identity impersonated. Deliberately not cluster-admin: its access comes
# from the nso-ipam-tenant binding in test/e2e/fixtures/ipam/rbac.yaml, so the
# cross-project deny assertions cannot pass by privilege.
: "${IPAM_TENANT_USER:=e2e-tenant-tester}"
# Set by callers that already know which cluster; otherwise kubectl's current
# context applies.
CONTEXT="${IPAM_KUBE_CONTEXT:-}"

set -- \
  --as="${IPAM_TENANT_USER}" \
  --as-user-extra="iam.miloapis.com/parent-api-group=resourcemanager.miloapis.com" \
  --as-user-extra="iam.miloapis.com/parent-type=Project" \
  --as-user-extra="iam.miloapis.com/parent-name=${PROJECT}" \
  "$@"

if [ -n "$CONTEXT" ]; then
  set -- --context "$CONTEXT" "$@"
fi

exec kubectl "$@"
