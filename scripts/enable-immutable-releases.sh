#!/usr/bin/env bash

set -euo pipefail

repo="${1:-${GITHUB_REPOSITORY:-tesh254/raph}}"

gh api \
  -X PUT \
  -H "Accept: application/vnd.github+json" \
  -H "X-GitHub-Api-Version: 2026-03-10" \
  "repos/${repo}/immutable-releases"
