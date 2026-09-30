#!/bin/sh
set -eu
# Preserve the unchanged stock allowlist/mount/network proof before candidate code.
env -i PATH=/usr/bin:/bin HOME=/tmp HOST_NETNS="$HOST_NETNS" HOST_MNTNS="$HOST_MNTNS" \
    /bin/sh /usr/local/bin/shell-linux-guest-proof.sh
umask 077
cat /acquisition-limits
mkdir /tmp/private /tmp/fixture /tmp/fixture/.pi
printf 'operator Pi canary: never reuse\n' > /tmp/fixture/.pi/canary
before=$(sha256sum /tmp/fixture/.pi/canary | cut -d ' ' -f1)
fixture_inventory() {
    /bin/bash -o pipefail -c 'find /tmp/fixture -printf "%P\t%y\t%m\t%U\t%G\0" 2>/dev/null | LC_ALL=C sort -z | sha256sum | cut -d " " -f1'
}
fixture_before=$(fixture_inventory)
receipt=$(cat /bundle-receipt)
test "$(sha256sum /bundle/SHA256SUMS | cut -d ' ' -f1)" = "$receipt"
(cd /bundle && sha256sum --strict -c SHA256SUMS >/dev/null)
installer=/usr/local/bin/install-gentle-shell-private.sh
command_install() {
    env -i PATH=/usr/bin:/bin HOME=/tmp/fixture /bin/sh "$installer" \
        --destination "$1" --bundle "$2" --bundle-sha256 "$3"
}
unchanged() {
    test "$(sha256sum /tmp/fixture/.pi/canary | cut -d ' ' -f1)" = "$before"
    fixture_after=$(fixture_inventory) || exit 1
    test "$fixture_after" = "$fixture_before"
    test -z "$(find /tmp/private -maxdepth 1 -name '.gentle-shell-stage.*' -print -quit)"
}
unexpected_fixture() {
    bytes=$(printf '%s' "$output" | wc -c)
    printf 'STOP: unexpected fixture=%s status=%s diagnostic-bytes=%s\n' "$1" "$status" "$bytes"
    if test "$bytes" -le 4096; then
        printf '%s\n' "$output" | awk '{print "|" $0}'
    else
        printf 'STOP: fixture diagnostic exceeds 4096-byte bound; content not printed.\n'
    fi
    exit 1
}
reject() {
    expected=$1; shift
    status=0
    output=$(command_install "$@" 2>&1) || status=$?
    test "$status" = 1 || unexpected_fixture "$expected"
    case "$output" in *"STOP: $expected"*) ;; *) unexpected_fixture "$expected" ;; esac
    unchanged
    printf 'PASS: rejection=%s exit=1 fixture-Pi-unchanged=true\n' "$expected"
}
reject 'bundle manifest digest mismatch' /tmp/private/rejected /bundle \
    0000000000000000000000000000000000000000000000000000000000000000
test ! -e /tmp/private/rejected
mkdir /tmp/private/nonempty
printf 'keep\n' > /tmp/private/nonempty/keep
reject 'destination already exists' /tmp/private/nonempty /bundle "$receipt"
test "$(cat /tmp/private/nonempty/keep)" = keep
ln -s /tmp/fixture/.pi /tmp/private/link
reject 'destination already exists' /tmp/private/link /bundle "$receipt"
reject 'destination must be absolute' relative /bundle "$receipt"
# Fresh fixture bundles are private tmpfs data, never operator content.
cp -R /bundle /tmp/bad
printf 'corrupt\n' >> /tmp/bad/project/package.json
reject 'bundle byte mismatch' /tmp/private/rejected /tmp/bad "$receipt"
test ! -e /tmp/private/rejected
rm -rf /tmp/bad
cp -R /bundle /tmp/bad
cache_file=$(find /tmp/bad/cache/_cacache/content-v2 -type f -print -quit)
test -n "$cache_file"
printf 'corrupt-cache\n' >> "$cache_file"
reject 'bundle byte mismatch' /tmp/private/rejected /tmp/bad "$receipt"
test ! -e /tmp/private/rejected
printf 'PASS: corrupted cache refused before runtime; cleanup=true fixture-Pi-unchanged=true\n'
rm -rf /tmp/bad
cp -R /bundle /tmp/bad
printf 'unlisted\n' > /tmp/bad/node/unlisted
reject 'bundle inventory differs' /tmp/private/rejected /tmp/bad "$receipt"
rm -rf /tmp/bad
cp -R /bundle /tmp/bad
ln -s /bundle/node/bin/node /tmp/bad/node/unlisted-link
reject 'nonregular bundle object' /tmp/private/rejected /tmp/bad "$receipt"
rm -rf /tmp/bad
cp -R /bundle /tmp/bad
# Alter a root SRI while preserving an otherwise internally hashed fixture.
env -i /bundle/node/bin/node --input-type=module - <<'JS'
import fs from 'node:fs';
const path = '/tmp/bad/project/package-lock.json';
const lock = JSON.parse(fs.readFileSync(path));
lock.packages['node_modules/gentle-pi'].integrity = 'sha512-' + 'A'.repeat(86) + '==';
fs.writeFileSync(path, JSON.stringify(lock));
JS
refresh() {
    (cd /tmp/bad && find node cache project -type f -print0 | LC_ALL=C sort -z | xargs -0 sha256sum > SHA256SUMS)
    bad_receipt=$(sha256sum /tmp/bad/SHA256SUMS | cut -d ' ' -f1)
}
refresh
status=0
output=$(command_install /tmp/private/rejected /tmp/bad "$bad_receipt" 2>&1) || status=$?
expected=false
case "$output" in *'root pin differs: gentle-pi'*) test "$status" = 0 || expected=true ;; esac
test "$expected" = true || unexpected_fixture 'corrupted root SRI'
unchanged
test ! -e /tmp/private/rejected
printf 'PASS: corrupted root SRI rejected before offline install; fixture-Pi-unchanged=true\n'
rm -rf /tmp/bad
mkdir /tmp/bad
cp -R /bundle/node /bundle/project /tmp/bad/
mkdir /tmp/bad/cache
refresh
reject 'offline npm install failed' /tmp/private/rejected /tmp/bad "$bad_receipt"
test ! -e /tmp/private/rejected
rm -rf /tmp/bad
# Required positive case: real physical private install, not package launch.
printf 'command: /bin/sh install-gentle-shell-private.sh --destination /tmp/private/stable --bundle /bundle --bundle-sha256 %s\n' "$receipt"
output=$(command_install /tmp/private/stable /bundle "$receipt" 2>&1) || { status=$?; unexpected_fixture 'positive installation'; }
test "${#output}" -le 12000
printf '%s\n' "$output" | awk '/^closure expected=[0-9]+ actual=[0-9]+$/ {split($2,e,"="); split($3,a,"="); count++; if(e[2]!=a[2]) bad=1} END {exit (count!=1 || bad)}'
printf '%s\n' "$output"
unchanged
test -x /tmp/private/stable/node/bin/node
test -f /tmp/private/stable/project/node_modules/gentle-pi/package.json
printf 'PASS: actual installation exit=0 fixture-Pi-canary-before=%s fixture-Pi-canary-after=%s Ready=false launch=not-run\n' "$before" "$before"
printf 'fixture path/type/mode/uid/gid inventory-before=%s inventory-after=%s universal-Pi-preservation=not-claimed\n' "$fixture_before" "$fixture_after"
