#!/bin/sh
# Guest-only wrapper controls; the unchanged Node suite chains private install.
set -eu
umask 077
env -i PATH=/usr/bin:/bin HOME=/tmp HOST_NETNS="$HOST_NETNS" HOST_MNTNS="$HOST_MNTNS" \
    /bin/sh /usr/local/bin/shell-linux-guest-proof.sh
mkdir /tmp/bundle-parent /tmp/bundle-fixture /tmp/bundle-fixture/.pi
printf 'global Pi sentinel\n' > /tmp/bundle-fixture/.pi/canary
provider=/usr/local/bin/acquire-gentle-shell-private-bundle.sh
inventory() {
    /bin/bash -o pipefail -c 'find /tmp/bundle-fixture -printf "%P\t%y\t%m\t%U\t%G\0" | LC_ALL=C sort -z | sha256sum'
}
before=$(inventory)
canary=$(sha256sum /tmp/bundle-fixture/.pi/canary)
sources() {
    for source in "$provider" /usr/local/bin/bootstrap-gentle-shell-private-node.sh \
        /usr/local/bin/complete-generated-lock-sri.mjs /node.tgz; do
        stat -c '%d:%i:%u:%g:%a:%s:%y:%z' "$source"
        sha256sum "$source"
    done
}
source_before=$(sources)
invoke() {
    env -i PATH=/usr/bin:/bin HOME=/tmp/bundle-fixture /bin/sh "$provider" "$@"
}
unchanged() {
    test "$(inventory)" = "$before"
    test "$(sha256sum /tmp/bundle-fixture/.pi/canary)" = "$canary"
    test "$(sources)" = "$source_before"
    test -z "$(find /tmp/bundle-parent -name '.gentle-bundle-stage.*' -o -name '.gentle-node-stage.*')"
    test ! -e /tmp/bundle-parent/rejected && test ! -L /tmp/bundle-parent/rejected
}
assert_rejection() {
    test "$status" = 1
    test "$(printf '%s\n' "$output" | wc -c)" -le 4096
    case "$output" in *"STOP: $expected"*) ;; *) exit 1;; esac
    unchanged
    printf 'PASS: bundle rejection=%s cleanup=true fixture-Pi-unchanged=true source-unchanged=true\n' "$expected"
}
reject() {
    expected=$1; shift
    status=0
    output=$(invoke "$@" 2>&1) || status=$?
    assert_rejection
}
reject 'invalid arguments' --destination /tmp/bundle-parent/rejected
reject 'invalid arguments' --node-archive /node.tgz --destination /tmp/bundle-parent/rejected
reject 'destination must be absolute' --destination relative --node-archive /node.tgz
reject 'archive must be absolute' --destination /tmp/bundle-parent/rejected --node-archive relative
reject 'invalid destination' --destination /tmp/bundle-parent/.. --node-archive /node.tgz
reject 'invalid destination' --destination /tmp/bundle-parent/bad:name --node-archive /node.tgz
reject 'invalid destination' --destination /tmp/bundle-parent/rejected/ --node-archive /node.tgz
reject 'archive absent' --destination /tmp/bundle-parent/rejected --node-archive /tmp/bundle-archive-absent
# Parent boundary cases remain wrapper-owned, before bootstrap delegation.
for destination in /tmp/rejected /tmp/missing/rejected /tmp/bundle-parent/../bundle-parent/rejected /root/rejected; do
    reject 'parent must be owned private canonical directory' --destination "$destination" --node-archive /node.tgz
done
chmod 0755 /tmp/bundle-parent
reject 'parent must be owned private canonical directory' --destination /tmp/bundle-parent/rejected --node-archive /node.tgz
chmod 0700 /tmp/bundle-parent
ln -s /tmp/bundle-parent /tmp/bundle-link
reject 'parent must be owned private canonical directory' --destination /tmp/bundle-link/rejected --node-archive /node.tgz
mkdir /tmp/bundle-parent/existing
printf 'sentinel\n' > /tmp/bundle-parent/existing/keep
reject 'destination already exists' --destination /tmp/bundle-parent/existing --node-archive /node.tgz
test "$(cat /tmp/bundle-parent/existing/keep)" = sentinel
ln -s /tmp/bundle-fixture/.pi /tmp/bundle-parent/link
reject 'destination already exists' --destination /tmp/bundle-parent/link --node-archive /node.tgz
ln -s /tmp/bundle-absent /tmp/bundle-parent/dangling
reject 'destination already exists' --destination /tmp/bundle-parent/dangling --node-archive /node.tgz
printf 'file sentinel\n' > /tmp/bundle-parent/file
reject 'destination already exists' --destination /tmp/bundle-parent/file --node-archive /node.tgz
test "$(cat /tmp/bundle-parent/file)" = 'file sentinel'
ln -s /node.tgz /tmp/bundle-archive-link
mkdir /tmp/bundle-archive-directory
mkfifo /tmp/bundle-archive-fifo
for archive in /tmp/bundle-archive-link /tmp/bundle-archive-directory /tmp/bundle-archive-fifo; do
    reject 'archive must be regular and physical' --destination /tmp/bundle-parent/rejected --node-archive "$archive"
done
printf 'bad\n' > /tmp/bundle-archive-small
reject 'archive size differs' --destination /tmp/bundle-parent/rejected --node-archive /tmp/bundle-archive-small
cp /node.tgz /tmp/bundle-archive-bad
# Only this new private fault fixture becomes writable.
chmod 0600 /tmp/bundle-archive-bad
printf X | dd of=/tmp/bundle-archive-bad bs=1 seek=0 conv=notrunc status=none
reject 'archive hash differs' --destination /tmp/bundle-parent/rejected --node-archive /tmp/bundle-archive-bad
rm /tmp/bundle-archive-bad
# Genuine nested stock cp fault after both wrapper and bootstrap own stages.
status=0
expected='staged archive copy failed'
output=$(/bin/bash -c 'ulimit -f 1024 && exec env -i PATH=/usr/bin:/bin HOME=/tmp/bundle-fixture /bin/sh /usr/local/bin/acquire-gentle-shell-private-bundle.sh --destination /tmp/bundle-parent/rejected --node-archive /node.tgz' 2>&1) || status=$?
assert_rejection
printf 'PASS: real bundle post-staging cascade cleanup=true\n'
exec /bin/bash -c 'ulimit -t 240 && ulimit -u 64 && exec /bin/sh /usr/local/bin/shell-linux-node-bootstrap-controls.sh'
