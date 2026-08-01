#!/usr/bin/env bash
set -euo pipefail

# Notifies the downstream consumer repository that a Gentle AI release has
# published and passed remote asset verification (design D8; spec
# downstream-release-notification.md).
#
# Credential-gated, inert by absence: when DOWNSTREAM_DISPATCH_TOKEN is not
# configured -- true today, since the consumer's receiver workflow does not
# exist yet -- this exits 0 after printing a skip line and attempts no
# network call. Once the token is configured on the target repository's
# default branch, the exact same script starts dispatching without any
# further code change here.
#
# The payload carries the release tag only. The consumer downloads and
# verifies checksums.txt, its minisign signature, and the assets archive
# against the authoritative published release regardless, so any other field
# (version, commit, archive name, contract major) is either redundant with
# what the consumer must fetch anyway or a second source of truth that would
# have to stay in sync with the release by hand. No digest is carried either:
# the consumer re-derives every digest from the signed checksums.txt, so a
# compromised dispatch can never pin a false one.

readonly downstream_repository="Gentleman-Programming/gentle-pi"
readonly dispatch_event_type="gentle-ai-release"

die() {
  printf 'downstream release notification: %s\n' "$*" >&2
  exit 1
}

: "${RELEASE_TAG:?RELEASE_TAG is required}"
[[ "$RELEASE_TAG" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || die "tag is not exact stable semver"

if [[ -z "${DOWNSTREAM_DISPATCH_TOKEN:-}" ]]; then
  printf 'downstream release notification: skipped, no dispatch credential configured\n'
  exit 0
fi

payload=$(printf '{"event_type":"%s","client_payload":{"tag":"%s"}}' "$dispatch_event_type" "$RELEASE_TAG")

curl --fail --silent --show-error \
  --request POST \
  --header "Accept: application/vnd.github+json" \
  --header "Authorization: Bearer ${DOWNSTREAM_DISPATCH_TOKEN}" \
  --header "X-GitHub-Api-Version: 2022-11-28" \
  "https://api.github.com/repos/${downstream_repository}/dispatches" \
  --data "$payload"

printf 'downstream release notification: dispatched %s to %s\n' "$RELEASE_TAG" "$downstream_repository"
