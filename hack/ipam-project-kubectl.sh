#!/usr/bin/env bash
# Run kubectl against IPAM through a project's control-plane path on Milo.
#
# This is the same route the operator takes. The project is in the URL and
# nowhere else: the caller's credential names no project, asserts no extras, and
# impersonates nobody. Milo resolves the project from the path and forwards it
# to IPAM as X-Remote-Extra-*, which is where IPAM reads the tenant from.
#
# This replaced impersonation (`--as` plus three --as-user-extra flags against
# the kind apiserver). That worked, but it let the CALLER choose the tenant,
# which is exactly the property the operator no longer relies on — so a suite
# built on it could not tell a correctly routed request from a request that
# simply asserted the right answer.
#
# The per-project kubeconfigs are written by test-infra:milo-kubeconfigs. Their
# server URL carries the path prefix; everything else about them is ordinary.
#
# Usage: ipam-project-kubectl.sh <project> [kubectl args...]
#   e.g. ipam-project-kubectl.sh project-alpha -n default get ipclaims

set -euo pipefail

PROJECT="${1:?usage: ipam-project-kubectl.sh <project> [kubectl args...]}"
shift

: "${TMPDIR:=/tmp}"
KUBECONFIG_PATH="${IPAM_PROJECT_KUBECONFIG:-${TMPDIR%/}/.milo-${PROJECT}.yaml}"

if [ ! -s "$KUBECONFIG_PATH" ]; then
  echo "no kubeconfig for project '${PROJECT}' at ${KUBECONFIG_PATH}" >&2
  echo "run 'task test-infra:milo-kubeconfigs' (or test-infra:up) first" >&2
  exit 1
fi

exec kubectl --kubeconfig "$KUBECONFIG_PATH" "$@"
