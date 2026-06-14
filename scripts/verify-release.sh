#!/usr/bin/env bash

set -euo pipefail

if [[ $# -lt 2 ]]; then
  echo "usage: $0 <tag> <artifact-path> [artifact-path...]" >&2
  exit 1
fi

tag="$1"
shift

repo="${GITHUB_REPOSITORY:-tesh254/raph}"

gh release verify "$tag" --repo "$repo"

for artifact in "$@"; do
  gh release verify-asset "$tag" "$artifact" --repo "$repo"
  gh attestation verify "$artifact" --repo "$repo"
done
