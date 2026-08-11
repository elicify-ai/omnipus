#!/usr/bin/env bash
# check-race-package-lockstep.sh
#
# Regression guard for #615: the `-race` package list must be byte-for-byte
# IDENTICAL (as a set) between the two CI surfaces that run it —
#   .github/workflows/pr.yml       ("Run go test -race" step)
#   deploy/ci-worker/runci.sh      (run_gorace function)
# — because a worker gate that measures something GitHub does not (or vice
# versa) is exactly how a green local verdict stops predicting the real one.
# This is not hypothetical: #615 found the two files had silently held
# byte-identical 18-entry lists for pkg/tools/browser's four subpackages
# while the surrounding comments in both files had drifted out of sync with
# what the lists actually excluded — the drift was caught only by manual
# reading, which does not scale. This script makes the invariant mechanical.
#
# What it checks: extracts the `./pkg/...` / `./tests/...` package-path
# tokens from each file's `go test -race …` invocation and fails if the two
# SETS (order-independent — pr.yml and runci.sh have historically ordered
# packages slightly differently across line wraps) differ in any way.
#
# Exit code: 0 if the two lists match exactly, 1 if they diverge, 2 on a
# parse error (e.g. the invocation shape changed and this script's
# extraction patterns no longer find it — treat that as a signal to update
# this script, not to ignore it).
#
# Usage:
#   bash scripts/check-race-package-lockstep.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="${REPO_ROOT:-$(cd "${SCRIPT_DIR}/.." && pwd)}"

PR_YML="${REPO_ROOT}/.github/workflows/pr.yml"
RUNCI_SH="${REPO_ROOT}/deploy/ci-worker/runci.sh"

for f in "$PR_YML" "$RUNCI_SH"; do
  if [[ ! -f "$f" ]]; then
    echo "check-race-package-lockstep: ERROR — file not found: $f" >&2
    exit 2
  fi
done

# extract_packages <file> <start-pattern>
#
# Prints one package-path token per line, starting from the first line that
# matches <start-pattern> (a `go test -race …` invocation) through the line
# that closes it (`2>&1)`), extracted from BOTH files regardless of whether
# the invocation is on one line (pr.yml) or wrapped across several with
# trailing backslashes (runci.sh).
extract_packages() {
  local file="$1" start_pattern="$2"
  # index() does a plain substring match (no regex escaping headaches for the
  # literal `"$TAGS"` / `,` characters in either invocation).
  awk -v start="$start_pattern" '
    index($0, start) > 0 { capturing = 1 }
    capturing { print }
    capturing && /2>&1\)/ { exit }
  ' "$file" | grep -oE '\./(pkg|tests)/[A-Za-z0-9_./]+' | sort -u
}

PR_PKGS="$(extract_packages "$PR_YML" 'go test -race -tags goolm,stdjson -count=1 -timeout')"
RUNCI_PKGS="$(extract_packages "$RUNCI_SH" 'go test -race -tags "$TAGS" -count=1 -timeout')"

if [[ -z "$PR_PKGS" ]]; then
  echo "check-race-package-lockstep: ERROR — found no package tokens in ${PR_YML}'s -race step; the invocation shape may have changed (update this script's extraction pattern)" >&2
  exit 2
fi
if [[ -z "$RUNCI_PKGS" ]]; then
  echo "check-race-package-lockstep: ERROR — found no package tokens in ${RUNCI_SH}'s run_gorace; the invocation shape may have changed (update this script's extraction pattern)" >&2
  exit 2
fi

if [[ "$PR_PKGS" == "$RUNCI_PKGS" ]]; then
  COUNT=$(echo "$PR_PKGS" | grep -c .)
  echo "check-race-package-lockstep: OK — ${COUNT} package(s), identical in both files"
  exit 0
fi

echo "check-race-package-lockstep: FAIL — the -race package lists in"
echo "  ${PR_YML}"
echo "  ${RUNCI_SH}"
echo "have diverged. Both files must list EXACTLY the same packages (see each"
echo "file's own comment on why: a worker gate that measures something GitHub"
echo "does not is how a green local verdict stops predicting the real one)."
echo ""

ONLY_IN_PR="$(comm -23 <(echo "$PR_PKGS") <(echo "$RUNCI_PKGS") || true)"
ONLY_IN_RUNCI="$(comm -13 <(echo "$PR_PKGS") <(echo "$RUNCI_PKGS") || true)"

if [[ -n "$ONLY_IN_PR" ]]; then
  echo "Only in ${PR_YML}:"
  echo "$ONLY_IN_PR" | sed 's/^/  /'
  echo ""
fi
if [[ -n "$ONLY_IN_RUNCI" ]]; then
  echo "Only in ${RUNCI_SH}:"
  echo "$ONLY_IN_RUNCI" | sed 's/^/  /'
  echo ""
fi

echo "Fix: update whichever file is missing entries so both lists match exactly,"
echo "then (if runci.sh changed) redeploy it per deploy/ci-worker/CLAUDE.md's"
echo "\"Redeploying runci.sh\" section."
exit 1
