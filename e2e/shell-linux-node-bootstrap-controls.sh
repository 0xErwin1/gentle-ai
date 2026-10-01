#!/bin/sh
# Guest-only controls, followed by the unchanged private-install qualification.
set -eu
umask 077
env -i PATH=/usr/bin:/bin HOME=/tmp HOST_NETNS="$HOST_NETNS" HOST_MNTNS="$HOST_MNTNS" \
    /bin/sh /usr/local/bin/shell-linux-guest-proof.sh
mkdir /tmp/bootstrap-parent /tmp/bootstrap-fixture /tmp/bootstrap-fixture/.pi
printf 'global Pi sentinel\n' > /tmp/bootstrap-fixture/.pi/canary
inventory() {
    /bin/bash -o pipefail -c 'find /tmp/bootstrap-fixture -printf "%P\t%y\t%m\t%U\t%G\0" | LC_ALL=C sort -z | sha256sum'
}
before=$(inventory)
canary=$(sha256sum /tmp/bootstrap-fixture/.pi/canary)
bootstrap=/usr/local/bin/bootstrap-gentle-shell-private-node.sh
invoke() {
    env -i PATH=/usr/bin:/bin HOME=/tmp/bootstrap-fixture \
        /bin/sh "$bootstrap" "$@"
}
unchanged() {
    test "$(inventory)" = "$before"
    test "$(sha256sum /tmp/bootstrap-fixture/.pi/canary)" = "$canary"
    test -z "$(find /tmp/bootstrap-parent -name '.gentle-node-stage.*' -print -quit)"
    test ! -e /tmp/bootstrap-parent/rejected
}
reject() {
    expected=$1; shift
    status=0
    output=$(invoke "$@" 2>&1) || status=$?
    test "$status" = 1 || { printf 'STOP: wrong bootstrap rejection status\n'; exit 1; }
    test "$(printf '%s\n' "$output" | wc -c)" -le 4096 || exit 1
    case "$output" in *"STOP: $expected"*) ;; *) printf 'STOP: wrong bootstrap diagnostic\n'; exit 1;; esac
    unchanged
    printf 'PASS: bootstrap rejection=%s fixture-Pi-unchanged=true\n' "$expected"
}
reject 'invalid arguments' --destination /tmp/bootstrap-parent/rejected
reject 'destination must be absolute' --destination relative --node-archive /node.tgz
reject 'archive must be absolute' --destination /tmp/bootstrap-parent/rejected --node-archive relative
reject 'parent must be owned private canonical directory' --destination /tmp/rejected --node-archive /node.tgz
reject 'parent must be owned private canonical directory' --destination /tmp/missing/rejected --node-archive /node.tgz
reject 'parent must be owned private canonical directory' --destination /tmp/bootstrap-parent/../bootstrap-parent/rejected --node-archive /node.tgz
reject 'parent must be owned private canonical directory' --destination /root/rejected --node-archive /node.tgz
chmod 0755 /tmp/bootstrap-parent
reject 'parent must be owned private canonical directory' --destination /tmp/bootstrap-parent/rejected --node-archive /node.tgz
chmod 0700 /tmp/bootstrap-parent
ln -s /tmp/bootstrap-parent /tmp/bootstrap-link
reject 'parent must be owned private canonical directory' --destination /tmp/bootstrap-link/rejected --node-archive /node.tgz
mkdir /tmp/bootstrap-parent/existing
printf 'sentinel\n' > /tmp/bootstrap-parent/existing/keep
reject 'destination already exists' --destination /tmp/bootstrap-parent/existing --node-archive /node.tgz
test "$(cat /tmp/bootstrap-parent/existing/keep)" = sentinel
ln -s /tmp/bootstrap-fixture/.pi /tmp/bootstrap-parent/link
reject 'destination already exists' --destination /tmp/bootstrap-parent/link --node-archive /node.tgz
ln -s /node.tgz /tmp/archive-link
reject 'archive must be regular and physical' --destination /tmp/bootstrap-parent/rejected --node-archive /tmp/archive-link
reject 'archive must be regular and physical' --destination /tmp/bootstrap-parent/rejected --node-archive /tmp/bootstrap-fixture
mkfifo /tmp/archive-fifo
reject 'archive must be regular and physical' --destination /tmp/bootstrap-parent/rejected --node-archive /tmp/archive-fifo
printf 'bad\n' > /tmp/archive-small
reject 'archive size differs' --destination /tmp/bootstrap-parent/rejected --node-archive /tmp/archive-small
cp /node.tgz /tmp/archive-bad
printf X | dd of=/tmp/archive-bad bs=1 seek=0 conv=notrunc status=none
reject 'archive hash differs' --destination /tmp/bootstrap-parent/rejected --node-archive /tmp/archive-bad
rm /tmp/archive-bad
# Real stock cp fails after the stage is owned; no mock runtime or fault callback.
status=0
output=$(/bin/bash -c 'ulimit -f 1024 && exec env -i PATH=/usr/bin:/bin HOME=/tmp/bootstrap-fixture /bin/sh /usr/local/bin/bootstrap-gentle-shell-private-node.sh --destination /tmp/bootstrap-parent/rejected --node-archive /node.tgz' 2>&1) || status=$?
test "$status" = 1
case "$output" in *'STOP: staged archive copy failed'*) ;; *) exit 1;; esac
test "$(printf '%s\n' "$output" | wc -c)" -le 4096
unchanged
printf 'PASS: real post-staging copy failure cleanup=true fixture-Pi-unchanged=true\n'
# Bash supplies the per-UID limit; the POSIX child does not pretend to set it.
exec /bin/bash -c 'ulimit -t 240 && ulimit -u 64 && exec /bin/sh /usr/local/bin/shell-linux-private-runtime-guest.sh'
