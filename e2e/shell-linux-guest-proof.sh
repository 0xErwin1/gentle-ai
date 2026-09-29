#!/bin/sh
# Candidate-free stock OS boundary proof. Never print environment values.
set -eu
fail() { printf 'STOP: %s\n' "$1" >&2; exit 1; }
for tool in stat id awk env bash timeout find; do
    command -v "$tool" >/dev/null 2>&1 || fail "required stock tool absent"
done
for file in /proc/self/mountinfo /proc/net/dev /proc/net/route /proc/net/ipv6_route; do
    test -r "$file" || fail "required proc observation absent"
done

# An allowlist is stricter than guessing every possible credential variable name.
# Only names are examined; neither diagnostics nor logs disclose their values.
env | awk -F= '
    $1 !~ /^(PATH|HOME|HOSTNAME|PWD|HOST_NETNS|HOST_MNTNS)$/ { bad=1 }
    END { exit bad }
' || fail "unexpected environment name (credentials forbidden)"
for inode in "${HOST_NETNS:-}" "${HOST_MNTNS:-}"; do
    case "$inode" in ''|*[!0-9]*) fail "host namespace inode absent or invalid" ;; esac
done
test "$(stat -Lc %i /proc/self/ns/net)" != "$HOST_NETNS" || fail "shared host network namespace"
test "$(stat -Lc %i /proc/self/ns/mnt)" != "$HOST_MNTNS" || fail "shared host mount namespace"
test "$(id -u)" = 65532 || fail "guest UID is not the designated unprivileged UID"
test "$(id -g)" = 65532 || fail "guest GID is not the designated unprivileged GID"
awk '/^CapEff:/ { found=1; if ($2 != "0000000000000000") bad=1 }
     /^NoNewPrivs:/ { locked=($2 == 1) }
     END { exit (!found || bad || !locked) }' /proc/self/status || fail "privilege guard absent"
test "${HOME:-}" = /tmp || fail "HOME is not the private tmpfs"
test "${PWD:-/}" = / || fail "unexpected working directory"

# Reject all unexpected mountpoints, including host HOME, Pi and docker.sock.
# Docker's three generated /etc files are system plumbing, not candidate mounts.
awk '
    function option(list, value) { return index("," list ",", "," value ",") > 0 }
    {
        mount=$5; type=""; source=""
        for (i=7; i<=NF; i++) if ($i == "-") { type=$(i+1); source=$(i+2); break }
        if (mount == "/") { root=1; if (!option($6,"ro")) bad=1; next }
        if (mount == "/tmp") {
            home=1
            if ($4 != "/" || type != "tmpfs" || source != "tmpfs" ||
                !option($6,"rw") || !option($6,"nosuid") || !option($6,"nodev")) bad=1
            next
        }
        if (mount ~ /^\/proc(\/|$)/ && (type == "proc" || type == "tmpfs")) next
        if (mount == "/sys" && type == "sysfs" && option($6,"ro")) next
        if (mount == "/sys/fs/cgroup" && type == "cgroup2" && option($6,"ro")) next
        if ((mount == "/sys/firmware" || mount == "/sys/devices/virtual/powercap") &&
            type == "tmpfs" && option($6,"ro")) next
        if (mount == "/dev" && type == "tmpfs") next
        if (mount == "/dev/pts" && type == "devpts") next
        if (mount == "/dev/shm" && type == "tmpfs") next
        if ((mount == "/dev/mqueue") && type == "mqueue") next
        if (mount ~ /^\/etc\/(hosts|hostname|resolv.conf)$/) next
        bad=1
    }
    END { exit (!root || !home || bad) }
' /proc/self/mountinfo || fail "unexpected mount or non-private HOME"
test "$(stat -c %a /tmp)" = 1777 || fail "private HOME mode differs"
umask 077
printf 'private guest home\n' > /tmp/guest-home-proof || fail "private HOME is not writable"
for path in /root /home /run/docker.sock /var/run/docker.sock; do
    case "$path" in
        /home) test -d /home || fail "stock home directory absent"
               home_child=$(find /home -mindepth 1 -maxdepth 1 -print -quit) || fail "home content observation failed"
               test -z "$home_child" || fail "host home content present" ;;
        /root) test ! -r /root || fail "root home accessible" ;;
        *) test ! -e "$path" || fail "Docker socket present" ;;
    esac
done

awk 'NR > 2 { split($0,a,":"); gsub(/[[:space:]]/,"",a[1]);
              if (a[1] != "lo") bad=1; seen=1 }
     END { exit (!seen || bad) }' /proc/net/dev || fail "non-loopback interface present"
awk 'NR > 1 { if ($1 != "lo") bad=1 } END { exit bad }' /proc/net/route || fail "external IPv4 route present"
awk 'NF { if ($NF != "lo") bad=1 } END { exit bad }' /proc/net/ipv6_route || fail "external IPv6 route present"

# Numeric public IP bypasses DNS; timeout is a guard, not evidence of denial.
# Reject timeout, unsupported /dev/tcp and every error except explicit no-route.
timeout 2 bash -c 'exit 0' || fail "bash/timeout unavailable"
set +e
output=$(LC_ALL=C timeout --kill-after=1 3 bash -c 'exec 3<>/dev/tcp/1.1.1.1/443' 2>&1)
status=$?
set -e
test "$status" = 1 || fail "raw TCP succeeded or denial was inconclusive"
case "$output" in
    *'Network is unreachable'*) ;;
    *) fail "raw TCP did not prove explicit network denial" ;;
esac
printf 'PASS: stock guest isolation only; no candidate execution or Ready claim.\n'
