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
#   5. A scanned dir missing                 -> guard exits 2 (refuses green)
#
# Exit: 0 all cases pass; 1 a case failed (prints which).

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GUARD="$SCRIPT_DIR/check-no-hardcoded-e2e-model.sh"
REAL_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

tmp="$(mktemp -d /tmp/e2e-model-guard-test.XXXXXX)"
trap 'rm -rf "$tmp"' EXIT

# make_tree <root>: a minimal synthetic repo with every directory the guard
# existence-checks, so case 5 can delete one and observe exit 2.
make_tree() {
  local root="$1"
  mkdir -p "$root/.github/workflows" "$root/deploy" "$root/scripts" \
           "$root/evals" "$root/pkg" "$root/cmd" "$root/src"
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

# Case 5: a scanned directory missing -> exit 2 (refuse green on a broken tree).
t5="$tmp/broken"
make_tree "$t5"
rm -rf "$t5/evals"
rc=0
REPO_ROOT="$t5" bash "$GUARD" >"$tmp/c5.out" 2>&1 || rc=$?
if [ "$rc" -eq 2 ]; then
  echo "case5 missing-dir-exit2: PASS (guard exited 2, refused a false green)"
else
  echo "case5 missing-dir-exit2: FAIL — want exit 2, got $rc" >&2
  sed 's/^/  /' "$tmp/c5.out" >&2
  fail=1
fi

if [ "$fail" -ne 0 ]; then
  echo "check-no-hardcoded-e2e-model.test: FAILED" >&2
  exit 1
fi
echo "check-no-hardcoded-e2e-model.test: all 5 cases PASS"
exit 0
