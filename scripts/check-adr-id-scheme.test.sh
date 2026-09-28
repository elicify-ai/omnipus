#!/usr/bin/env bash
# check-adr-id-scheme.test.sh
#
# Proof-of-failure companion for check-adr-id-scheme.sh (and a behavioural
# self-test for its twin, scripts/new-adr-id.sh). Builds a real, throwaway
# git repository under a temp directory for every case (the guard's whole
# job is diffing against a base ref, so a fixture without real git history
# could never exercise it) and NEVER touches the real repo tree. Modelled
# on scripts/check-spec-status.test.sh's assert helpers and fixture.
#
# Guard cases:
#   1. Unchanged pre-existing legacy ADR — must NOT be flagged.
#   2. A NEW ADR in the old 3-digit scheme, OUTSIDE the grandfather range
#      (ADR-097-…) — CAUGHT (exit 1).
#   3. Two NEW files, identical slug, different dates — CAUGHT (exit 1),
#      both named.
#   4. A NEW file matching the grandfather regex (ADR-096-… + its -review
#      sibling) despite the old numbered shape — EXEMPT (exit 0).
#   5. A NEW file in the correct new scheme, fresh slug — clean (exit 0),
#      and the legacy ADR stays unflagged in the same run.
#   6. A NEW date-slug file duplicating a PRE-EXISTING legacy ADR's slug —
#      CAUGHT (exit 1), both files named.
#   7. Malformed new files — uppercase/underscore, double hyphen, 81-char
#      slug — each CAUGHT with its specific reason.
#   8. A MODIFIED (not added) legacy ADR — NOT flagged (--diff-filter=A).
#   9. Unresolvable base ref — WARNING + exit 0, never "scan everything".
#  10/11. REPO_ROOT not a directory / not a git repo — exit 2.
#  16. Base ref resolves but the diff itself fails ("no merge base", as a
#      shallow CI checkout produces) — WARNING naming the git error +
#      exit 0; the false-clean "0 new ADR files" pass is gone.
#  17/18. GITHUB_BASE_REF — the source GitHub Actions populates for PR
#      runs, requiring no workflow wiring — drives both a finding case
#      and a clean case (the CHECK_ADR_ID_SCHEME_BASE_REF override unset).
#  19. A new date-scheme file duplicating a GRANDFATHERED file's slug
#      (ADR-095 vs ADR-20260927, identical slug) — CAUGHT, both named.
#  20. Missing specific-reason assertions: leading hyphen, trailing
#      hyphen, non-date ID token, missing slug after the date.
#  21. No base source at all (no override, no GITHUB_BASE_REF, no
#      OMNIPUS_INTEGRATION_BRANCH, not a push event) — the first fail-open
#      tier: "no comparison base" WARNING + exit 0.
#
# Generator cases (scripts/new-adr-id.sh):
#  12. Fresh title → exactly one stdout line matching
#      ADR-[0-9]{8}-[a-z0-9]+(-[a-z0-9]+)*, lowercase alnum only.
#  13. A title colliding with an existing ADR file (the real ADR-095
#      "Thinking Visibility Gate And Signed Blocks" case — that file sits
#      on an in-flight branch invisible from the release ref, so the
#      fixture reproduces it deterministically) — REFUSED, nonzero exit,
#      stderr names the colliding file.
#  14. All-punctuation title (empty slug) — REFUSED, stdout empty.
#  15. Long title truncates to ≤80; a truncation landing on a hyphen is
#      re-trimmed (never a trailing-hyphen slug).
#  22. A ref that resolves but whose ls-tree fails (missing tree object) —
#      WARNING + working-tree-only fallback, still mints the ID (exit 0).
#
# Exit code: 0 if every assertion passed, 1 if any failed.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LINT_SCRIPT="${SCRIPT_DIR}/check-adr-id-scheme.sh"
GEN_SCRIPT="${SCRIPT_DIR}/new-adr-id.sh"

PASS=0
FAIL=0
ERRORS=()

TMP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/check-adr-id-scheme-test.XXXXXX")
trap 'rm -rf "$TMP_DIR"' EXIT

assert_exit_code() {
  local label="$1" expected="$2" actual="$3"
  if [[ "$actual" -eq "$expected" ]]; then
    echo "  PASS [$label]: got $actual (expected $expected)"
    PASS=$((PASS + 1))
  else
    echo "  FAIL [$label]: got $actual (expected $expected)"
    FAIL=$((FAIL + 1))
    ERRORS+=("[$label] expected $expected, got $actual")
  fi
}

assert_output_contains() {
  local label="$1" needle="$2" haystack="$3"
  if printf '%s' "$haystack" | grep -qF -- "$needle"; then
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
  if printf '%s' "$haystack" | grep -qF -- "$needle"; then
    echo "  FAIL [$label]: output unexpectedly contains '$needle'"
    FAIL=$((FAIL + 1))
    ERRORS+=("[$label] output must NOT contain '$needle'")
  else
    echo "  PASS [$label]: output does not contain '$needle'"
    PASS=$((PASS + 1))
  fi
}

# A fresh repo: 'main' carries one pre-existing legacy ADR (predates the
# scheme; must never be flagged); a 'feature' branch is checked out on top
# for the caller to add/modify files on.
new_repo_with_feature_branch() {
  REPO="$TMP_DIR/repo-$RANDOM"
  mkdir -p "$REPO/docs/internal/architecture"
  git -C "$REPO" init --quiet --initial-branch=main
  git -C "$REPO" config user.email "test@example.invalid"
  git -C "$REPO" config user.name "check-adr-id-scheme test"
  printf '# ADR-077 — two layers\n\nLegacy numbered ADR; never checked by the guard.\n' \
    > "$REPO/docs/internal/architecture/ADR-077-two-layers.md"
  git -C "$REPO" add -A
  git -C "$REPO" commit --quiet -m "base: pre-existing legacy ADR"
  git -C "$REPO" checkout --quiet -b feature
  echo "$REPO"
}

echo "=== check-adr-id-scheme self-test ==="

# --- Test 1: unchanged pre-existing legacy ADR is never flagged -------------

echo ""
echo "Test 1: an unchanged pre-existing legacy ADR is not flagged"
REPO="$(new_repo_with_feature_branch)"
echo "unrelated" > "$REPO/README.md"
git -C "$REPO" add -A
git -C "$REPO" commit --quiet -m "unrelated change"
OUTPUT=$(REPO_ROOT="$REPO" CHECK_ADR_ID_SCHEME_BASE_REF=main bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "legacy-unchanged-exit" 0 "$EXIT_CODE"
assert_output_not_contains "legacy-unchanged-no-finding" "ADR-077-two-layers.md" "$OUTPUT"

# --- Test 2: new 3-digit ADR outside the grandfather range — CAUGHT ---------

echo ""
echo "Test 2: a new 3-digit ADR outside the grandfather range is caught"
REPO="$(new_repo_with_feature_branch)"
printf '# ADR-097\n' > "$REPO/docs/internal/architecture/ADR-097-some-new-thing.md"
git -C "$REPO" add -A
git -C "$REPO" commit --quiet -m "add legacy-numbered ADR"
OUTPUT=$(REPO_ROOT="$REPO" CHECK_ADR_ID_SCHEME_BASE_REF=main bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "legacy-numbered-exit" 1 "$EXIT_CODE"
assert_output_contains "legacy-numbered-finding" "ADR-097-some-new-thing.md" "$OUTPUT"
assert_output_contains "legacy-numbered-reason" "retired 3-digit" "$OUTPUT"

# --- Test 3: two new files, same slug, different dates — CAUGHT -------------

echo ""
echo "Test 3: two new files with the same slug and different dates are caught"
REPO="$(new_repo_with_feature_branch)"
printf '# X\n' > "$REPO/docs/internal/architecture/ADR-20260927-my-feature.md"
printf '# X\n' > "$REPO/docs/internal/architecture/ADR-20260928-my-feature.md"
git -C "$REPO" add -A
git -C "$REPO" commit --quiet -m "add two same-slug ADRs"
OUTPUT=$(REPO_ROOT="$REPO" CHECK_ADR_ID_SCHEME_BASE_REF=main bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "duplicate-new-pair-exit" 1 "$EXIT_CODE"
assert_output_contains "duplicate-new-pair-a" "ADR-20260927-my-feature.md" "$OUTPUT"
assert_output_contains "duplicate-new-pair-b" "ADR-20260928-my-feature.md" "$OUTPUT"

# --- Test 4: grandfather regex exempts ADR-096 + a -review sibling ---------

echo ""
echo "Test 4: grandfathered ADR-096 and its -review sibling are exempt"
REPO="$(new_repo_with_feature_branch)"
printf '# ADR-096\n' > "$REPO/docs/internal/architecture/ADR-096-something-new.md"
printf '# ADR-096 review\n' > "$REPO/docs/internal/architecture/ADR-096-something-new-review.md"
git -C "$REPO" add -A
git -C "$REPO" commit --quiet -m "add grandfathered ADR-096 pair"
OUTPUT=$(REPO_ROOT="$REPO" CHECK_ADR_ID_SCHEME_BASE_REF=main bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "grandfather-exit" 0 "$EXIT_CODE"
assert_output_not_contains "grandfather-not-flagged" "ADR-096-something-new" "$OUTPUT"

# --- Test 5: correct new-scheme ADR with a fresh slug — clean ---------------

echo ""
echo "Test 5: a correct new date-slug ADR with a fresh slug is clean"
REPO="$(new_repo_with_feature_branch)"
printf '# New scheme\n' > "$REPO/docs/internal/architecture/ADR-20260927-unique-fresh-slug.md"
git -C "$REPO" add -A
git -C "$REPO" commit --quiet -m "add correct new-scheme ADR"
OUTPUT=$(REPO_ROOT="$REPO" CHECK_ADR_ID_SCHEME_BASE_REF=main bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "new-scheme-exit" 0 "$EXIT_CODE"
assert_output_not_contains "new-scheme-no-finding" "unique-fresh-slug.md" "$OUTPUT"
assert_output_not_contains "legacy-still-unflagged" "ADR-077-two-layers.md" "$OUTPUT"

# --- Test 6: new file duplicates a PRE-EXISTING legacy slug — CAUGHT --------

echo ""
echo "Test 6: a new file duplicating a legacy ADR's slug is caught"
REPO="$(new_repo_with_feature_branch)"
printf '# Dup\n' > "$REPO/docs/internal/architecture/ADR-20260927-two-layers.md"
git -C "$REPO" add -A
git -C "$REPO" commit --quiet -m "add duplicate of legacy slug"
OUTPUT=$(REPO_ROOT="$REPO" CHECK_ADR_ID_SCHEME_BASE_REF=main bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "cross-scheme-duplicate-exit" 1 "$EXIT_CODE"
assert_output_contains "cross-scheme-duplicate-pair" "ADR-20260927-two-layers.md" "$OUTPUT"
assert_output_contains "cross-scheme-duplicate-legacy-named" "ADR-077-two-layers.md" "$OUTPUT"

# --- Test 7: malformed new files — specific reasons --------------------------

echo ""
echo "Test 7: malformed new files are caught with specific reasons"
REPO="$(new_repo_with_feature_branch)"
LONG81="$(printf 'a%.0s' $(seq 1 81))"
printf '# U\n' > "$REPO/docs/internal/architecture/ADR-20260927-My_Feature.md"
printf '# D\n' > "$REPO/docs/internal/architecture/ADR-20260927-my--feature.md"
printf '# L\n' > "$REPO/docs/internal/architecture/ADR-20260927-$LONG81.md"
git -C "$REPO" add -A
git -C "$REPO" commit --quiet -m "add malformed ADR files"
OUTPUT=$(REPO_ROOT="$REPO" CHECK_ADR_ID_SCHEME_BASE_REF=main bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "malformed-exit" 1 "$EXIT_CODE"
assert_output_contains "malformed-uppercase-finding" "My_Feature" "$OUTPUT"
assert_output_contains "malformed-uppercase-reason" "lowercase alnum" "$OUTPUT"
assert_output_contains "malformed-double-hyphen-finding" "my--feature" "$OUTPUT"
assert_output_contains "malformed-double-hyphen-reason" "contains a double hyphen" "$OUTPUT"
assert_output_contains "malformed-long-slug-finding" "max 80" "$OUTPUT"

# --- Test 8: MODIFIED (not added) legacy ADR — not flagged ------------------

echo ""
echo "Test 8: a modified (not added) legacy ADR is not flagged"
REPO="$(new_repo_with_feature_branch)"
printf 'Extra paragraph.\n' >> "$REPO/docs/internal/architecture/ADR-077-two-layers.md"
git -C "$REPO" add -A
git -C "$REPO" commit --quiet -m "modify the legacy ADR; the scheme never applies retroactively"
OUTPUT=$(REPO_ROOT="$REPO" CHECK_ADR_ID_SCHEME_BASE_REF=main bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "modified-legacy-exit" 0 "$EXIT_CODE"
assert_output_not_contains "modified-legacy-no-finding" "ADR-077-two-layers.md" "$OUTPUT"

# --- Test 9: unresolvable base — WARNING + exit 0, never "scan all" ---------

echo ""
echo "Test 9: an unresolvable base ref warns and exits 0, never scans everything"
REPO="$(new_repo_with_feature_branch)"
printf '# New scheme, bad base\n' > "$REPO/docs/internal/architecture/ADR-20260927-orphan-slug.md"
git -C "$REPO" add -A
git -C "$REPO" commit --quiet -m "add ADR with unresolvable base"
OUTPUT=$(REPO_ROOT="$REPO" CHECK_ADR_ID_SCHEME_BASE_REF="does-not-exist-anywhere" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "unresolvable-base-exit" 0 "$EXIT_CODE"
assert_output_contains "unresolvable-base-warning" "cannot resolve integration branch" "$OUTPUT"
assert_output_not_contains "unresolvable-base-no-finding" "orphan-slug" "$OUTPUT"

# --- Test 10/11: REPO_ROOT sanity — exit 2 -----------------------------------

echo ""
echo "Test 10: a nonexistent REPO_ROOT exits 2"
OUTPUT=$(REPO_ROOT="$TMP_DIR/does-not-exist" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "bad-repo-root-exit" 2 "$EXIT_CODE"

echo ""
echo "Test 11: a REPO_ROOT with no .git exits 2"
NOTAREPO="$TMP_DIR/not-a-repo"
mkdir -p "$NOTAREPO"
OUTPUT=$(REPO_ROOT="$NOTAREPO" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "not-a-repo-exit" 2 "$EXIT_CODE"

# --- Test 16 (FIX 1, silent-failure-hunter CRITICAL) -------------------------
#
# A shallow CI checkout can resolve BASE_COMMIT yet fail the base...HEAD
# diff with "no merge base" (exit 128). This construction is a truly
# disconnected history (an orphan branch — two independent roots), which
# fails merge-base unconditionally, offline, deterministically. A shallow
# fetch alone does NOT reproduce it: the fetched base commit is then HEAD
# direct parent, so the merge base stays visible (verified, git 2.50.1).
# The guard must warn visibly and still exit 0 (fail-open by design).

echo ""
echo "Test 16: base resolves but the diff itself fails — visible WARNING, still exit 0"
REPO="$(new_repo_with_feature_branch)"
git -C "$REPO" checkout --quiet --orphan feature2
git -C "$REPO" rm -qrf . 2>/dev/null
mkdir -p "$REPO/docs/internal/architecture"
printf '# bad\n' > "$REPO/docs/internal/architecture/ADR-097-bad.md"
git -C "$REPO" add -A
git -C "$REPO" commit --quiet -m "orphan root adds a non-conforming ADR"
OUTPUT=$(REPO_ROOT="$REPO" CHECK_ADR_ID_SCHEME_BASE_REF=main bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "diff-fail-exit" 0 "$EXIT_CODE"
assert_output_contains "diff-fail-warning" "WARNING" "$OUTPUT"
assert_output_contains "diff-fail-git-error" "no merge base" "$OUTPUT"
assert_output_not_contains "diff-fail-no-false-clean" "0 new ADR files" "$OUTPUT"

# --- Test 17 (FIX 3, pr-test-analyzer CRITICAL) ------------------------------
#
# GITHUB_BASE_REF is the base-ref source GitHub Actions populates for PR
# runs (no workflow wiring needed, per the guard header) — yet every
# pre-existing case drove the guard through the OVERRIDE only.

echo ""
echo "Test 17: GITHUB_BASE_REF drives a finding without the override"
REPO="$(new_repo_with_feature_branch)"
printf '# ADR-097\n' > "$REPO/docs/internal/architecture/ADR-097-some-new-thing.md"
git -C "$REPO" add -A
git -C "$REPO" commit --quiet -m "add legacy-numbered ADR"
OUTPUT=$(env -u CHECK_ADR_ID_SCHEME_BASE_REF -u OMNIPUS_INTEGRATION_BRANCH GITHUB_BASE_REF=main REPO_ROOT="$REPO" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "github-base-finding-exit" 1 "$EXIT_CODE"
assert_output_contains "github-base-finding-name" "ADR-097-some-new-thing.md" "$OUTPUT"
assert_output_contains "github-base-finding-reason" "retired 3-digit" "$OUTPUT"

# --- Test 18: GITHUB_BASE_REF drives the clean case (CI's real path) ---------

echo ""
echo "Test 18: GITHUB_BASE_REF resolves and reports a clean run"
REPO="$(new_repo_with_feature_branch)"
printf '# Fresh\n' > "$REPO/docs/internal/architecture/ADR-20260927-github-base-fresh.md"
git -C "$REPO" add -A
git -C "$REPO" commit --quiet -m "add correct new-scheme ADR"
OUTPUT=$(env -u CHECK_ADR_ID_SCHEME_BASE_REF -u OMNIPUS_INTEGRATION_BRANCH GITHUB_BASE_REF=main REPO_ROOT="$REPO" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "github-base-clean-exit" 0 "$EXIT_CODE"
assert_output_contains "github-base-clean-resolved" "against main," "$OUTPUT"
assert_output_not_contains "github-base-clean-no-tier1-warning" "no comparison base" "$OUTPUT"
assert_output_not_contains "github-base-clean-no-finding" "github-base-fresh" "$OUTPUT"

# --- Test 19 (FIX 4, pr-test-analyzer CRITICAL) ------------------------------
#
# Grandfathering exempts ADR-094/095/096 from being CHECKED, but they still
# participate as collision counterparts: a new date-scheme file whose slug
# equals a grandfathered file's slug must fail naming both. Mirrors the
# manually-reproduced ADR-095 vs ADR-20260927 same-slug scenario; only the
# GENERATOR side (test 13) covered collisions before this test.

echo ""
echo "Test 19: a new date-scheme file duplicating a grandfathered slug is caught"
REPO="$(new_repo_with_feature_branch)"
git -C "$REPO" checkout --quiet main
printf '# ADR-095\n' > "$REPO/docs/internal/architecture/ADR-095-thinking-visibility-gate-and-signed-blocks.md"
git -C "$REPO" add -A
git -C "$REPO" commit --quiet -m "base: add grandfathered ADR-095"
git -C "$REPO" checkout --quiet -b feature2 main
printf '# dup of 095\n' > "$REPO/docs/internal/architecture/ADR-20260927-thinking-visibility-gate-and-signed-blocks.md"
git -C "$REPO" add -A
git -C "$REPO" commit --quiet -m "add date-scheme duplicate of a grandfathered slug"
OUTPUT=$(REPO_ROOT="$REPO" CHECK_ADR_ID_SCHEME_BASE_REF=main bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "grandfather-slug-dup-exit" 1 "$EXIT_CODE"
assert_output_contains "grandfather-slug-dup-new" "ADR-20260927-thinking-visibility-gate-and-signed-blocks.md" "$OUTPUT"
assert_output_contains "grandfather-slug-dup-old" "ADR-095-thinking-visibility-gate-and-signed-blocks.md" "$OUTPUT"
assert_output_contains "grandfather-slug-dup-reason" "duplicate slug" "$OUTPUT"

# --- Test 20 (FIX 5a, pr-test-analyzer) --------------------------------------
#
# shape_problem() promises a distinct reason for each malformed shape; only
# 4 of the promised reasons had assertions. These four close the gap.

echo ""
echo "Test 20: leading/trailing hyphens and bad ID tokens get their specific reasons"
REPO="$(new_repo_with_feature_branch)"
printf '# L\n' > "$REPO/docs/internal/architecture/ADR-20260927--leading.md"
printf '# T\n' > "$REPO/docs/internal/architecture/ADR-20260927-trailing-.md"
printf '# N\n' > "$REPO/docs/internal/architecture/ADR-1234567-short-id.md"
printf '# M\n' > "$REPO/docs/internal/architecture/ADR-20260927-.md"
git -C "$REPO" add -A
git -C "$REPO" commit --quiet -m "add hyphen/ID-token malformed ADRs"
OUTPUT=$(REPO_ROOT="$REPO" CHECK_ADR_ID_SCHEME_BASE_REF=main bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "hyphen-token-exit" 1 "$EXIT_CODE"
assert_output_contains "leading-hyphen-reason" "starts with a hyphen" "$OUTPUT"
assert_output_contains "trailing-hyphen-reason" "ends with a hyphen" "$OUTPUT"
assert_output_contains "non-date-token-reason" "neither a grandfathered 3-digit number nor an 8-digit UTC date" "$OUTPUT"
assert_output_contains "missing-slug-reason" "no slug after the 8-digit date" "$OUTPUT"

# --- Test 21 (FIX 5c, pr-test-analyzer) --------------------------------------
#
# The FIRST fail-open tier was never exercised (only tier 2, an
# unresolvable override ref). With no override, no GITHUB_BASE_REF, no
# OMNIPUS_INTEGRATION_BRANCH and not a push event, the guard must print
# the "no comparison base" warning and exit 0 — even with a
# non-conforming ADR sitting on the branch.

echo ""
echo "Test 21: with no base source at all, the guard warns and exits 0"
REPO="$(new_repo_with_feature_branch)"
printf '# Bad\n' > "$REPO/docs/internal/architecture/ADR-097-never-checked.md"
git -C "$REPO" add -A
git -C "$REPO" commit --quiet -m "add ADR that must NOT be checked without a base"
OUTPUT=$(env -u CHECK_ADR_ID_SCHEME_BASE_REF -u GITHUB_BASE_REF -u OMNIPUS_INTEGRATION_BRANCH -u GITHUB_EVENT_NAME REPO_ROOT="$REPO" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "no-base-tier1-exit" 0 "$EXIT_CODE"
assert_output_contains "no-base-tier1-warning" "no comparison base" "$OUTPUT"
assert_output_not_contains "no-base-tier1-no-finding" "never-checked" "$OUTPUT"

# ═══ Generator (scripts/new-adr-id.sh) cases ══════════════════════════════

echo ""
echo "── Generator cases ──"

# --- Test 12: fresh title → exactly one stdout line, valid ID shape ---------

echo ""
echo "Test 12: a fresh title yields one clean stdout line with a valid ID"
GEN_REPO="$TMP_DIR/gen-repo-empty"
mkdir -p "$GEN_REPO/docs/internal/architecture"
git -C "$GEN_REPO" init --quiet --initial-branch=main
GEN_OUT=$(REPO_ROOT="$GEN_REPO" bash "$GEN_SCRIPT" "Web Search Provider Model" 2>/dev/null)
GEN_EXIT=$?
assert_exit_code "gen-fresh-exit" 0 "$GEN_EXIT"
GEN_LINES="$(printf '%s' "$GEN_OUT" | grep -c . || true)"
assert_exit_code "gen-fresh-one-line" 1 "$GEN_LINES"
GEN_ID_RE='^ADR-[0-9]{8}-[a-z0-9]+(-[a-z0-9]+)*$'
if printf '%s' "$GEN_OUT" | grep -Eq "$GEN_ID_RE"; then
  echo "  PASS [gen-fresh-shape]: '$GEN_OUT' matches ADR-<8-digit>-<slug>"
  PASS=$((PASS + 1))
else
  echo "  FAIL [gen-fresh-shape]: '$GEN_OUT' does not match $GEN_ID_RE"
  FAIL=$((FAIL + 1))
  ERRORS+=("gen-fresh-shape: '$GEN_OUT' does not match")
fi
GEN_SLUG12="${GEN_OUT#ADR-}"
GEN_SLUG12="${GEN_SLUG12#*-}"   # strip the 8-digit date token; only the slug must be lowercase
if printf '%s' "$GEN_SLUG12" | grep -Eq '[A-Z_]'; then
  echo "  FAIL [gen-fresh-no-uppercase]: uppercase or underscore survived into the slug"
  FAIL=$((FAIL + 1))
  ERRORS+=("gen-fresh-no-uppercase: slug kept case/underscore")
else
  echo "  PASS [gen-fresh-no-uppercase]: no uppercase/underscore in '$GEN_OUT'"
  PASS=$((PASS + 1))
fi

# --- Test 13: the ADR-095 title collides when the file is visible -----------

echo ""
echo "Test 13: a colliding title is refused, naming the existing file"
GEN_REPO2="$TMP_DIR/gen-repo-095"
mkdir -p "$GEN_REPO2/docs/internal/architecture"
git -C "$GEN_REPO2" init --quiet --initial-branch=main
printf '# ADR-095\n' > "$GEN_REPO2/docs/internal/architecture/ADR-095-thinking-visibility-gate-and-signed-blocks.md"
GEN_ERR=$(REPO_ROOT="$GEN_REPO2" bash "$GEN_SCRIPT" "Thinking Visibility Gate And Signed Blocks" 2>&1 1>/dev/null)
GEN_EXIT2=$?
assert_exit_code "gen-collision-exit-nonzero" 1 "$GEN_EXIT2"
assert_output_contains "gen-collision-names-file" "ADR-095-thinking-visibility-gate-and-signed-blocks.md" "$GEN_ERR"
assert_output_contains "gen-collision-says-collision" "COLLISION" "$GEN_ERR"
GEN_OUT2=$(REPO_ROOT="$GEN_REPO2" bash "$GEN_SCRIPT" "Thinking Visibility Gate And Signed Blocks" 2>/dev/null)
assert_exit_code "gen-collision-stdout-empty" 0 "$([ -z "$GEN_OUT2" ] && echo 0 || echo 1)"

# --- Test 14: all-punctuation title → refused, nothing on stdout ------------

echo ""
echo "Test 14: an all-punctuation title is refused with empty stdout"
GEN_OUT3=$(REPO_ROOT="$GEN_REPO2" bash "$GEN_SCRIPT" "??? !!! ———" 2>/dev/null)
GEN_EXIT3=$?
assert_exit_code "gen-empty-slug-exit-nonzero" 1 "$GEN_EXIT3"
assert_exit_code "gen-empty-slug-stdout-empty" 0 "$([ -z "$GEN_OUT3" ] && echo 0 || echo 1)"

# --- Test 15: truncation cap and trailing-hyphen re-trim ---------------------

echo ""
echo "Test 15: a >80-char slug truncates; truncation-time trailing hyphen is re-trimmed"
LONG_TITLE="$(printf 'a%.0s' $(seq 1 79)) b c"
GEN_OUT4=$(REPO_ROOT="$GEN_REPO2" bash "$GEN_SCRIPT" "$LONG_TITLE" 2>/dev/null)
GEN_EXIT4=$?
assert_exit_code "gen-trunc-exit" 0 "$GEN_EXIT4"
GEN_SLUG="${GEN_OUT4#ADR-}"
GEN_SLUG="${GEN_SLUG#*-}"
assert_exit_code "gen-trunc-len-79" 79 "${#GEN_SLUG}"
case "$GEN_SLUG" in
  *-)
    echo "  FAIL [gen-trunc-no-trailing-hyphen]: slug ends with a hyphen"
    FAIL=$((FAIL + 1))
    ERRORS+=("gen-trunc-no-trailing-hyphen: slug ends with a hyphen")
    ;;
  *)
    echo "  PASS [gen-trunc-no-trailing-hyphen]: slug ends with alnum"
    PASS=$((PASS + 1))
    ;;
esac

# --- Test 22 (FIX 2, silent-failure-hunter MEDIUM) ---------------------------
#
# A ref that RESOLVES via rev-parse but whose ls-tree fails (here: the
# commit's root tree object deleted from loose storage — deterministic and
# offline, verified git 2.50.1: rev-parse rc=0, ls-tree "fatal: not a tree
# object" rc=128). The generator must warn visibly and fall back to the
# working-tree-only collision check, still minting the ID (exit 0).

echo ""
echo "Test 22: a resolvable ref with a failing ls-tree warns and falls back"
CORRUPT_REPO="$TMP_DIR/gen-repo-corrupt-tree"
mkdir -p "$CORRUPT_REPO/docs/internal/architecture"
git -C "$CORRUPT_REPO" init --quiet --initial-branch=main
git -C "$CORRUPT_REPO" config user.email "test@example.invalid"
git -C "$CORRUPT_REPO" config user.name "check-adr-id-scheme test"
printf '# ADR-077\n' > "$CORRUPT_REPO/docs/internal/architecture/ADR-077-two-layers.md"
git -C "$CORRUPT_REPO" add -A
git -C "$CORRUPT_REPO" commit --quiet -m "base"
git -C "$CORRUPT_REPO" update-ref refs/remotes/origin/release/v0.1.1 "$(git -C "$CORRUPT_REPO" rev-parse HEAD)"
CORRUPT_TREE="$(git -C "$CORRUPT_REPO" rev-parse 'refs/remotes/origin/release/v0.1.1^{tree}')"
rm -f "$CORRUPT_REPO/.git/objects/${CORRUPT_TREE:0:2}/${CORRUPT_TREE:2}"
GEN_ERR3=$(REPO_ROOT="$CORRUPT_REPO" bash "$GEN_SCRIPT" "Some Fresh Title" 2>&1 1>/dev/null)
GEN_EXIT5=$?
assert_exit_code "gen-lstree-fail-exit-zero" 0 "$GEN_EXIT5"
assert_output_contains "gen-lstree-fail-warning" "ls-tree against origin/release/v0.1.1 failed" "$GEN_ERR3"
assert_output_contains "gen-lstree-fail-fallback" "fell back to the working tree only" "$GEN_ERR3"
GEN_OUT5=$(REPO_ROOT="$CORRUPT_REPO" bash "$GEN_SCRIPT" "Some Fresh Title" 2>/dev/null)
GEN_EXIT6=$?
assert_exit_code "gen-lstree-fail-still-mints" 0 "$GEN_EXIT6"
assert_output_contains "gen-lstree-fail-still-mints-id" "-some-fresh-title" "$GEN_OUT5"

# ─── Summary ─────────────────────────────────────────────────────────────────

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
