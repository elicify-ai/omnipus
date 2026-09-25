#!/usr/bin/env bash
# check-spec-status.test.sh
#
# Proof-of-failure companion for check-spec-status.sh. Builds a real,
# throwaway git repository under a temp directory for every case (this
# guard's whole job is diffing against a base ref, so a fixture tree
# without real git history and branches could never exercise it) and NEVER
# touches the real repo tree. Modelled on
# scripts/check-no-goal-field-erasure.test.sh's assert helpers.
#
# Covers:
#   1. A pre-existing spec, UNCHANGED on the feature branch — must NOT be
#      flagged (the "do NOT fail on the 214 existing specs" requirement).
#   2. A NEW spec with no Status: line at all — CAUGHT (exit 1).
#   3. A NEW spec with a valid "Status: Draft" line — clean (exit 0).
#   4. A NEW spec with an invalid status value ("Status: WIP") — CAUGHT.
#   5. A NEW spec using bold markdown ("**Status:** Approved") — clean,
#      proves the '*' stripping.
#   6. A pre-existing spec that IS modified on the feature branch, still
#      with no Status: line — CAUGHT (the "changed" half of "NEW or
#      changed", not just "new").
#   7. A pre-existing spec DELETED on the feature branch — must NOT be
#      flagged (nothing left to check).
#   8. A changed file that is not a *-spec.md (e.g. a -spec-review.md) —
#      never scanned at all.
#   9. The base ref cannot be resolved (bogus ref, no matching branch, no
#      origin remote) — WARNING + exit 0, never a false failure and never
#      a silent "scan everything".
#  10. REPO_ROOT not a directory — exit 2.
#  11. REPO_ROOT not a git repository — exit 2.
#
# Exit code: 0 if every assertion passed, 1 if any failed.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LINT_SCRIPT="${SCRIPT_DIR}/check-spec-status.sh"

PASS=0
FAIL=0
ERRORS=()

TMP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/check-spec-status-test.XXXXXX")
trap 'rm -rf "$TMP_DIR"' EXIT

assert_exit_code() {
  local label="$1" expected="$2" actual="$3"
  if [[ "$actual" -eq "$expected" ]]; then
    echo "  PASS [$label]: exit $actual (expected $expected)"
    PASS=$((PASS + 1))
  else
    echo "  FAIL [$label]: exit $actual (expected $expected)"
    FAIL=$((FAIL + 1))
    ERRORS+=("[$label] expected exit $expected, got $actual")
  fi
}

assert_output_contains() {
  local label="$1" needle="$2" haystack="$3"
  if echo "$haystack" | grep -qF "$needle"; then
    echo "  PASS [$label]: output contains '$needle'"
    PASS=$((PASS + 1))
  else
    echo "  FAIL [$label]: output does NOT contain '$needle'"
    FAIL=$((FAIL + 1))
    ERRORS+=("[$label] expected output to contain '$needle'")
  fi
}

assert_output_not_contains() {
  local label="$1" needle="$2" haystack="$3"
  if echo "$haystack" | grep -qF "$needle"; then
    echo "  FAIL [$label]: output unexpectedly contains '$needle'"
    FAIL=$((FAIL + 1))
    ERRORS+=("[$label] expected output to NOT contain '$needle'")
  else
    echo "  PASS [$label]: output does not contain '$needle'"
    PASS=$((PASS + 1))
  fi
}

# Builds a fresh repo at $REPO with a 'main' branch carrying one
# pre-existing spec (no Status: line, standing in for the 214 existing
# specs) and checks out a 'feature' branch on top of it for the caller to
# add/modify/delete files on before invoking the guard.
new_repo_with_feature_branch() {
  REPO="$TMP_DIR/repo-$RANDOM"
  mkdir -p "$REPO/docs/internal/specs"
  git -C "$REPO" init --quiet --initial-branch=main
  git -C "$REPO" config user.email "test@example.invalid"
  git -C "$REPO" config user.name "check-spec-status test"
  cat > "$REPO/docs/internal/specs/existing-spec.md" <<'EOF'
# Existing feature — spec

No Status field — this predates DECISIONS.md #7 and must never be flagged
unless this branch itself touches it.
EOF
  git -C "$REPO" add -A
  git -C "$REPO" commit --quiet -m "base: pre-existing spec with no Status field"
  git -C "$REPO" checkout --quiet -b feature
  echo "$REPO"
}

echo "=== check-spec-status self-test ==="

# --- Test 1: unchanged pre-existing spec is never flagged -------------------

echo ""
echo "Test 1: an unchanged pre-existing spec is not flagged"
REPO="$(new_repo_with_feature_branch)"
# Touch an unrelated file so the branch has A commit to diff against main.
echo "unrelated" > "$REPO/README.md"
git -C "$REPO" add -A
git -C "$REPO" commit --quiet -m "unrelated change"
OUTPUT=$(REPO_ROOT="$REPO" CHECK_SPEC_STATUS_BASE_REF=main bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "unchanged-pre-existing-exit" 0 "$EXIT_CODE"
assert_output_not_contains "unchanged-pre-existing-no-finding" "existing-spec.md" "$OUTPUT"

# --- Test 2: a new spec with no Status: line — CAUGHT -----------------------

echo ""
echo "Test 2: a new spec with no Status line is caught"
REPO="$(new_repo_with_feature_branch)"
cat > "$REPO/docs/internal/specs/new-feature-spec.md" <<'EOF'
# New feature — spec

No status line here at all.
EOF
git -C "$REPO" add -A
git -C "$REPO" commit --quiet -m "add new spec, no Status line"
OUTPUT=$(REPO_ROOT="$REPO" CHECK_SPEC_STATUS_BASE_REF=main bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "new-no-status-exit" 1 "$EXIT_CODE"
assert_output_contains "new-no-status-finding" "new-feature-spec.md" "$OUTPUT"

# --- Test 3: a new spec with a valid Status line — clean --------------------

echo ""
echo "Test 3: a new spec with 'Status: Draft' is clean"
REPO="$(new_repo_with_feature_branch)"
cat > "$REPO/docs/internal/specs/new-feature-spec.md" <<'EOF'
# New feature — spec

Status: Draft

Body.
EOF
git -C "$REPO" add -A
git -C "$REPO" commit --quiet -m "add new spec with valid Status"
OUTPUT=$(REPO_ROOT="$REPO" CHECK_SPEC_STATUS_BASE_REF=main bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "new-valid-status-exit" 0 "$EXIT_CODE"
assert_output_not_contains "new-valid-status-no-finding" "new-feature-spec.md:" "$OUTPUT"

# --- Test 4: a new spec with an invalid status value — CAUGHT ---------------

echo ""
echo "Test 4: a new spec with 'Status: WIP' (not one of the five) is caught"
REPO="$(new_repo_with_feature_branch)"
cat > "$REPO/docs/internal/specs/new-feature-spec.md" <<'EOF'
# New feature — spec

Status: WIP
EOF
git -C "$REPO" add -A
git -C "$REPO" commit --quiet -m "add new spec with invalid Status"
OUTPUT=$(REPO_ROOT="$REPO" CHECK_SPEC_STATUS_BASE_REF=main bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "new-invalid-status-exit" 1 "$EXIT_CODE"
assert_output_contains "new-invalid-status-finding" "new-feature-spec.md" "$OUTPUT"

# --- Test 5: bold markdown Status line — clean (proves '*' stripping) ------

echo ""
echo "Test 5: '**Status:** Approved' (bold markdown) is clean"
REPO="$(new_repo_with_feature_branch)"
cat > "$REPO/docs/internal/specs/new-feature-spec.md" <<'EOF'
# New feature — spec

**Status:** Approved
EOF
git -C "$REPO" add -A
git -C "$REPO" commit --quiet -m "add new spec with bold Status"
OUTPUT=$(REPO_ROOT="$REPO" CHECK_SPEC_STATUS_BASE_REF=main bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "bold-status-exit" 0 "$EXIT_CODE"
assert_output_not_contains "bold-status-no-finding" "new-feature-spec.md:" "$OUTPUT"

# --- Test 6: a pre-existing spec MODIFIED with no Status — CAUGHT ----------

echo ""
echo "Test 6: a pre-existing spec modified (still no Status) is caught"
REPO="$(new_repo_with_feature_branch)"
cat >> "$REPO/docs/internal/specs/existing-spec.md" <<'EOF'

One more paragraph, still no Status field.
EOF
git -C "$REPO" add -A
git -C "$REPO" commit --quiet -m "touch the pre-existing spec, still no Status"
OUTPUT=$(REPO_ROOT="$REPO" CHECK_SPEC_STATUS_BASE_REF=main bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "changed-pre-existing-exit" 1 "$EXIT_CODE"
assert_output_contains "changed-pre-existing-finding" "existing-spec.md" "$OUTPUT"

# --- Test 7: a pre-existing spec DELETED — not flagged ----------------------

echo ""
echo "Test 7: a pre-existing spec deleted on the branch is not flagged"
REPO="$(new_repo_with_feature_branch)"
git -C "$REPO" rm --quiet "docs/internal/specs/existing-spec.md"
git -C "$REPO" commit --quiet -m "remove the pre-existing spec"
OUTPUT=$(REPO_ROOT="$REPO" CHECK_SPEC_STATUS_BASE_REF=main bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "deleted-spec-exit" 0 "$EXIT_CODE"
assert_output_not_contains "deleted-spec-no-finding" "existing-spec.md" "$OUTPUT"

# --- Test 8: a changed non-*-spec.md file is never scanned -----------------

echo ""
echo "Test 8: a changed *-spec-review.md file is never scanned"
REPO="$(new_repo_with_feature_branch)"
cat > "$REPO/docs/internal/specs/new-feature-spec-review.md" <<'EOF'
# Review — no Status field, and none required: this is not a *-spec.md file.
EOF
git -C "$REPO" add -A
git -C "$REPO" commit --quiet -m "add a spec review file"
OUTPUT=$(REPO_ROOT="$REPO" CHECK_SPEC_STATUS_BASE_REF=main bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "review-file-exit" 0 "$EXIT_CODE"
assert_output_not_contains "review-file-no-finding" "new-feature-spec-review.md" "$OUTPUT"

# --- Test 9: an unresolvable base ref — WARNING + exit 0, never "scan all" -

echo ""
echo "Test 9: an unresolvable base ref warns and exits 0, never scans everything"
REPO="$(new_repo_with_feature_branch)"
cat > "$REPO/docs/internal/specs/new-feature-spec.md" <<'EOF'
# New feature — spec

No Status line — if this guard fell back to scanning everything on an
unresolvable base, this file (and existing-spec.md) would both fail here.
EOF
git -C "$REPO" add -A
git -C "$REPO" commit --quiet -m "add new spec, no Status line"
OUTPUT=$(REPO_ROOT="$REPO" CHECK_SPEC_STATUS_BASE_REF="does-not-exist-anywhere" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "unresolvable-base-exit" 0 "$EXIT_CODE"
assert_output_contains "unresolvable-base-warning" "cannot resolve integration branch" "$OUTPUT"
assert_output_not_contains "unresolvable-base-no-finding" "new-feature-spec.md:" "$OUTPUT"

# --- Test 10: REPO_ROOT not a directory — exit 2 ----------------------------

echo ""
echo "Test 10: a nonexistent REPO_ROOT exits 2"
OUTPUT=$(REPO_ROOT="$TMP_DIR/does-not-exist" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "bad-repo-root-exit" 2 "$EXIT_CODE"

# --- Test 11: REPO_ROOT not a git repository — exit 2 -----------------------

echo ""
echo "Test 11: a REPO_ROOT with no .git exits 2"
NOTAREPO="$TMP_DIR/not-a-repo"
mkdir -p "$NOTAREPO"
OUTPUT=$(REPO_ROOT="$NOTAREPO" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "not-a-repo-exit" 2 "$EXIT_CODE"

# --- Summary -----------------------------------------------------------------

# ── Self-contained cases for base resolution without a PR base ──
BR=$(mktemp -d)
git -C "$BR" init --quiet --initial-branch=main
git -C "$BR" config user.email t@example.invalid; git -C "$BR" config user.name t
mkdir -p "$BR/docs/internal/specs"; echo "x" > "$BR/README.md"
git -C "$BR" add -A; git -C "$BR" commit --quiet -m base
BEFORE_SHA=$(git -C "$BR" rev-parse HEAD)
printf '# New spec without a status line\n' > "$BR/docs/internal/specs/new-thing-spec.md"
git -C "$BR" add -A; git -C "$BR" commit --quiet -m "add spec"

# no base at all -> WARNING + pass (never a main fallback)
OUT=$(cd "$BR" && env -u GITHUB_BASE_REF -u OMNIPUS_INTEGRATION_BRANCH -u CHECK_SPEC_STATUS_BASE_REF -u GITHUB_EVENT_NAME -u GITHUB_EVENT_PATH REPO_ROOT="$BR" bash "$LINT_SCRIPT" 2>&1); RC=$?
if [ $RC -eq 0 ] && printf '%s' "$OUT" | grep -q "no base, fail-open"; then echo "PASS [no-base-fail-open]"; PASS=$((PASS+1)); else echo "FAIL [no-base-fail-open]"; FAIL=$((FAIL+1)); ERRORS+=("no-base-fail-open: rc=$RC $OUT"); fi

# push event -> compares against github.event.before, so the spec added by the push is checked and fails
EVT=$(mktemp); printf '{"before":"%s"}' "$BEFORE_SHA" > "$EVT"
OUT=$(cd "$BR" && env -u GITHUB_BASE_REF -u OMNIPUS_INTEGRATION_BRANCH -u CHECK_SPEC_STATUS_BASE_REF REPO_ROOT="$BR" GITHUB_EVENT_NAME=push GITHUB_EVENT_PATH="$EVT" bash "$LINT_SCRIPT" 2>&1); RC=$?
if [ $RC -ne 0 ] && printf '%s' "$OUT" | grep -q "new-thing-spec.md"; then echo "PASS [push-before-base-catches-new-spec]"; PASS=$((PASS+1)); else echo "FAIL [push-before-base-catches-new-spec]"; FAIL=$((FAIL+1)); ERRORS+=("push-before-base: rc=$RC $OUT"); fi

# push event creating the branch (before = all zeros) -> WARNING + pass
printf '{"before":"0000000000000000000000000000000000000000"}' > "$EVT"
OUT=$(cd "$BR" && env -u GITHUB_BASE_REF -u OMNIPUS_INTEGRATION_BRANCH -u CHECK_SPEC_STATUS_BASE_REF REPO_ROOT="$BR" GITHUB_EVENT_NAME=push GITHUB_EVENT_PATH="$EVT" bash "$LINT_SCRIPT" 2>&1); RC=$?
if [ $RC -eq 0 ] && printf '%s' "$OUT" | grep -q "no base, fail-open"; then echo "PASS [push-new-branch-fail-open]"; PASS=$((PASS+1)); else echo "FAIL [push-new-branch-fail-open]"; FAIL=$((FAIL+1)); ERRORS+=("push-new-branch: rc=$RC $OUT"); fi
rm -rf "$BR" "$EVT"

echo ""
echo "─────────────────────────────────────────"
echo "Results: ${PASS} passed, ${FAIL} failed"

if [[ "$FAIL" -gt 0 ]]; then
  echo ""
  echo "Failures:"
  for e in "${ERRORS[@]}"; do
    echo "  - $e"
  done
  exit 1
fi

echo "All assertions passed."
exit 0
