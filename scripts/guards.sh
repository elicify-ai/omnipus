#!/usr/bin/env bash
# scripts/guards.sh
#
# Single discovery-based runner for every regression guard under scripts/.
# Wave G1 of the ADR-084/085/086 joint delivery
# (docs/internal/specs/adr-084-086-joint-delivery-plan.md, C-19, C-44, C-45,
# C-60, C-92, C-93). Before this file existed, adding a guard meant editing
# three CI wiring files (Makefile, .github/workflows/pr.yml,
# deploy/ci-worker/runci.sh) by hand — an append-only edit to all three that a
# bad merge resolution could silently drop. After this file, adding a guard
# is adding a file: this script discovers it, runs its proof-of-failure
# companion first, runs it, and reports it. No wiring file changes again.
#
# ─── What "discovery" means ──────────────────────────────────────────────────
# Every file matching scripts/check-*.sh is a CANDIDATE. Candidates named in
# scripts/guards.quarantine are dropped first (discovered, deliberately never
# run — distinct from the no-self-test exemption below). What remains is the
# ACTIVE set.
#
# ─── Companions are subtracted BEFORE guards are counted (C-93 rule 1) ──────
# A candidate that is itself another active candidate's proof-of-failure
# companion is not a guard in its own right. A candidate is a companion of
# <name>.sh when it is named <name>.test.sh or <name>-selfcheck.sh AND
# <name>.sh is itself in the active set. Companions are removed from the
# active set first; what remains is the GUARD list. This is why
# check-no-handwritten-wire-types.test.sh and
# check-no-removed-providers-selfcheck.sh do not inflate the guard count.
#
# ─── Companion resolution, per guard (C-44, C-93 rule 2) ────────────────────
# For each guard <name>.sh, ALL THREE of the following are checked — not a
# first-match fallback — and every one that resolves is run, in this order,
# before the guard itself:
#   1. <name>.test.sh, if that file is in the active set.
#   2. <name>.sh --self-test, if the guard's own source contains the string
#      "--self-test" (i.e. the guard implements the flag itself).
#   3. <name>-selfcheck.sh, if that file is in the active set.
# When more than one resolves (check-no-handwritten-wire-types.sh has both a
# .test.sh file and a --self-test flag today), BOTH run — this file does not
# pick a winner. A guard with none of the three MUST be listed in
# scripts/guards-no-selftest.exempt, or the run fails. No guard added by the
# ADR-084/085/086 delivery may be added to that exemption file — each of the
# nine it introduces ships with a real .test.sh companion.
#
# ─── Pass/fail (C-19, C-45) ──────────────────────────────────────────────────
# Every discovered guard runs regardless of an earlier guard's outcome — this
# script does not stop at the first failure. It fails overall if: any guard
# or any of its companions exited non-zero; zero guards were discovered; or a
# non-exempt guard had no companion. Exactly one "<guard> exit=<code>" line is
# printed per guard (never per companion — companion lines are prefixed
# ">>").
#
# ─── Consumers ────────────────────────────────────────────────────────────
#   Makefile                     — `make lint-guards` / `make lint`
#   .github/workflows/pr.yml     — the tool-error-status-lint job's one step
#   deploy/ci-worker/runci.sh    — run_lint()
# All three now hold exactly one guard invocation, forever (C-92). None of
# them may grow a new guard step — a new guard is a new scripts/check-*.sh
# file plus its companion, nothing else.
#
# ─── Testing this file ───────────────────────────────────────────────────────
# scripts/guards.test.sh is the mutation proof that THIS RUNNER can go red —
# it builds synthetic scripts/ directories under REPO_ROOT overrides and
# never touches the real scripts/ directory. Run it before trusting any
# verdict from this script (L2 precedes L3 in the delivery plan's exit
# proof).
#
# ─── Overrides (for guards.test.sh only — never set these in CI) ────────────
#   REPO_ROOT   — repo root to discover scripts/ under. Defaults to this
#                 script's own parent directory's parent.

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" && pwd)"
REPO_ROOT="${REPO_ROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"
GUARDS_DIR="$REPO_ROOT/scripts"

QUARANTINE_FILE="$GUARDS_DIR/guards.quarantine"
EXEMPT_FILE="$GUARDS_DIR/guards-no-selftest.exempt"

WORK="$(mktemp -d /tmp/guards-run.XXXXXX)"
trap 'rm -rf "$WORK"' EXIT

# Strip a list file down to bare names for matching: drop blank lines, drop
# full-comment lines, drop an inline "# reason" trailer, trim whitespace.
strip_list() {
  local src="$1" dst="$2"
  : > "$dst"
  [ -f "$src" ] || return 0
  while IFS= read -r line || [ -n "$line" ]; do
    line="${line%%#*}"
    line="$(printf '%s' "$line" | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//')"
    [ -n "$line" ] && printf '%s\n' "$line" >> "$dst"
  done < "$src"
}

in_file() { grep -qxF -- "$1" "$2" 2>/dev/null; }

strip_list "$QUARANTINE_FILE" "$WORK/quarantine.txt"
strip_list "$EXEMPT_FILE" "$WORK/exempt.txt"

: > "$WORK/candidates.txt"
if [ -d "$GUARDS_DIR" ]; then
  ( cd "$GUARDS_DIR" && ls check-*.sh 2>/dev/null | sort ) > "$WORK/candidates.txt"
fi

# ACTIVE = candidates - quarantined.
: > "$WORK/active.txt"
while IFS= read -r c; do
  [ -z "$c" ] && continue
  in_file "$c" "$WORK/quarantine.txt" && continue
  printf '%s\n' "$c" >> "$WORK/active.txt"
done < "$WORK/candidates.txt"

# A candidate is a companion of an active guard when it is named
# <base>.test.sh or <base>-selfcheck.sh and <base>.sh is itself active.
is_companion_of_active() {
  local c="$1" base=""
  case "$c" in
    *.test.sh)      base="${c%.test.sh}.sh" ;;
    *-selfcheck.sh) base="${c%-selfcheck.sh}.sh" ;;
    *) return 1 ;;
  esac
  in_file "$base" "$WORK/active.txt"
}

# GUARDS = active - companions (C-93 rule 1: subtract before counting).
: > "$WORK/guards.txt"
while IFS= read -r c; do
  [ -z "$c" ] && continue
  is_companion_of_active "$c" && continue
  printf '%s\n' "$c" >> "$WORK/guards.txt"
done < "$WORK/active.txt"

GUARD_TOTAL="$(grep -c . "$WORK/guards.txt" 2>/dev/null)"
GUARD_TOTAL="${GUARD_TOTAL:-0}"

echo "=== scripts/guards.sh ==="
echo "repo root: $REPO_ROOT"
CANDIDATE_TOTAL="$(grep -c . "$WORK/candidates.txt" 2>/dev/null)"
echo "candidates discovered under $GUARDS_DIR: ${CANDIDATE_TOTAL:-0}"

# Print quarantine lines from the RAW file (preserves the one-line reason,
# C-60) — matching for exclusion still uses the stripped bare-name set above.
if [ -f "$QUARANTINE_FILE" ]; then
  while IFS= read -r raw || [ -n "$raw" ]; do
    case "$raw" in
      ''|'#'*) continue ;;
    esac
    echo "QUARANTINED: $raw"
  done < "$QUARANTINE_FILE"
fi

echo "guards resolved (companions subtracted): $GUARD_TOTAL"
echo ""

if [ "$GUARD_TOTAL" -eq 0 ]; then
  echo "GUARD RUNNER FAILURE: zero guards discovered under $GUARDS_DIR" >&2
  exit 1
fi

: > "$WORK/failed.txt"
RUNNER_FAIL=0

while IFS= read -r g; do
  [ -z "$g" ] && continue
  guard_failed=0
  companions_run=0

  test_file="${g%.sh}.test.sh"
  if in_file "$test_file" "$WORK/active.txt"; then
    echo ">> companion for $g: $test_file"
    bash "$GUARDS_DIR/$test_file"
    tc=$?
    echo ">> companion($test_file) exit=$tc"
    [ "$tc" -ne 0 ] && guard_failed=1
    companions_run=1
  fi

  if grep -q -- '--self-test' "$GUARDS_DIR/$g" 2>/dev/null; then
    echo ">> companion for $g: $g --self-test"
    bash "$GUARDS_DIR/$g" --self-test
    fc=$?
    echo ">> companion($g --self-test) exit=$fc"
    [ "$fc" -ne 0 ] && guard_failed=1
    companions_run=1
  fi

  selfcheck_file="${g%.sh}-selfcheck.sh"
  if in_file "$selfcheck_file" "$WORK/active.txt"; then
    echo ">> companion for $g: $selfcheck_file"
    bash "$GUARDS_DIR/$selfcheck_file"
    sc=$?
    echo ">> companion($selfcheck_file) exit=$sc"
    [ "$sc" -ne 0 ] && guard_failed=1
    companions_run=1
  fi

  if [ "$companions_run" -eq 0 ]; then
    if in_file "$g" "$WORK/exempt.txt"; then
      echo ">> $g: no proof-of-failure companion (exempt — see $EXEMPT_FILE)"
    else
      echo "GUARD RUNNER FAILURE: $g has no proof-of-failure companion and is not listed in $EXEMPT_FILE" >&2
      guard_failed=1
    fi
  fi

  bash "$GUARDS_DIR/$g"
  gc=$?
  echo "$g exit=$gc"
  [ "$gc" -ne 0 ] && guard_failed=1

  if [ "$guard_failed" -ne 0 ]; then
    RUNNER_FAIL=1
    printf '%s\n' "$g" >> "$WORK/failed.txt"
  fi
  echo ""
done < "$WORK/guards.txt"

echo "=== summary ==="
echo "guards run: $GUARD_TOTAL"
if [ -s "$WORK/exempt.txt" ]; then
  echo "no-self-test exemptions:"
  sed 's/^/  /' "$WORK/exempt.txt"
fi

if [ -s "$WORK/failed.txt" ]; then
  echo "FAILED:" >&2
  sed 's/^/  /' "$WORK/failed.txt" >&2
  RUNNER_FAIL=1
fi

if [ "$RUNNER_FAIL" -ne 0 ]; then
  echo "GUARD RUNNER: FAILED"
  exit 1
fi

echo "GUARD RUNNER: all $GUARD_TOTAL guards passed"
exit 0
