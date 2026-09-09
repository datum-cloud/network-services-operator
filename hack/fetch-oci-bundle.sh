#!/usr/bin/env bash
# Fetch a published manifest bundle from an OCI registry and extract it.
#
# Used for both the IPAM bundle and the dns-operator bundle. Sourcing manifests
# this way rather than from a Go module directory also removes a failure mode:
# `go list -m -f {{.Dir}}` returns an empty string on a cold module cache and
# exits 0, which silently produced paths with a leading slash.
#
# A bundle is a flux OCI artifact: a single layer of media type
# application/vnd.cncf.flux.content.v1.tar+gzip carrying the repo's config/
# tree. It has no org.opencontainers.image.title annotation, so `oras pull`
# extracts nothing; reading the layer blob directly works with any OCI client
# and needs no flux-specific tooling. We use crane because it installs with
# `go install` like every other pinned tool here, where the flux CLI's own
# go.mod replace directives make that fail.
#
# Pull is anonymous — the repository is public, so CI needs no registry
# credentials.
#
# The revision check takes whatever substring identifies the pinned version —
# a commit sha for a bundle pinned to a commit, a tag for one pinned to a
# release — and fails if the artifact does not carry it.
#
# Usage:
#   fetch-oci-bundle.sh <crane> <crane-version> <repo> <digest> <revision> <out-dir>

set -euo pipefail

CRANE="${1:?crane binary path required}"
CRANE_VERSION="${2:?crane version required}"
REPO="${3:?bundle repository required}"
DIGEST="${4:?bundle digest required}"
REVISION="${5:?expected revision required}"
OUT="${6:?output directory required}"

# Install crane on demand rather than making the caller remember a prep step.
# `up` does not depend on the tools task, and the workflow that runs the e2e
# suite installs its binaries from an explicit list, so requiring crane to be
# there already means it works on whichever machine happened to install it and
# nowhere else. Same shape as the tools task's own per-binary guard.
if [ ! -x "$CRANE" ]; then
  echo "🔧 installing crane ${CRANE_VERSION} into $(dirname "$CRANE")" >&2
  if ! GOBIN="$(cd "$(dirname "$CRANE")" && pwd)" \
      go install "github.com/google/go-containerregistry/cmd/crane@${CRANE_VERSION}" >&2; then
    echo "❌ could not install crane ${CRANE_VERSION}; needs Go and network access" >&2
    exit 1
  fi
fi

if [ ! -x "$CRANE" ]; then
  echo "❌ no crane binary at ${CRANE} after installing" >&2
  exit 1
fi

# ghcr.io occasionally times out on the first dial. A registry read is now on
# the critical path of every `up`, so retry rather than fail the whole bring-up
# on one flaky connection.
retry() {
  local attempt=1
  until "$@"; do
    if [ "$attempt" -ge 3 ]; then
      echo "❌ '$*' failed after ${attempt} attempts" >&2
      return 1
    fi
    echo "   retrying (${attempt}/3) after registry error" >&2
    attempt=$((attempt + 1))
    sleep $((attempt * 3))
  done
}

manifest="$(retry "$CRANE" manifest "${REPO}@${DIGEST}")"

field() {
  printf '%s' "$manifest" | python3 -c "import json,sys; print($1)"
}

# The artifact must carry the revision go.mod pins, or the manifests and the Go
# types NSO compiles against have drifted apart — exactly the failure the digest
# pin exists to prevent, so it is checked rather than assumed.
revision="$(field "json.load(sys.stdin).get('annotations',{}).get('org.opencontainers.image.revision','')")"
case "$revision" in
  *"$REVISION"*) ;;
  *)
    echo "❌ bundle revision '${revision}' does not carry the pinned revision ${REVISION}" >&2
    exit 1
    ;;
esac

layer="$(field "json.load(sys.stdin)['layers'][0]['digest']")"

rm -rf "$OUT"
mkdir -p "$OUT"
# Buffer the blob before extracting: piping crane straight into tar hides a
# mid-transfer registry failure behind tar's exit status.
blob="$(mktemp)"
trap 'rm -f "$blob"' EXIT
retry "$CRANE" blob "${REPO}@${layer}" > "$blob"
tar xz -C "$OUT" < "$blob"

if [ -z "$(ls -A "$OUT" 2>/dev/null)" ]; then
  echo "❌ extracted bundle at ${OUT} is empty" >&2
  exit 1
fi

echo "$revision"
