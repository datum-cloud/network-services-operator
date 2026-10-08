#!/usr/bin/env bash
# Generate the datumctl catalog entry for the alb plugin from a published
# release.
#
# The official catalog (datum-cloud/datumctl-plugins) pins every archive by
# SHA256, and its CI re-verifies them, so the entry cannot be written before the
# release exists. This reads the checksums the release publishes rather than
# hashing anything locally: a locally built archive is not the artifact users
# download, and pinning its hash would pass review and fail on install.
#
#   hack/alb-catalog-entry.sh v0.29.0 > alb.yaml
#
# Then open that as plugins/alb.yaml against datum-cloud/datumctl-plugins.
set -euo pipefail

TAG="${1:-}"
REPO="${ALB_CATALOG_REPO:-datum-cloud/network-services-operator}"

if [[ -z "$TAG" ]]; then
  echo "usage: $0 <release-tag>   e.g. $0 v0.29.0" >&2
  exit 2
fi

# name_template in .goreleaser-plugin.yaml, and the os/arch datumctl selects on.
# Keep the two in step: an archive named differently is silently absent here.
PLATFORMS=(
  "linux   amd64 datumctl-alb_Linux_x86_64.tar.gz"
  "linux   arm64 datumctl-alb_Linux_arm64.tar.gz"
  "darwin  amd64 datumctl-alb_Darwin_x86_64.tar.gz"
  "darwin  arm64 datumctl-alb_Darwin_arm64.tar.gz"
  "windows amd64 datumctl-alb_Windows_x86_64.zip"
)

SUMS="$(gh release view "$TAG" --repo "$REPO" --json assets \
  --jq '.assets[] | select(.name=="checksums.txt") | .url' 2>/dev/null || true)"

if [[ -z "$SUMS" ]]; then
  echo "error: release $TAG on $REPO publishes no checksums.txt." >&2
  echo "       The plugin archives are attached by .github/workflows/release-plugin.yml," >&2
  echo "       which only runs on a published release. Publish $TAG first." >&2
  exit 1
fi

CHECKSUMS="$(mktemp)"
trap 'rm -f "$CHECKSUMS"' EXIT
gh release download "$TAG" --repo "$REPO" --pattern checksums.txt --output "$CHECKSUMS" --clobber

cat <<HEADER
apiVersion: datumctl.datum.net/v1alpha1
kind: Plugin
metadata:
  name: alb
spec:
  shortDescription: Create and manage Application Load Balancers on Datum Cloud
  homepage: https://github.com/${REPO}
  version: ${TAG}
  platforms:
HEADER

missing=0
for row in "${PLATFORMS[@]}"; do
  read -r os arch file <<<"$row"
  sha="$(awk -v f="$file" '$2 == f || $2 == "*"f {print $1}' "$CHECKSUMS" | head -1)"
  if [[ -z "$sha" ]]; then
    echo "error: $file is not in the release's checksums.txt" >&2
    missing=1
    continue
  fi
  cat <<ENTRY
    - selector:
        matchLabels:
          os: ${os}
          arch: ${arch}
      uri: https://github.com/${REPO}/releases/download/${TAG}/${file}
      sha256: ${sha}
ENTRY
done

# A partial entry installs cleanly on the platforms it lists and fails on the
# rest, so refuse to emit one rather than leave the gap to review.
if (( missing )); then
  echo "error: entry is incomplete; not usable as plugins/alb.yaml" >&2
  exit 1
fi
