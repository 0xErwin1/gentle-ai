#!/usr/bin/env bash
set -euo pipefail

# Proves the credential-absent path of scripts/notify-downstream-release.sh
# (design D8, spec "Credential-Gated, Inert-by-Default Activation"): with no
# dispatch credential configured, the script must print a skip line, exit 0,
# and attempt no dispatch at all -- not fail, not silently no-op without
# saying so.

repo_root="$(git rev-parse --show-toplevel)"
tmp_root="$(mktemp -d)"
trap 'rm -rf "$tmp_root"' EXIT

# A fake curl on PATH that would prove a dispatch was attempted, and fails
# loudly if actually invoked -- the credential-absent path must never reach
# it.
fake_bin="$tmp_root/bin"
mkdir -p "$fake_bin"
dispatch_marker="$tmp_root/curl-invoked"
cat >"$fake_bin/curl" <<SCRIPT
#!/usr/bin/env bash
touch "$dispatch_marker"
exit 1
SCRIPT
chmod +x "$fake_bin/curl"

output="$tmp_root/output"
status=0
PATH="$fake_bin:$PATH" \
RELEASE_TAG="v0.0.0" \
DOWNSTREAM_DISPATCH_TOKEN="" \
  "$repo_root/scripts/notify-downstream-release.sh" >"$output" 2>&1 || status=$?

if [[ "$status" -ne 0 ]]; then
  printf 'expected exit 0 on the credential-absent path, got %s\n' "$status" >&2
  cat "$output" >&2
  exit 1
fi
if ! grep -q "skipped" "$output"; then
  printf 'expected a skip line in output\n' >&2
  cat "$output" >&2
  exit 1
fi
if [[ -e "$dispatch_marker" ]]; then
  printf 'credential-absent path attempted a dispatch (curl was invoked)\n' >&2
  exit 1
fi

printf 'notify-downstream-release credential-absent path: PASS\n'
