#!/bin/bash
# Only stock tools inspect bytes. Neither package code nor lifecycle hooks run.
set -euo pipefail
export LC_ALL=C TZ=UTC
umask 077
fail() { printf 'STOP: %s\n' "$1" >&2; exit 1; }
# Limits: compressed 9417802 bytes, decompressed 64 MiB, 8192 members,
# each regular member 8 MiB, selected member 256 KiB, selected total 1792 KiB.
# RAW tar listing is capped at 2 MiB; encoded inventory is NOT that listing.
# Total public log bound: 8 MiB, derived without truncating any accepted output:
# inventory <= 8192*(512 encoded-name + 64 framing) = 4718592 bytes;
# selected output <= 7*(349528 base64 bytes + 4600 lines*14 framing)
# = 2897496 bytes; fixed headers, controls/proof and final messages < 8192.
# Combined bound = 7624280 bytes < 8 MiB; accepted output is never truncated.
# Names <= 384 bytes and decimal size labels <= 7 digits enforce this accounting.
# Synthetic output stays private. Any quota, parser or extraction error fails.
validate() {
    local archive="$1" listing=/tmp/data-list selected=/tmp/data-selected
    # Reject oversized listing instead of accepting a truncated inventory.
    (ulimit -f 2048; timeout --kill-after=1 15 tar --numeric-owner \
        --quoting-style=escape -tvf "$archive" > "$listing" 2>/tmp/tar-errors) \
        || fail 'tar listing failed or exceeded limit'
    test ! -s /tmp/tar-errors || fail 'tar diagnostics present'
    awk '
        NF != 6 { bad=1; next }
        {
            type=substr($1,1,1); size=$3; name=$6
            if (++count > 8192 || length(name) > 384 ||
                name !~ /^package\/[A-Za-z0-9_.+@\/-]+$/ ||
                name ~ /(^|\/)\.\.?($|\/)/ || name ~ /\/\// || seen[name]++ ||
                size !~ /^[0-9]+$/ || length(size) > 7 || size > 8388608 ||
                (type != "-" && type != "d")) bad=1
            if (name == "package/package.json" ||
                name == "package/bin/gentle-shell.mjs" ||
                name == "package/scripts/install-gentle-ai.mjs" ||
                name == "package/runtime/gentle-shell-launcher.mjs" ||
                name == "package/runtime/gentle-ai-binary.mjs" ||
                name == "package/scripts/gentle-ai-installer.mjs" ||
                name == "package/scripts/install-tui-mode-setting.mjs") {
                if (type != "-" || size > 262144) bad=1
                total+=size; found++
                print name, size
            }
        }
        END { if (bad || found != 7 || total > 1835008) exit 1 }
    ' "$listing" > "$selected" || fail 'unsafe, missing, duplicate, nonregular or oversized member'
    # Prefix every line and encode names/content: no raw archive text enters CI logs.
    while read -r mode owner size date clock name; do
        printf 'DATA inventory type=%s bytes=%s name-base64=' "${mode:0:1}" "$size"
        printf '%s' "$name" | base64 -w0
        printf '\n'
    done < "$listing"
    while read -r name expected_size; do
        # --to-stdout never creates archive paths; only previously validated names.
        # Bash ulimit -f uses 1024-byte blocks: independently cap both private
        # stdout and stderr writes at 256 KiB DURING extraction, not afterwards.
        (ulimit -f 256; timeout --kill-after=1 10 tar -xOf "$archive" -- "$name" \
            > /tmp/data-member 2>/tmp/tar-errors) || fail 'selected stdout extraction failed'
        test ! -s /tmp/tar-errors || fail 'selected extraction diagnostics present'
        test "$(wc -c < /tmp/data-member)" -eq "$expected_size" || fail 'member size mismatch'
        printf 'DATA member=%s bytes=%s sha256=%s encoding=base64\n' \
            "$name" "$expected_size" "$(sha256sum /tmp/data-member | cut -d ' ' -f1)"
        # Fixed prefix prevents even encoded output from becoming a CI command.
        base64 -w76 /tmp/data-member | while IFS= read -r line; do
            printf 'DATA content %s\n' "$line"
        done
    done < "$selected"
}
archive=/data/approved.tgz
case "${1:-}" in
    --synthetic)
        # Synthetic controls never attest approved bytes or permit package execution.
        test "$#" -eq 2 && test "$2" = /tmp/control.tar || fail 'invalid synthetic input'
        validate "$2"
        exit 0 ;;
    --sri-control)
        test "$#" -eq 2 && test "$2" = /tmp/control.tgz || fail 'invalid SRI control'
        archive="$2" ;;
    '') test "$#" -eq 0 || fail 'unexpected arguments' ;;
    *) fail 'unexpected mode' ;;
esac
sri='sha512-SXBp9jIRnVIcOsLCW/Zw4XDTXxhViXNxrCZS7LyioB6kdRl99Ohojeaj+yIH5+K/ucwLlTlHYBz8zwue9aspNQ=='
# Hash check precedes EVERY tar/gzip operation on approved archive bytes.
expected_hex=$(printf '%s' "${sri#sha512-}" | base64 -d | od -An -v -tx1 | tr -d ' \n')
actual_hex=$(sha512sum "$archive" | cut -d ' ' -f1)
test "$actual_hex" = "$expected_hex" || fail 'approved SRI mismatch before tar'
test "$(wc -c < "$archive")" -eq 9417802 || fail 'approved compressed size mismatch'
test "$(sha256sum "$archive" | cut -d ' ' -f1)" = \
    e738bdef907dbdd86507a3a170222a97d85a502e49d7a5685e6b19801b37aeff \
    || fail 'approved SHA256 mismatch'
printf 'DATA source=https://registry.npmjs.org/gentle-pi/-/gentle-pi-3.7.0.tgz\n'
printf 'DATA bytes=9417802 sha256=e738bdef907dbdd86507a3a170222a97d85a502e49d7a5685e6b19801b37aeff sri=%s\n' "$sri"
printf 'DATA commands: sha512sum /data/approved.tgz; sha256sum /data/approved.tgz\n'
printf 'DATA commands: timeout --kill-after=1 15 gzip -dc /data/approved.tgz\n'
printf 'DATA commands: timeout --kill-after=1 15 tar --numeric-owner --quoting-style=escape -tvf /tmp/data.tar\n'
printf 'DATA commands: (ulimit -f 256; timeout --kill-after=1 10 tar -xOf /tmp/data.tar -- SELECTED_MEMBER)\n'
printf 'DATA limits: decompressed=67108864 raw-listing=2097152 total-log<=8388608 count=8192 regular=8388608 selected=262144 total-selected=1835008\n'
(ulimit -f 65536; timeout --kill-after=1 15 gzip -dc "$archive" > /tmp/data.tar 2>/tmp/gzip-errors) \
    || fail 'decompression failed or exceeded 64 MiB'
test ! -s /tmp/gzip-errors || fail 'gzip diagnostics present'
test "$(wc -c < /tmp/data.tar)" -le 67108864 || fail 'decompressed size exceeded'
validate /tmp/data.tar
printf 'PASS: approved archive data inspection only; installation and Ready unproven.\n'
