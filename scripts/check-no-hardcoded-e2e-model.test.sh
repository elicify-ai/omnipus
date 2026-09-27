#!/usr/bin/env bash
# check-no-hardcoded-e2e-model.test.sh — mutation proof for
# check-no-hardcoded-e2e-model.sh. guards.sh runs this companion BEFORE the
# guard itself; it plants violations in SYNTHETIC repo trees under a temp
# REPO_ROOT override and never touches the real tree (same discipline as
# guards.test.sh).
#
# Cases:
#   1. Clean synthetic tree                  -> guard exits 0
#   2. Old id planted in .github/workflows/  -> guard exits 1, names the file
#   3. Current id planted in deploy/ (JSON)  -> guard exits 1, names the file
#   4. Old id planted in evals/ prose (.md)  -> guard scans *.md inside evals/
#   5. Any existence-checked path missing    -> guard exits 2 (refuses green);
#                                               covers dirs AND the
#                                               .github/SECRETS.md file
#   6. Literal in tests/e2e/foo.spec.ts      -> guard exits 1, names the file
#   7. Literal in a tests/e2e comment        -> guard exits 1 (comments do not
#                                               exempt a scanned surface)
#   8. Literal ONLY in tests/e2e/e2e-model.json -> guard exits 0 (the ONE
#                                               allowed location; allowance
#                                               case — green under the old
#                                               guard too, which never
#                                               scanned tests/e2e; its teeth
#                                               are that case 6 proves
#                                               tests/e2e IS scanned)
#   9. Literal in .github/SECRETS.md         -> guard exits 1, names the file
#
# Exit: 0 all cases pass; 1 a case failed (prints which).

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GUARD="$SCRIPT_DIR/check-no-hardcoded-e2e-model.sh"
REAL_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

tmp="$(mktemp -d /tmp/e2e-model-guard-test.XXXXXX)"
trap 'rm -rf "$tmp"' EXIT

# make_tree <root>: a minimal synthetic repo with every directory AND file the
# guard existence-checks (.github/SECRETS.md included), so case 5 can delete
# any one of them and observe exit 2.
make_tree() {
  local root="$1"
  mkdir -p "$root/.github/workflows" "$root/deploy" "$root/scripts" \
           "$root/evals" "$root/pkg" "$root/cmd" "$root/src" "$root/tests/e2e"
  : > "$root/.github/SECRETS.md"
}

fail=0

# Case 1: clean synthetic tree -> the guard must pass.
t1="$tmp/clean"
make_tree "$t1"
if REPO_ROOT="$t1" bash "$GUARD" >"$tmp/c1.out" 2>&1; then
  echo "case1 clean-tree-passes: PASS"
else
  echo "case1 clean-tree-passes: FAIL — guard output:" >&2
  sed 's/^/  /' "$tmp/c1.out" >&2
  fail=1
fi

# Case 2: an OLD model id planted in a workflow -> guard fails and NAMES it.
t2="$tmp/old-id"
make_tree "$t2"
printf '%s\n' '"model": "z-ai/glm-5.2"' > "$t2/.github/workflows/bad.yml"
if REPO_ROOT="$t2" bash "$GUARD" >"$tmp/c2.out" 2>&1; then
  echo "case2 planted-old-id-fails: FAIL — guard went green on a planted violation" >&2
  sed 's/^/  /' "$tmp/c2.out" >&2
  fail=1
else
  if grep -q "bad.yml" "$tmp/c2.out"; then
    echo "case2 planted-old-id-fails: PASS (guard named the offending file)"
  else
    echo "case2 planted-old-id-fails: FAIL — guard failed but did not name bad.yml" >&2
    sed 's/^/  /' "$tmp/c2.out" >&2
    fail=1
  fi
fi

# Case 3: the CURRENT id planted in deploy/ (JSON) — the new literal must also
# stay single-sourced.
t3="$tmp/current-id"
make_tree "$t3"
printf '%s' '"model": "deepseek/deepseek-v4.1-flash"' > "$t3/deploy/seed.json"
if REPO_ROOT="$t3" bash "$GUARD" >"$tmp/c3.out" 2>&1; then
  echo "case3 planted-current-id-fails: FAIL — guard went green on a planted literal" >&2
  sed 's/^/  /' "$tmp/c3.out" >&2
  fail=1
else
  if grep -q "seed.json" "$tmp/c3.out"; then
    echo "case3 planted-current-id-fails: PASS (guard named the offending file)"
  else
    echo "case3 planted-current-id-fails: FAIL — guard failed but did not name seed.json" >&2
    sed 's/^/  /' "$tmp/c3.out" >&2
    fail=1
  fi
fi

# Case 4: an old id planted in evals/ prose (.md) — the scan includes *.md.
t4="$tmp/md-prose"
make_tree "$t4"
printf '%s\n' 'export AGENT_MODEL=z-ai/glm-5-turbo' > "$t4/evals/notes.md"
if REPO_ROOT="$t4" bash "$GUARD" >"$tmp/c4.out" 2>&1; then
  echo "case4 md-prose-scanned: FAIL — guard went green on a planted literal" >&2
  sed 's/^/  /' "$tmp/c4.out" >&2
  fail=1
else
  if grep -q "evals/notes.md" "$tmp/c4.out" 2>/dev/null; then
    echo "case4 md-prose-scanned: PASS (guard named evals/notes.md)"
  else
    echo "case4 md-prose-scanned: FAIL — guard failed but did not name evals/notes.md" >&2
    sed 's/^/  /' "$tmp/c4.out" >&2
    fail=1
  fi
fi

# Case 5: ANY existence-checked path missing -> exit 2 (refuse green on a
# broken tree). Loops over every path the guard requires, dirs and files.
t5="$tmp/broken"
for victim in .github/workflows deploy evals tests/e2e .github/SECRETS.md; do
  make_tree "$t5"
  rm -rf "$t5/$victim"
  rc=0
  REPO_ROOT="$t5" bash "$GUARD" >"$tmp/c5.out" 2>&1 || rc=$?
  if [ "$rc" -eq 2 ]; then
    echo "case5 missing-path-exit2 [$victim]: PASS (guard exited 2, refused a false green)"
  else
    echo "case5 missing-path-exit2 [$victim]: FAIL — want exit 2, got $rc" >&2
    sed 's/^/  /' "$tmp/c5.out" >&2
    fail=1
  fi
  rm -rf "$t5"
done

# Case 6: a literal in tests/e2e/foo.spec.ts — tests/e2e is now a scanned
# surface (founder ruling 2026-09-27: no hardcoded model ids in tests).
t6="$tmp/e2e-spec"
make_tree "$t6"
printf '%s\n' 'const model = "z-ai/glm-5.2";' > "$t6/tests/e2e/foo.spec.ts"
if REPO_ROOT="$t6" bash "$GUARD" >"$tmp/c6.out" 2>&1; then
  echo "case6 e2e-spec-literal-fails: FAIL — guard went green on a planted violation" >&2
  sed 's/^/  /' "$tmp/c6.out" >&2
  fail=1
else
  if grep -q "tests/e2e/foo.spec.ts" "$tmp/c6.out"; then
    echo "case6 e2e-spec-literal-fails: PASS (guard named tests/e2e/foo.spec.ts)"
  else
    echo "case6 e2e-spec-literal-fails: FAIL — guard failed but did not name the file" >&2
    sed 's/^/  /' "$tmp/c6.out" >&2
    fail=1
  fi
fi

# Case 7: the same literal hidden in a COMMENT inside tests/e2e — a comment is
# still a hardcoded id on a scanned surface.
t7="$tmp/e2e-comment"
make_tree "$t7"
printf '%s\n' '// const fallbackModel = "google/gemini-2.5-flash"; // legacy id, do not use' > "$t7/tests/e2e/comment-only.spec.ts"
if REPO_ROOT="$t7" bash "$GUARD" >"$tmp/c7.out" 2>&1; then
  echo "case7 e2e-comment-literal-fails: FAIL — guard went green on a commented-out id" >&2
  sed 's/^/  /' "$tmp/c7.out" >&2
  fail=1
else
  if grep -q "tests/e2e/comment-only.spec.ts" "$tmp/c7.out"; then
    echo "case7 e2e-comment-literal-fails: PASS (guard named tests/e2e/comment-only.spec.ts)"
  else
    echo "case7 e2e-comment-literal-fails: FAIL — guard failed but did not name the file" >&2
    sed 's/^/  /' "$tmp/c7.out" >&2
    fail=1
  fi
fi

# Case 8: the literal ONLY in tests/e2e/e2e-model.json — the ONE allowed
# location. Allowance case: green under the old guard too (it never scanned
# tests/e2e at all); against the NEW guard it proves the exclusion is exactly
# this file, while case 6 proves tests/e2e is genuinely scanned.
t8="$tmp/allowed-file"
make_tree "$t8"
printf '%s' '{ "model": "deepseek/deepseek-v4.1-flash" }' > "$t8/tests/e2e/e2e-model.json"
rc=0
REPO_ROOT="$t8" bash "$GUARD" >"$tmp/c8.out" 2>&1 || rc=$?
if [ "$rc" -eq 0 ]; then
  echo "case8 allowed-file-only-passes: PASS (guard exited 0 with the literal only in tests/e2e/e2e-model.json)"
else
  echo "case8 allowed-file-only-passes: FAIL — want exit 0, got $rc" >&2
  sed 's/^/  /' "$tmp/c8.out" >&2
  fail=1
fi

# Case 9: a literal in .github/SECRETS.md — the .github/*.md docs surface is
# scanned too.
t9="$tmp/secrets-md"
make_tree "$t9"
printf '%s\n' 'E2E model: z-ai/glm-5.3-flash' > "$t9/.github/SECRETS.md"
if REPO_ROOT="$t9" bash "$GUARD" >"$tmp/c9.out" 2>&1; then
  echo "case9 secrets-md-literal-fails: FAIL — guard went green on a planted violation" >&2
  sed 's/^/  /' "$tmp/c9.out" >&2
  fail=1
else
  if grep -q ".github/SECRETS.md" "$tmp/c9.out"; then
    echo "case9 secrets-md-literal-fails: PASS (guard named .github/SECRETS.md)"
  else
    echo "case9 secrets-md-literal-fails: FAIL — guard failed but did not name SECRETS.md" >&2
    sed 's/^/  /' "$tmp/c9.out" >&2
    fail=1
  fi
fi

if [ "$fail" -ne 0 ]; then
  echo "check-no-hardcoded-e2e-model.test: FAILED" >&2
  exit 1
fi
echo "check-no-hardcoded-e2e-model.test: all 9 cases PASS"
exit 0
