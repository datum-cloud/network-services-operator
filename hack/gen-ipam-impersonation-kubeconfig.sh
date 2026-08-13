#!/usr/bin/env bash
# Write a kubeconfig whose contexts drive the IPAM aggregated apiserver as a
# specific project.
#
# IPAM scopes storage per tenant from three UserInfo.Extra keys
# (iam.miloapis.com/parent-api-group, parent-type, parent-name). Milo's front
# gate normally supplies them as X-Remote-Extra-* requestheader extras. A
# kubeconfig's as-user-extra becomes Impersonate-Extra-*, which the front proxy
# re-emits as X-Remote-Extra-* — the same place IPAM reads from. A request
# carrying none of the three reads nothing and its writes are refused, so every
# fixture and every assertion has to go through one of these contexts.
#
# Adapted from github.com/milo-os/ipam test/e2e/lib/gen-impersonation-kubeconfig.sh
# at ref 20865afe018c. The cross-project group is dropped: this env seeds no
# shared pools.
#
# Usage: gen-ipam-impersonation-kubeconfig.sh <out-path> <source-context> [project...]

set -euo pipefail

OUT="${1:?usage: gen-ipam-impersonation-kubeconfig.sh <out-path> <source-context> [project...]}"
SRC_CTX="${2:?source context required}"
shift 2
PROJECTS=("$@")
if [ ${#PROJECTS[@]} -eq 0 ]; then
  PROJECTS=(project-alpha project-beta)
fi

TENANT_USER="e2e-tenant-tester"
PARENT_API_GROUP="resourcemanager.miloapis.com"
PARENT_TYPE="Project"

# The output is assembled by hand rather than round-tripped through
# `kubectl config view`: kubectl v1.36 drops as / as-user-extra when it
# re-serialises a kubeconfig, which would silently remove the impersonation.
base="$(mktemp)"
trap 'rm -f "$base"' EXIT
kubectl --context "$SRC_CTX" config view --minify --flatten >"$base"

BASE_CLUSTER="$(KUBECONFIG="$base" kubectl config view -o jsonpath="{.contexts[?(@.name=='${SRC_CTX}')].context.cluster}")"
BASE_USER="$(KUBECONFIG="$base" kubectl config view -o jsonpath="{.contexts[?(@.name=='${SRC_CTX}')].context.user}")"

C_SERVER="$(KUBECONFIG="$base" kubectl config view --raw -o jsonpath="{.clusters[?(@.name=='${BASE_CLUSTER}')].cluster.server}")"
C_CA="$(KUBECONFIG="$base" kubectl config view --raw -o jsonpath="{.clusters[?(@.name=='${BASE_CLUSTER}')].cluster.certificate-authority-data}")"
C_INSECURE="$(KUBECONFIG="$base" kubectl config view --raw -o jsonpath="{.clusters[?(@.name=='${BASE_CLUSTER}')].cluster.insecure-skip-tls-verify}")"

B_CERT="$(KUBECONFIG="$base" kubectl config view --raw -o jsonpath="{.users[?(@.name=='${BASE_USER}')].user.client-certificate-data}")"
B_KEY="$(KUBECONFIG="$base" kubectl config view --raw -o jsonpath="{.users[?(@.name=='${BASE_USER}')].user.client-key-data}")"
B_TOKEN="$(KUBECONFIG="$base" kubectl config view --raw -o jsonpath="{.users[?(@.name=='${BASE_USER}')].user.token}")"

emit_base_credentials() {
  [ -n "$B_CERT" ] && printf '%s\n' "    client-certificate-data: ${B_CERT}"
  [ -n "$B_KEY" ] && printf '%s\n' "    client-key-data: ${B_KEY}"
  [ -n "$B_TOKEN" ] && printf '%s\n' "    token: ${B_TOKEN}"
  return 0
}

{
  printf '%s\n' "apiVersion: v1"
  printf '%s\n' "kind: Config"
  printf '%s\n' "current-context: tenant-platform"
  printf '%s\n' "clusters:"
  printf '%s\n' "- name: ${BASE_CLUSTER}"
  printf '%s\n' "  cluster:"
  printf '%s\n' "    server: ${C_SERVER}"
  [ -n "$C_CA" ] && printf '%s\n' "    certificate-authority-data: ${C_CA}"
  [ "$C_INSECURE" = "true" ] && printf '%s\n' "    insecure-skip-tls-verify: true"
  printf '%s\n' "users:"
  printf '%s\n' "- name: ${BASE_USER}"
  printf '%s\n' "  user:"
  emit_base_credentials
  for proj in "${PROJECTS[@]}"; do
    printf '%s\n' "- name: tenant-${proj}-as"
    printf '%s\n' "  user:"
    emit_base_credentials
    printf '%s\n' "    as: ${TENANT_USER}"
    printf '%s\n' "    as-user-extra:"
    printf '%s\n' "      iam.miloapis.com/parent-api-group:"
    printf '%s\n' "        - ${PARENT_API_GROUP}"
    printf '%s\n' "      iam.miloapis.com/parent-type:"
    printf '%s\n' "        - ${PARENT_TYPE}"
    printf '%s\n' "      iam.miloapis.com/parent-name:"
    printf '%s\n' "        - ${proj}"
  done
  printf '%s\n' "contexts:"
  for proj in "${PROJECTS[@]}"; do
    printf '%s\n' "- name: tenant-${proj}"
    printf '%s\n' "  context:"
    printf '%s\n' "    cluster: ${BASE_CLUSTER}"
    printf '%s\n' "    user: tenant-${proj}-as"
  done
  printf '%s\n' "- name: tenant-platform"
  printf '%s\n' "  context:"
  printf '%s\n' "    cluster: ${BASE_CLUSTER}"
  printf '%s\n' "    user: ${BASE_USER}"
} >"$OUT"

echo "wrote ${OUT} (source context ${SRC_CTX})"
echo "  contexts: tenant-platform, $(printf 'tenant-%s ' "${PROJECTS[@]}")"
