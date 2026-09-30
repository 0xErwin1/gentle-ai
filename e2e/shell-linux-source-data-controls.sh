#!/bin/sh
# Proof and all synthetic controls must pass before approved archive inspection.
set -eu
/bin/sh /usr/local/bin/shell-linux-guest-proof.sh
umask 077
inspector=/usr/local/bin/shell-linux-source-data-guest.sh
fail() { printf 'STOP: %s\n' "$1" >&2; exit 1; }
reject() {
    expected="$1"
    shift
    status=0
    timeout --kill-after=1 25 /bin/bash "$inspector" "$@" \
        > /tmp/control-output 2>&1 || status=$?
    test "$status" = 1 || fail 'synthetic rejection status differed'
    # Never relay attacker-controlled output, even when a control fails.
    test "$(wc -l < /tmp/control-output)" = 1 || fail 'synthetic rejection emitted unexpected data'
    if test "$expected" = unsafe-name; then
        # GNU tar may diagnose traversal before the inventory validator does.
        # A generic parser/quota failure is NOT evidence of unsafe-name rejection.
        grep -Exq 'STOP: (tar diagnostics present|unsafe, missing, duplicate, nonregular or oversized member)' \
            /tmp/control-output || fail 'unsafe-name rejection reason differed'
    else
        grep -Fxq -- "$expected" /tmp/control-output || fail 'synthetic rejection reason differed'
    fi
    if grep -Eq '(^DATA|^::|^##\[)' /tmp/control-output; then
        fail 'synthetic rejection leaked data or a raw CI annotation'
    fi
}
# Invalid gzip bytes must fail at SRI, not reach any archive parser.
printf 'not a gzip or tar archive\n' > /tmp/control.tgz
reject 'STOP: approved SRI mismatch before tar' --sri-control /tmp/control.tgz
test ! -e /tmp/data.tar && test ! -e /tmp/data-list || fail 'SRI failure reached archive parsing'
mkdir -p /tmp/control/package/bin /tmp/control/package/scripts
printf '{}\n' > /tmp/control/package/package.json
printf 'synthetic launcher data\n' > /tmp/control/package/bin/gentle-shell.mjs
printf 'synthetic lifecycle data\n' > /tmp/control/package/scripts/install-gentle-ai.mjs
make_tar() {
    tar -cf /tmp/control.tar -C /tmp/control \
        package/package.json package/bin/gentle-shell.mjs package/scripts/install-gentle-ai.mjs
}
make_tar
timeout --kill-after=1 25 /bin/bash "$inspector" --synthetic /tmp/control.tar \
    > /tmp/control-output 2>&1 || fail 'synthetic positive inspection failed'
reason='STOP: unsafe, missing, duplicate, nonregular or oversized member'
# Every hostile name is appended to an otherwise valid base archive as DATA.
# Transform changes only the stored label; no traversal path is created.
printf 'synthetic fixture data\n' > /tmp/control/package/fixture
make_tar
tar -rf /tmp/control.tar -C /tmp/control --transform='s|package/fixture|package/../fixture|' \
    package/fixture 2>/tmp/control-create-errors || fail 'traversal fixture creation failed'
reject unsafe-name --synthetic /tmp/control.tar
for name in 'unsafe:name' "$(printf 'control\001name')" "$(printf 'line\n::error::synthetic')" '::error::synthetic'; do
    printf 'synthetic fixture data\n' > "/tmp/control/package/$name"
    make_tar
    tar -rf /tmp/control.tar -C /tmp/control -- "package/$name" \
        2>/tmp/control-create-errors || fail 'unsafe-name fixture creation failed'
    reject unsafe-name --synthetic /tmp/control.tar
done
# Duplicate exact selected member, then a symlink (never dereferenced).
make_tar
tar -rf /tmp/control.tar -C /tmp/control package/package.json
reject "$reason" --synthetic /tmp/control.tar
make_tar
ln -s package.json /tmp/control/package/unsafe-link
tar -rf /tmp/control.tar -C /tmp/control package/unsafe-link
reject "$reason" --synthetic /tmp/control.tar
# Size failure is explicit; no truncated selected bytes may pass.
truncate -s 262145 /tmp/control/package/package.json
make_tar
reject "$reason" --synthetic /tmp/control.tar
printf '{}\n' > /tmp/control/package/package.json
truncate -s 8388609 /tmp/control/package/unknown-data
make_tar
tar -rf /tmp/control.tar -C /tmp/control package/unknown-data
reject "$reason" --synthetic /tmp/control.tar
# Required lifecycle data cannot silently disappear.
tar -cf /tmp/control.tar -C /tmp/control package/package.json package/bin/gentle-shell.mjs
reject "$reason" --synthetic /tmp/control.tar
printf 'PASS: synthetic SRI-order, unsafe-name, duplicate, nonregular, oversize and missing controls only.\n'
timeout --kill-after=1 90 /bin/bash "$inspector"
