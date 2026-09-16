#!/usr/bin/env bash
# check-agents-md-sync.test.sh
#
# Proof-of-failure companion for check-agents-md-sync.sh (and for the
# lane's fixer, scripts/sync-agents-md.sh — this is the lane's only test
# file, so both halves of the mechanism are proven here).
#
# Everything runs against synthetic trees under mktemp dirs via the
# REPO_ROOT override; the real repository is never scanned or mutated.
# That property is itself asserted: the real repo's `git status
# --porcelain` snapshot before and after the run must be byte-identical.
#
# GUARD cases (check-agents-md-sync.sh must go RED, not just "run"):
#   G1  zero CLAUDE.md/AGENTS.md anywhere          -> exit 0 (vacuous)
#   G2  matched pairs                              -> exit 0
#   G3  missing twin (CLAUDE.md, no AGENTS.md)     -> exit 1, dir named
#   G4  extra twin (AGENTS.md, no CLAUDE.md)       -> exit 1, dir named
#   G5  differing content                          -> exit 1, dir named
#   G6  all three shapes at once                   -> 3 lines, exit 1
#   G7  files under exempt trees are ignored       -> exit 0
#   G8  real repo untouched (status identical)
#
# SYNC cases (scripts/sync-agents-md.sh, in synthetic git repos):
#   S1  clean consistent tree                      -> no changes, exit 0
#   S2  only CLAUDE.md changed vs HEAD             -> CLAUDE wins, exit 0
#   S3  only AGENTS.md changed vs HEAD             -> AGENTS wins, exit 0
#   S4  both changed and differing                 -> CONFLICT, exit 1,
#                                                    neither file touched
#   S5  neither changed, differing (bad commit)    -> PRE-EXISTING, exit 1,
#                                                    neither file touched
#   S6  AGENTS.md absent                           -> created, exit 0
#   S7  CLAUDE.md absent                           -> created, exit 0
#   S8  untracked CLAUDE.md only                   -> AGENTS.md created, exit 0
#   S9  --dry-run with pending action              -> nothing changed, exit 1
#   S10 not a git repository                       -> exit 2
#
# Exit code: 0 if all assertions pass, 1 if any assertion fails.

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GUARD="$SCRIPT_DIR/check-agents-md-sync.sh"
SYNC="$SCRIPT_DIR/sync-agents-md.sh"
REAL_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

PASS=0
FAIL=0
ERRORS=()

TMP_BASE="$(mktemp -d /tmp/agents-md-test.XXXXXX)" || exit 2
trap 'rm -rf "$TMP_BASE"' EXIT

# ─── Assert helpers ───────────────────────────────────────────────────────────

assert_exit_code() {
  local label="$1" expected="$2" actual="$3"
  if [ "$actual" -eq "$expected" ]; then
    echo "  PASS [$label]: exit $actual (expected $expected)"
    PASS=$((PASS + 1))
  else
    echo "  FAIL [$label]: exit $actual (expected $expected)"
    FAIL=$((FAIL + 1))
    ERRORS+=("[$label] expected exit $expected, got $actual")
  fi
}

assert_output_contains() {
  local label="$1" needle="$2" file="$3"
  if grep -qF -- "$needle" "$file"; then
    echo "  PASS [$label]: output contains '$needle'"
    PASS=$((PASS + 1))
  else
    echo "  FAIL [$label]: output does NOT contain '$needle'"
    FAIL=$((FAIL + 1))
    ERRORS+=("[$label] expected output to contain '$needle'")
  fi
}

assert_output_not_contains() {
  local label="$1" needle="$2" file="$3"
  if grep -qF -- "$needle" "$file"; then
    echo "  FAIL [$label]: output unexpectedly contains '$needle'"
    FAIL=$((FAIL + 1))
    ERRORS+=("[$label] expected output to NOT contain '$needle'")
  else
    echo "  PASS [$label]: output does not contain '$needle'"
    PASS=$((PASS + 1))
  fi
}

assert_files_equal() {
  local label="$1" a="$2" b="$3"
  if cmp -s "$a" "$b"; then
    echo "  PASS [$label]: $a == $b"
    PASS=$((PASS + 1))
  else
    echo "  FAIL [$label]: $a != $b"
    FAIL=$((FAIL + 1))
    ERRORS+=("[$label] expected $a and $b to be byte-identical")
  fi
}

assert_files_unequal() {
  local label="$1" a="$2" b="$3"
  if cmp -s "$a" "$b"; then
    echo "  FAIL [$label]: $a == $b (expected differing)"
    FAIL=$((FAIL + 1))
    ERRORS+=("[$label] expected $a and $b to differ")
  else
    echo "  PASS [$label]: $a != $b"
    PASS=$((PASS + 1))
  fi
}

assert_missing() {
  local label="$1" path="$2"
  if [ ! -e "$path" ]; then
    echo "  PASS [$label]: $path absent"
    PASS=$((PASS + 1))
  else
    echo "  FAIL [$label]: $path exists (expected absent)"
    FAIL=$((FAIL + 1))
    ERRORS+=("[$label] expected $path to be absent")
  fi
}

# ─── Fixture helpers ──────────────────────────────────────────────────────────

write_file() {
  # write_file <repo> <relpath> <content>
  local f="$1/$2"
  mkdir -p "$(dirname "$f")"
  printf '%s\n' "$3" > "$f"
}

new_tree() {
  # new_tree <name> — a plain directory (no git) for guard-only tests.
  local d="$TMP_BASE/$1"
  mkdir -p "$d"
  printf '%s' "$d"
}

new_repo() {
  # new_repo <name> — prints a fresh git repo path with identity configured.
  local d="$TMP_BASE/$1"
  mkdir -p "$d"
  git -C "$d" init -q > "$TMP_BASE/$1.gitlog" 2>&1
  local ge=$?
  if [ "$ge" -ne 0 ]; then
    echo "COMPANION HARNESS FAILURE: git init failed in $d (exit $ge)" >&2
    cat "$TMP_BASE/$1.gitlog" >&2
    exit 2
  fi
  printf '%s' "$d"
}

repo_commit_all() {
  # repo_commit_all <repo> <message>
  git -C "$1" add -A > /dev/null 2>&1
  git -C "$1" -c user.email=companion@test -c user.name=companion \
    commit -q -m "$2" > /dev/null 2>&1
  local ge=$?
  if [ "$ge" -ne 0 ]; then
    echo "COMPANION HARNESS FAILURE: git commit failed in $1 (exit $ge)" >&2
    exit 2
  fi
}

# Snapshot the real repo BEFORE any test runs (G8 compares after).
git -C "$REAL_ROOT" status --porcelain > "$TMP_BASE/real-before.txt" 2>/dev/null

echo "=== check-agents-md-sync companion (guard + fixer) ==="
echo ""

# ─── G1: zero pairs -> vacuous pass ───────────────────────────────────────────

echo "G1: guard passes a tree with zero CLAUDE.md/AGENTS.md files"
T="$(new_tree g1)"
write_file "$T" "README.md" "hello"
REPO_ROOT="$T" bash "$GUARD" > "$TMP_BASE/g1.out" 2>&1
ge=$?
assert_exit_code "g1-zero-pairs-exit" 0 "$ge"
assert_output_contains "g1-zero-pairs-ok" "OK" "$TMP_BASE/g1.out"

# ─── G2: matched pairs -> pass ────────────────────────────────────────────────

echo ""
echo "G2: guard passes matched pairs"
T="$(new_tree g2)"
write_file "$T" "CLAUDE.md" "same content"
write_file "$T" "AGENTS.md" "same content"
write_file "$T" "pkg/tools/CLAUDE.md" "nested pair"
write_file "$T" "pkg/tools/AGENTS.md" "nested pair"
REPO_ROOT="$T" bash "$GUARD" > "$TMP_BASE/g2.out" 2>&1
ge=$?
assert_exit_code "g2-exit" 0 "$ge"
assert_output_contains "g2-count" "2 pair(s)" "$TMP_BASE/g2.out"

# ─── G3: missing twin -> RED ──────────────────────────────────────────────────

echo ""
echo "G3: CLAUDE.md with no AGENTS.md twin is caught"
T="$(new_tree g3)"
write_file "$T" "alpha/CLAUDE.md" "only claude here"
write_file "$T" "beta/CLAUDE.md" "paired"
write_file "$T" "beta/AGENTS.md" "paired"
REPO_ROOT="$T" bash "$GUARD" > "$TMP_BASE/g3.out" 2>&1
ge=$?
assert_exit_code "g3-exit" 1 "$ge"
assert_output_contains "g3-names-dir" "agents-md-sync: alpha: missing twin" "$TMP_BASE/g3.out"
assert_output_not_contains "g3-no-false-positive" "beta" "$TMP_BASE/g3.out"

# ─── G4: extra twin -> RED ────────────────────────────────────────────────────

echo ""
echo "G4: AGENTS.md with no CLAUDE.md twin is caught"
T="$(new_tree g4)"
write_file "$T" "gamma/AGENTS.md" "only agents here"
REPO_ROOT="$T" bash "$GUARD" > "$TMP_BASE/g4.out" 2>&1
ge=$?
assert_exit_code "g4-exit" 1 "$ge"
assert_output_contains "g4-names-dir" "agents-md-sync: gamma: extra twin" "$TMP_BASE/g4.out"

# ─── G5: differing content -> RED ─────────────────────────────────────────────

echo ""
echo "G5: byte-differing pair is caught"
T="$(new_tree g5)"
write_file "$T" "delta/CLAUDE.md" "content A"
write_file "$T" "delta/AGENTS.md" "content B"
REPO_ROOT="$T" bash "$GUARD" > "$TMP_BASE/g5.out" 2>&1
ge=$?
assert_exit_code "g5-exit" 1 "$ge"
assert_output_contains "g5-names-dir" "agents-md-sync: delta: differing" "$TMP_BASE/g5.out"

# Byte-identity is literal: a trailing-newline-only difference must also fail.
T="$(new_tree g5b)"
printf 'content' > "$T/CLAUDE.md"
printf 'content\n' > "$T/AGENTS.md"
REPO_ROOT="$T" bash "$GUARD" > "$TMP_BASE/g5b.out" 2>&1
ge=$?
assert_exit_code "g5b-newline-exit" 1 "$ge"
assert_output_contains "g5b-names-dir" "differing" "$TMP_BASE/g5b.out"

# ─── G6: all three shapes at once -> three lines, RED ─────────────────────────

echo ""
echo "G6: all three failure shapes reported together"
T="$(new_tree g6)"
write_file "$T" "missingdir/CLAUDE.md" "x"
write_file "$T" "extradir/AGENTS.md" "x"
write_file "$T" "diffdir/CLAUDE.md" "a"
write_file "$T" "diffdir/AGENTS.md" "b"
REPO_ROOT="$T" bash "$GUARD" > "$TMP_BASE/g6.out" 2>&1
ge=$?
assert_exit_code "g6-exit" 1 "$ge"
assert_output_contains "g6-missing" "missingdir: missing twin" "$TMP_BASE/g6.out"
assert_output_contains "g6-extra" "extradir: extra twin" "$TMP_BASE/g6.out"
assert_output_contains "g6-differing" "diffdir: differing" "$TMP_BASE/g6.out"
THREE="$(grep -c '^agents-md-sync: ' "$TMP_BASE/g6.out")"
if [ "$THREE" -eq 3 ]; then
  echo "  PASS [g6-three-lines]: exactly 3 finding lines"
  PASS=$((PASS + 1))
else
  echo "  FAIL [g6-three-lines]: expected 3 finding lines, got $THREE"
  FAIL=$((FAIL + 1))
  ERRORS+=("[g6-three-lines] expected 3 finding lines, got $THREE")
fi

# ─── G7: exempt trees are ignored ─────────────────────────────────────────────

echo ""
echo "G7: stray files under exempt trees are ignored"
T="$(new_tree g7)"
write_file "$T" "node_modules/somepkg/CLAUDE.md" "vendored, no twin"
write_file "$T" "vendor/lib/AGENTS.md" "vendored, no twin"
write_file "$T" "dist/CLAUDE.md" "built, no twin"
write_file "$T" "pkg/gateway/spa/AGENTS.md" "embedded, no twin"
write_file "$T" "src/lib/api/generated/CLAUDE.md" "generated, no twin"
REPO_ROOT="$T" bash "$GUARD" > "$TMP_BASE/g7.out" 2>&1
ge=$?
assert_exit_code "g7-exit" 0 "$ge"
assert_output_contains "g7-ok" "OK" "$TMP_BASE/g7.out"

# ─── Guard half done; now the fixer, in synthetic git repos ───────────────────

echo ""
echo "S1: sync on a clean consistent tree makes no changes, exits 0"
R="$(new_repo s1)"
write_file "$R" "CLAUDE.md" "root pair"
write_file "$R" "AGENTS.md" "root pair"
write_file "$R" "pkg/x/CLAUDE.md" "nested pair"
write_file "$R" "pkg/x/AGENTS.md" "nested pair"
repo_commit_all "$R" "consistent"
REPO_ROOT="$R" bash "$SYNC" > "$TMP_BASE/s1.out" 2>&1
ge=$?
assert_exit_code "s1-exit" 0 "$ge"
assert_exit_code "s1-clean-after" 0 "$(git -C "$R" status --porcelain > /dev/null 2>&1; echo $?)"
assert_output_contains "s1-consistent" "tree consistent" "$TMP_BASE/s1.out"

echo ""
echo "S2: only CLAUDE.md changed vs HEAD -> CLAUDE.md wins"
R="$(new_repo s2)"
write_file "$R" "CLAUDE.md" "old"
write_file "$R" "AGENTS.md" "old"
repo_commit_all "$R" "seed"
write_file "$R" "CLAUDE.md" "new claude content"
REPO_ROOT="$R" bash "$SYNC" > "$TMP_BASE/s2.out" 2>&1
ge=$?
assert_exit_code "s2-exit" 0 "$ge"
assert_files_equal "s2-agents-overwritten" "$R/CLAUDE.md" "$R/AGENTS.md"
assert_output_contains "s2-direction" "copy CLAUDE.md over AGENTS.md" "$TMP_BASE/s2.out"

echo ""
echo "S3: only AGENTS.md changed vs HEAD -> AGENTS.md wins"
R="$(new_repo s3)"
write_file "$R" "CLAUDE.md" "old"
write_file "$R" "AGENTS.md" "old"
repo_commit_all "$R" "seed"
write_file "$R" "AGENTS.md" "new agents content"
REPO_ROOT="$R" bash "$SYNC" > "$TMP_BASE/s3.out" 2>&1
ge=$?
assert_exit_code "s3-exit" 0 "$ge"
assert_files_equal "s3-claude-overwritten" "$R/CLAUDE.md" "$R/AGENTS.md"
assert_output_contains "s3-direction" "copy AGENTS.md over CLAUDE.md" "$TMP_BASE/s3.out"

echo ""
echo "S4: both changed and differing -> CONFLICT, neither file touched"
R="$(new_repo s4)"
write_file "$R" "sub/CLAUDE.md" "old"
write_file "$R" "sub/AGENTS.md" "old"
repo_commit_all "$R" "seed"
write_file "$R" "sub/CLAUDE.md" "claude edited"
write_file "$R" "sub/AGENTS.md" "agents edited"
REPO_ROOT="$R" bash "$SYNC" > "$TMP_BASE/s4.out" 2>&1
ge=$?
assert_exit_code "s4-exit" 1 "$ge"
assert_files_unequal "s4-claude-untouched" "$R/sub/CLAUDE.md" "$R/sub/AGENTS.md"
assert_output_contains "s4-conflict" "CONFLICT sub: both CLAUDE.md and AGENTS.md changed" "$TMP_BASE/s4.out"
assert_output_contains "s4-no-side" "no side copied" "$TMP_BASE/s4.out"

echo ""
echo "S5: neither changed, differing -> PRE-EXISTING, no side picked"
R="$(new_repo s5)"
write_file "$R" "CLAUDE.md" "committed claude"
write_file "$R" "AGENTS.md" "committed agents"
repo_commit_all "$R" "committed inconsistent"
REPO_ROOT="$R" bash "$SYNC" > "$TMP_BASE/s5.out" 2>&1
ge=$?
assert_exit_code "s5-exit" 1 "$ge"
assert_output_contains "s5-label" "PRE-EXISTING .: CLAUDE.md and AGENTS.md differ but neither changed" "$TMP_BASE/s5.out"
assert_output_contains "s5-no-side" "no side picked" "$TMP_BASE/s5.out"
assert_files_unequal "s5-untouched" "$R/CLAUDE.md" "$R/AGENTS.md"
assert_output_contains "s5-guard-still-red" "differing" "$TMP_BASE/s5.out"

echo ""
echo "S6: AGENTS.md absent -> created from CLAUDE.md"
R="$(new_repo s6)"
write_file "$R" "docs/CLAUDE.md" "the truth"
repo_commit_all "$R" "seed"
REPO_ROOT="$R" bash "$SYNC" > "$TMP_BASE/s6.out" 2>&1
ge=$?
assert_exit_code "s6-exit" 0 "$ge"
assert_files_equal "s6-created" "$R/docs/CLAUDE.md" "$R/docs/AGENTS.md"
assert_output_contains "s6-label" "create AGENTS.md from CLAUDE.md" "$TMP_BASE/s6.out"

echo ""
echo "S7: CLAUDE.md absent -> created from AGENTS.md"
R="$(new_repo s7)"
write_file "$R" "docs/AGENTS.md" "the truth"
repo_commit_all "$R" "seed"
REPO_ROOT="$R" bash "$SYNC" > "$TMP_BASE/s7.out" 2>&1
ge=$?
assert_exit_code "s7-exit" 0 "$ge"
assert_files_equal "s7-created" "$R/docs/CLAUDE.md" "$R/docs/AGENTS.md"
assert_output_contains "s7-label" "create CLAUDE.md from AGENTS.md" "$TMP_BASE/s7.out"

echo ""
echo "S8: untracked CLAUDE.md only -> AGENTS.md created"
R="$(new_repo s8)"
write_file "$R" "README.md" "seed"
repo_commit_all "$R" "seed"
write_file "$R" "CLAUDE.md" "fresh from a harness"
REPO_ROOT="$R" bash "$SYNC" > "$TMP_BASE/s8.out" 2>&1
ge=$?
assert_exit_code "s8-exit" 0 "$ge"
assert_files_equal "s8-created" "$R/CLAUDE.md" "$R/AGENTS.md"

echo ""
echo "S9: --dry-run reports the action, changes nothing, exits 1"
R="$(new_repo s9)"
write_file "$R" "CLAUDE.md" "old"
write_file "$R" "AGENTS.md" "old"
repo_commit_all "$R" "seed"
write_file "$R" "CLAUDE.md" "new content"
REPO_ROOT="$R" bash "$SYNC" --dry-run > "$TMP_BASE/s9.out" 2>&1
ge=$?
assert_exit_code "s9-exit" 1 "$ge"
assert_files_unequal "s9-nothing-applied" "$R/CLAUDE.md" "$R/AGENTS.md"
assert_output_contains "s9-would" "would copy" "$TMP_BASE/s9.out"
assert_output_contains "s9-pending" "pending" "$TMP_BASE/s9.out"

echo ""
echo "S10: not a git repository -> exit 2"
T="$(new_tree s10)"
write_file "$T" "CLAUDE.md" "x"
write_file "$T" "AGENTS.md" "y"
REPO_ROOT="$T" bash "$SYNC" > "$TMP_BASE/s10.out" 2>&1
ge=$?
assert_exit_code "s10-exit" 2 "$ge"
assert_output_contains "s10-reason" "not a git repository" "$TMP_BASE/s10.out"

# ─── G8: the real repo was never touched ──────────────────────────────────────

echo ""
echo "G8: real repo untouched (git status identical before/after)"
git -C "$REAL_ROOT" status --porcelain > "$TMP_BASE/real-after.txt" 2>/dev/null
assert_files_equal "g8-real-status-identical" "$TMP_BASE/real-before.txt" "$TMP_BASE/real-after.txt"

# ─── Summary ──────────────────────────────────────────────────────────────────

echo ""
echo "─────────────────────────────────────────"
echo "Results: ${PASS} passed, ${FAIL} failed"

if [ "$FAIL" -gt 0 ]; then
  echo ""
  echo "Failures:"
  for e in "${ERRORS[@]:-}"; do
    [ -n "$e" ] && echo "  - $e"
  done
  exit 1
fi

echo "All assertions passed."
exit 0
