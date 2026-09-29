#!/bin/sh
# Root is confined to this guest; stock proof runs before any candidate binary.
set -eu
fail() { printf 'STOP: %s\n' "$1" >&2; exit 1; }
for tool in setpriv env awk id stat timeout; do
    command -v "$tool" >/dev/null 2>&1 || fail 'required stock tool absent'
done
env | awk -F= '$1 !~ /^(PATH|HOME|HOSTNAME|PWD|HOST_NETNS|HOST_MNTNS)$/ { bad=1 }
    END { exit bad }' || fail 'unexpected root environment name'
test "$(id -u):$(id -g)" = 0:0 || fail 'root guest required'
test "${HOME:-}" = /tmp && test "${PWD:-/}" = / || fail 'unexpected root environment'
# Only SETGID (6) and SETUID (7), including the bounding set; no ambient caps.
awk '/^Cap(Eff|Prm|Bnd):/ { seen++; if ($2 != "00000000000000c0") bad=1 }
     /^Cap(Inh|Amb):/ { if ($2 != "0000000000000000") bad=1 }
     /^NoNewPrivs:/ { locked=($2 == 1) }
     END { exit (seen != 3 || bad || !locked) }' /proc/self/status || fail 'root privilege guard absent'
# Root retains only bounded SETUID/SETGID; dropping UID clears Eff/Prm.
# Inh/Amb stay zero and no-new-privs prevents executable privilege acquisition.
# env -i strips candidate opt-in; stock proof independently requires client Eff=0.
setpriv --reuid=65532 --regid=65532 --clear-groups \
    --inh-caps=-all --ambient-caps=-all --no-new-privs \
    env -i PATH=/usr/bin:/bin HOME=/tmp HOST_NETNS="$HOST_NETNS" HOST_MNTNS="$HOST_MNTNS" \
    /bin/sh -c '
        awk '\''/^Cap(Eff|Prm|Inh|Amb):/ { seen++; if ($2 != "0000000000000000") bad=1 }
             /^NoNewPrivs:/ { locked=($2 == 1) }
             END { exit (seen != 4 || bad || !locked) }'\'' /proc/self/status || exit 1
        exec /bin/sh /usr/local/bin/shell-linux-guest-proof.sh
    '
# The stock proof has now checked current namespaces, mounts and network denial.
# Candidate fixtures are created only by the root test, inside the proven tmpfs.
exec env -i PATH=/usr/bin:/bin HOME=/tmp TMPDIR=/tmp GENTLE_BROKER_GUEST=1 \
    timeout --kill-after=2 25 /usr/local/bin/shellinstaller.test \
    -test.v -test.run='^TestLinuxBroker' -test.timeout=20s
