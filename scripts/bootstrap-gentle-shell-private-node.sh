#!/bin/sh
# The fixed accepted archive pins runtime bytes but grants no effect permission.
# The separately approved caller command owns guest execution authority.
set -eu
umask 077
# Withhold all raw stock diagnostics; only fixed bounded owned messages escape.
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
accepted=783130984963db7ba9cbd01089eaf2c2efb055c7c1693c943174b967b3050cb8
verify_archive() {
    test -f "$1" && test ! -L "$1" || fail 'archive must be regular and physical'
    test "$(wc -c < "$1")" = 57224421 || fail 'archive size differs'
    test "$(sha256sum "$1" | cut -d ' ' -f1)" = "$accepted" || fail 'archive hash differs'
}
verify_archive "$archive"
archive_preimage=$(stat -c '%d:%i:%s:%y:%z' "$archive")
stage=$(mktemp -d "$parent/.gentle-node-stage.XXXXXXXX")
cleanup() {
    status=$?
    trap - EXIT HUP INT TERM
    if test -n "$stage"; then
        if ! rm -rf -- "$stage"; then
            printf 'STOP: bootstrap cleanup failed\n' >&3
            status=1
        fi
    fi
    exit "$status"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
# All potentially verbose stock diagnostics stay private and are wholly withheld.
cp -- "$archive" "$stage/node.tgz" >"$stage/diagnostic" 2>&1 || fail 'staged archive copy failed'
test -f "$archive" && test ! -L "$archive" &&
    test "$(stat -c '%d:%i:%s:%y:%z' "$archive")" = "$archive_preimage" || fail 'archive preimage changed'
verify_archive "$stage/node.tgz"
mkdir "$stage/original" "$stage/node" "$stage/home" "$stage/tmp" "$stage/config" "$stage/state"
: > "$stage/config/user.npmrc"
: > "$stage/config/global.npmrc"
# The exact accepted archive is the only archive ever parsed.
tar -xzf "$stage/node.tgz" -C "$stage/original" --strip-components=1 \
    >"$stage/diagnostic" 2>&1 || fail 'archive extraction failed'
find "$stage/original" -type l -exec sh -c '
    root=$1; shift
    for p do case "$(realpath "$p")" in "$root"/*) ;; *) exit 1;; esac; done
' sh "$stage/original" {} + >"$stage/diagnostic" 2>&1 || fail 'archive symlink escaped'
test -z "$(find "$stage/original" ! -type f ! -type d ! -type l -print -quit)" || fail 'nonregular archive object'
cp -RL "$stage/original/." "$stage/node/" >"$stage/diagnostic" 2>&1 || fail 'physical normalization failed'
test -z "$(find "$stage/node" ! -type f ! -type d -print -quit)" || fail 'nonregular Node object'
# Restrict names to the complete stock inventory grammar used by the installer.
find "$stage/node" -printf '%P\n' | awk '
    $0 != "" && ($0 ~ /[^A-Za-z0-9_@.+\/-]/ || $0 ~ /(^|\/)\.\.?(\/|$)/) {bad=1}
    END {exit bad}
' || fail 'unknown Node path'
node=$stage/node/bin/node
npm=$stage/node/lib/node_modules/npm/bin/npm-cli.js
test -x "$node" && test -f "$npm" || fail 'stock runtime absent'
probe() {
    timeout --kill-after=2 15 env -i HOME="$stage/home" TMPDIR="$stage/tmp" \
        XDG_CONFIG_HOME="$stage/config" XDG_STATE_HOME="$stage/state" \
        PATH="$stage/node/bin:/usr/bin:/bin" NPM_CONFIG_USERCONFIG="$stage/config/user.npmrc" \
        NPM_CONFIG_GLOBALCONFIG="$stage/config/global.npmrc" \
        "$node" --max-old-space-size=256 "$@"
}
# Outer Bash supplies per-UID PID limits. This portable shell supplies CPU time.
ulimit -t 60
probe --version >"$stage/probe" 2>"$stage/diagnostic" || fail 'stock Node probe failed'
test "$(cat "$stage/probe")" = v24.18.0 || fail 'stock Node version differs'
probe "$npm" --version >"$stage/probe" 2>"$stage/diagnostic" || fail 'stock npm probe failed'
test "$(cat "$stage/probe")" = 11.16.0 || fail 'stock npm version differs'
# DATA provenance and full regular-file inventory are not execution authority.
printf 'accepted-archive-sha256=%s\nbytes=57224421\nNode=24.18.0\nnpm=11.16.0\n' "$accepted" \
    > "$stage/node/BOOTSTRAP-PROVENANCE"
/bin/bash -o pipefail -c 'cd "$1" && find . -type f -print0 | LC_ALL=C sort -z | xargs -0 sha256sum' \
    bash "$stage/node" > "$stage/inventory" 2>"$stage/diagnostic" || fail 'inventory failed'
mv "$stage/inventory" "$stage/node/BOOTSTRAP-SHA256SUMS"
private_parent && test "$(stat -c '%d:%i:%u:%a' "$parent")" = "$parent_preimage" || fail 'parent preimage changed'
test ! -e "$destination" && test ! -L "$destination" || fail 'destination appeared'
mv -T --no-clobber "$stage/node" "$destination" >"$stage/diagnostic" 2>&1 || fail 'publication failed; inspect destination'
test ! -e "$stage/node" || fail 'publication refused'
# Publication can precede a transport/cleanup failure: callers must inspect state.
rm -rf -- "$stage" || fail 'published bootstrap cleanup failed'
stage=
printf 'Node bootstrap only: Node=24.18.0 npm=11.16.0 package-install=not-run launch=not-run Ready=false\n'
