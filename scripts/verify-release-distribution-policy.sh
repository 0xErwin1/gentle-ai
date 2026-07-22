#!/usr/bin/env bash
set -euo pipefail

die() {
  printf 'release distribution policy: %s\n' "$*" >&2
  exit 1
}

config=${1:-.goreleaser.yaml}
workflow=${2:-.github/workflows/release.yml}
[[ -f "$config" ]] || die "GoReleaser config is missing: $config"

# Official Windows binaries stay omitted until the restoration gate documented
# in docs/release-signing.md is fully provisioned. Match the whole config so an
# inline target or renamed archive stanza cannot bypass this pre-publication gate.
if grep -Eiq '(^|[^[:alnum:]_])windows([^[:alnum:]_]|$)' "$config"; then
  die "Windows release targets are disabled until public-trust Authenticode signing is enforced"
fi
if grep -Eiq '(^|[^[:alnum:]_])scoops?([^[:alnum:]_]|$)' "$config"; then
  die "Scoop publication is disabled while Windows binaries are omitted"
fi
if [[ -f "$workflow" ]] && grep -Eiq 'mock[^[:cntrl:]]*sign' "$workflow"; then
  die "mock signing is forbidden"
fi

printf 'release distribution policy: Windows and Scoop outputs omitted\n'
