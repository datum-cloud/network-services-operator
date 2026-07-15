#!/usr/bin/env bash
# Mint an already-expired self-signed TLS certificate and emit it as a
# kubernetes.io/tls Secret on stdout. Test-env-only: it hands the gateway an
# expired certificate directly, so the extension server's removal of unusable
# certificates can be exercised on its own, without the earlier check rejecting
# it first.
#
# Usage: mint-expired-secret.sh <namespace> <secret-name> <hostname>
set -euo pipefail

NS="${1:?namespace required}"
SECRET="${2:?secret name required}"
HOST="${3:?hostname required}"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

# Generate a key + a self-signed cert dated entirely in the past so it is expired
# the moment it is created. openssl's -not_before/-not_after (LibreSSL/OpenSSL 3)
# set an explicit validity window; fall back to a 1-second window via -days 0 if
# the flags are unavailable.
openssl req -x509 -newkey rsa:2048 -nodes \
  -keyout "$WORK/tls.key" -out "$WORK/tls.crt" \
  -subj "/CN=${HOST}" \
  -addext "subjectAltName=DNS:${HOST}" \
  -not_before 20200101000000Z -not_after 20200102000000Z 2>/dev/null \
  || openssl req -x509 -newkey rsa:2048 -nodes \
       -keyout "$WORK/tls.key" -out "$WORK/tls.crt" \
       -subj "/CN=${HOST}" -addext "subjectAltName=DNS:${HOST}" -days 1 2>/dev/null

CRT_B64="$(base64 < "$WORK/tls.crt" | tr -d '\n')"
KEY_B64="$(base64 < "$WORK/tls.key" | tr -d '\n')"

cat <<YAML
apiVersion: v1
kind: Secret
metadata:
  name: ${SECRET}
  namespace: ${NS}
type: kubernetes.io/tls
data:
  tls.crt: ${CRT_B64}
  tls.key: ${KEY_B64}
YAML
