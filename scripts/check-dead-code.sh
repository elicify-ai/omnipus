#!/usr/bin/env bash
# Reports unreachable Go functions using the Go team's own dead-code analyser.
#
# WHY THIS REPLACED A BESPOKE SCANNER (2026-08-22)
# ------------------------------------------------
# This repo previously carried a hand-written scanner that counted name
# references. It reported 614 findings. Measured against three known cases it
# got two wrong:
#
#   SecureString.UnmarshalYAML  reported dead — it implements yaml.Unmarshaler
#                               and is called by the YAML library via
#                               reflection. Deleting it would not have broken
#                               the build; it would have silently changed how
#                               config decodes secrets.
#   LoopScheduler.RunDueJobs    reported dead — it has two production callers.
#   ProcessSession.*PtyKeyMode  correctly reported.
#
# x/tools/cmd/deadcode reports 82 on the same tree and gets the first two
# right, because it builds a real whole-program call graph (Rapid Type
# Analysis) from main/test entry points instead of matching identifiers.
# A reference-counting scanner cannot see interface dispatch or reflection,
# so it cannot be made correct by patching — it has to be the call graph.
#
# LIMITS — read before treating output as a delete-list:
#   * Only main packages are entry points. Exported symbols on exported types
#     are reachable in principle from outside the module, so genuinely-unused
#     public API (e.g. ProcessSession.GetPtyKeyMode) is NOT reported. This
#     tool answers "unreachable", not "unused".
#   * -test changes the answer by ~9x on this repo (82 with, 715 without),
#     because without it every test-only helper looks dead. We always pass it.
#   * Per the tool's own docs, a public function reported dead WITH -test
#     indicates a possible test-coverage gap, not necessarily deletable code.
#   * node_modules ships Go sample code (the flatted package) — filtered out.
#
# Usage: scripts/check-dead-code.sh [--count]
set -euo pipefail

TAGS="${GO_BUILD_TAGS:-goolm,stdjson}"

if ! command -v deadcode >/dev/null 2>&1; then
  if [ -x "$(go env GOPATH)/bin/deadcode" ]; then
    export PATH="$PATH:$(go env GOPATH)/bin"
  else
    echo "deadcode not installed. Install with:" >&2
    echo "  go install golang.org/x/tools/cmd/deadcode@latest" >&2
    exit 2
  fi
fi

OUT="$(deadcode -tags="$TAGS" -test ./... 2>&1 | grep -v '^node_modules/' || true)"
COUNT="$(printf '%s' "$OUT" | grep -c 'unreachable func:' || true)"

if [ "${1:-}" = "--count" ]; then
  echo "$COUNT"
  exit 0
fi

if [ -n "$OUT" ]; then
  echo "$OUT"
  echo
  echo "$COUNT unreachable functions (excluding node_modules)."
  echo "Not automatically a delete-list — see this script's header for what"
  echo "'unreachable' does and does not mean."
fi
