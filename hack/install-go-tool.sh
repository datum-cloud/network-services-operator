#!/usr/bin/env bash
# Install a Go tool as BIN_DIR/NAME-VERSION with BIN_DIR/NAME symlinked to it,
# so a change to the pinned version replaces the tool on the next run.
# The same installer as datum-cloud/dns-operator's hack/install-go-tool.sh, with
# this repository's pins in the release table.
set -euo pipefail

if [ $# -ne 4 ]; then
  echo "usage: $0 BIN_DIR NAME PACKAGE VERSION" >&2
  exit 2
fi
bin_dir=$1
name=$2
package=$3
version=$4

mkdir -p "$bin_dir"
bin_dir=$(cd "$bin_dir" && pwd)
target="$bin_dir/$name-$version"
link="$bin_dir/$name"

if [ ! -x "$target" ]; then
  echo "Installing $package@$version into $target"
  rm -f "$link"
  # The hosted E2E job can use upstream releases for its exact tool pins.
  # Other platforms and newer versions keep the normal source install.
  asset=
  if [[ ${CI_RELEASE_TOOLS:-0} == 1 && $(uname -sm) == 'Linux x86_64' ]]; then
    case "$package@$version" in
      github.com/kyverno/chainsaw@v0.2.15)
        asset="kyverno/chainsaw/releases/download/$version/chainsaw_linux_amd64.tar.gz"
        digest=295d226c89f126c0a97775d364be149f47a810c8a3f9829ee410583d0c1abe3c
        ;;
      github.com/cert-manager/cmctl/v2@v2.1.1)
        asset="cert-manager/cmctl/releases/download/$version/cmctl_linux_amd64"
        digest=41d96165d5654f6988c48681deabd9cb1622a0d0ebd049f1a459e7e8877afea3
        ;;
      sigs.k8s.io/kustomize/kustomize/v5@v5.5.0)
        asset="kubernetes-sigs/kustomize/releases/download/kustomize/$version/kustomize_${version}_linux_amd64.tar.gz"
        digest=6703a3a70a0c47cf0b37694030b54f1175a9dfeb17b3818b623ed58b9dbc2a77
        ;;
      sigs.k8s.io/kind@v0.32.0)
        asset="kubernetes-sigs/kind/releases/download/$version/kind-linux-amd64"
        digest=50030de23cf40a18505f20426f6a8506bedf13c6e509244bd1fa9463721b0f54
        ;;
      github.com/google/go-containerregistry/cmd/crane@v0.21.7)
        asset="google/go-containerregistry/releases/download/$version/go-containerregistry_Linux_x86_64.tar.gz"
        digest=1a57bc98207fa1c0d04bf760699099e26f8383499bfd55b99c1b919a928a7230
        ;;
    esac
  fi
  if [[ -n $asset ]]; then
    stage=$(mktemp -d "$bin_dir/.install.XXXXXX")
    trap 'rm -rf "$stage"' EXIT
    curl --fail --silent --show-error --location --retry 3 "https://github.com/$asset" -o "$stage/asset"
    printf '%s  %s\n' "$digest" "$stage/asset" | sha256sum -c -
    if [[ $asset == *.tar.gz ]]; then
      tar -xOzf "$stage/asset" "$name" > "$stage/binary"
    else
      mv "$stage/asset" "$stage/binary"
    fi
    chmod 755 "$stage/binary"
    mv "$stage/binary" "$target"
  else
    GOBIN=$bin_dir go install "$package@$version"
    mv "$link" "$target"
  fi
fi
ln -sfn "$target" "$link"
