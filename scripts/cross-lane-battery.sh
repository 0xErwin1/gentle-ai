#!/usr/bin/env bash
# Local cross-lane integration battery. NOT wired into CI on purpose:
# the optional --with-model lane spends real reviewer model runs on the
# development subscription, and every lane drives a real gentle-ai binary
# end to end against live scratch repositories.
#
# Usage:
#   scripts/cross-lane-battery.sh --binary /path/to/gentle-ai [--with-model] [--keep-work]
#
# Lanes:
#   opencode  - drives the REAL OpenCode transport plugin bytes through an
#               emulated Task hook surface with HOST-assembled binding frames
#               (lens frame and validator role frame).
#   claude    - one low-risk full lifecycle to gate allow, plus one medium
#               candidate consent/v3 round-trip; --with-model additionally
#               runs the real compiled claude-code reviewer runtime.
#   schema    - validates every envelope captured above against the published
#               schemas in contracts/review-integration/.
#
# Exit code is non-zero when any check fails. Known-red checks (pending
# fixes referenced in the output) still fail: red is the battery working.
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"
exec go run ./scripts/crosslane "$@"
