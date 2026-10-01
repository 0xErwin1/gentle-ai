#!/bin/sh
# DATA acquisition only. Verified source and an approved caller own effects.
# Pins and receipts identify bytes, never grant execution permission.
# Aggregate memory/CPU/PID/wall containment belongs to approved guest/Go callers.
# This helper is not same-UID takeover or independent loaded-byte attestation.
set -eu
umask 077
exec 3>&2 2>/dev/null
PATH=/usr/bin:/bin
export PATH
fail() { printf 'STOP: %s\n' "$1" >&3; exit 1; }
test "$#" = 4 || fail 'invalid arguments'
test "$1" = --destination && test "$3" = --node-archive || fail 'invalid arguments'
destination=$2 archive=$4
case "$destination" in /*) ;; *) fail 'destination must be absolute';; esac
case "$archive" in /*) ;; *) fail 'archive must be absolute';; esac
parent=$(dirname -- "$destination")
name=$(basename -- "$destination")
case "$name" in ''|.|..|*[!A-Za-z0-9_.-]*) fail 'invalid destination';; esac
private_parent() {
    test -d "$parent" && test ! -L "$parent" &&
    test "$(realpath -- "$parent")" = "$parent" &&
    test "$(stat -c %u "$parent")" = "$(id -u)" &&
    test "$(stat -c %a "$parent")" = 700
}
private_parent || fail 'parent must be owned private canonical directory'
test "$destination" = "$parent/$name" || fail 'invalid destination'
test ! -e "$destination" && test ! -L "$destination" || fail 'destination already exists'
parent_preimage=$(stat -c '%d:%i:%u:%a' "$parent")
# Fixed colocated production sources, not caller-supplied code or helper URLs.
owned=$(dirname -- "$0")
test "$(realpath -- "$owned")" = "$owned" || fail 'noncanonical source directory'
bootstrap=$owned/bootstrap-gentle-shell-private-node.sh
sri=$owned/complete-generated-lock-sri.mjs
source_preimages() {
    for source in "$0" "$bootstrap" "$sri"; do
        test -f "$source" && test ! -L "$source" && test -r "$source" || return 1
        test -z "$(find "$source" -perm /022 -print)" || return 1
        owner=$(stat -c %u "$source")
        test "$owner" = 0 || test "$owner" = "$(id -u)" || return 1
        stat -c '%d:%i:%u:%a:%s:%y:%z' "$source" || return 1
        sha256sum "$source" || return 1
    done
}
source_before=$(source_preimages) || fail 'unsafe owned source'
archive_before=$(stat -c '%d:%i:%s:%y:%z' "$archive") || fail 'archive absent'
stage=$(mktemp -d "$parent/.gentle-bundle-stage.XXXXXXXX")
cleanup() {
    status=$?
    trap - EXIT HUP INT TERM
    if test -n "$stage"; then
        if ! rm -rf -- "$stage"; then
            printf 'STOP: bundle cleanup failed; inspect destination\n' >&3
            status=1
        fi
    fi
    exit "$status"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
mkdir "$stage/result" "$stage/result/bundle" "$stage/home" "$stage/tmp" "$stage/config" "$stage/state"
bundle=$stage/result/bundle
# Delegation owns the sole archive pin/parser and stock version probes.
# Outer approved Bash supplies per-UID limits; this portable child checks CPU.
ulimit -t 240 || fail 'CPU limit failed'
if ! timeout --kill-after=5 90 env -i PATH=/usr/bin:/bin HOME="$stage/home" \
    /bin/sh "$bootstrap" --destination "$bundle/node" --node-archive "$archive" \
    >"$stage/bootstrap.log" 2>&1; then
    test "$(wc -c < "$stage/bootstrap.log")" -le 4096 && cat "$stage/bootstrap.log" >&3
    fail 'Node bootstrap failed'
fi
mkdir "$bundle/project" "$bundle/cache"
: > "$stage/config/user.npmrc"
: > "$stage/config/global.npmrc"
chmod 0600 "$stage/config/user.npmrc" "$stage/config/global.npmrc"
printf '%s\n' '{"name":"gentle-shell-private","version":"1.0.0","private":true,"dependencies":{"gentle-pi":"3.7.0","@earendil-works/pi-coding-agent":"0.85.1","@earendil-works/pi-tui":"0.85.1","@heyhuynhgiabuu/pi-pretty":"0.6.27","typebox":"1.3.7"}}' > "$bundle/project/package.json"
node=$bundle/node/bin/node
npm=$bundle/node/lib/node_modules/npm/bin/npm-cli.js
private_npm() {
    timeout --kill-after=5 240 env -i HOME="$stage/home" TMPDIR="$stage/tmp" \
        XDG_CONFIG_HOME="$stage/config" XDG_STATE_HOME="$stage/state" \
        PATH="$bundle/node/bin:/usr/bin:/bin" NPM_CONFIG_USERCONFIG="$stage/config/user.npmrc" \
        NPM_CONFIG_GLOBALCONFIG="$stage/config/global.npmrc" \
        "$node" --max-old-space-size=2048 "$npm" "$@" --ignore-scripts --engine-strict \
        --no-audit --no-fund --registry=https://registry.npmjs.org --cache="$bundle/cache"
}
(cd "$bundle/project" && private_npm install --package-lock-only) \
    >"$stage/resolve.log" 2>&1 || fail 'lock resolution failed'
timeout --kill-after=5 150 env -i HOME="$stage/home" TMPDIR="$stage/tmp" \
    XDG_CONFIG_HOME="$stage/config" XDG_STATE_HOME="$stage/state" PATH="$bundle/node/bin:/usr/bin:/bin" \
    NPM_CONFIG_USERCONFIG="$stage/config/user.npmrc" NPM_CONFIG_GLOBALCONFIG="$stage/config/global.npmrc" \
    "$node" --max-old-space-size=256 "$sri" "$bundle/project/package-lock.json" \
    >"$stage/sri.log" 2>&1 || fail 'SRI completion failed'
(cd "$bundle/project" && private_npm ci) >"$stage/cache.log" 2>&1 || fail 'cache acquisition failed'
# Reuse the supplier's stock metadata-only physical selection, not package JS.
mkdir "$stage/metadata"
(cd "$bundle/project" && find . -type f \
    \( -name package.json -o -name package-lock.json -o -name binding.gyp \) \
    -exec cp --parents -t "$stage/metadata" {} +) >"$stage/metadata.log" 2>&1 || fail 'metadata collection failed'
mv "$bundle/project" "$stage/acquired-project"
mv "$stage/metadata" "$bundle/project"
test -z "$(find "$bundle" ! -type f ! -type d -print -quit)" || fail 'nonregular bundle object'
find "$bundle" -printf '%P\n' | awk '
    $0 != "" && ($0 ~ /[^A-Za-z0-9_@.+\/-]/ || $0 ~ /(^|\/)\.\.?(\/|$)/) {bad=1}
    END {exit bad}
' || fail 'unsafe bundle path'
/bin/bash -o pipefail -c 'cd "$1" && find node cache project -type f -print0 | LC_ALL=C sort -z | xargs -0 sha256sum' \
    bash "$bundle" > "$bundle/SHA256SUMS" 2>"$stage/inventory.log" || fail 'bundle inventory failed'
receipt=$(sha256sum "$bundle/SHA256SUMS") || fail 'bundle receipt failed'
receipt=${receipt%% *}
printf '%s\n' "$receipt" > "$stage/result/bundle-receipt"
test "$(source_preimages)" = "$source_before" || fail 'owned source preimage changed'
test "$(stat -c '%d:%i:%s:%y:%z' "$archive")" = "$archive_before" || fail 'archive preimage changed'
private_parent && test "$(stat -c '%d:%i:%u:%a' "$parent")" = "$parent_preimage" || fail 'parent preimage changed'
test ! -e "$destination" && test ! -L "$destination" || fail 'destination appeared'
mv -T --no-clobber "$stage/result" "$destination" >"$stage/publication.log" 2>&1 || fail 'publication failed; inspect destination'
test ! -e "$stage/result" || fail 'publication refused'
# Publication may precede cleanup or transport failure; inspect state on uncertainty.
rm -rf -- "$stage" || fail 'published bundle cleanup failed; inspect destination'
stage=
printf 'Bundle acquisition only: bundle-sha256=%s package-install=not-run launch=not-run Ready=false\n' "$receipt"
