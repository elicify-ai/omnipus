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
#  10. Literal in deploy/fake.sh             -> guard exits 1 (regression:
#                                               the old closed --include list
#                                               caught *.sh; the all-text-file
#                                               scan must keep catching it)
#  11. Literal in evals/x/main.go            -> guard exits 1 (same regression,
#                                               *.go)
#  12. Literal in deploy/foo.toml            -> guard exits 1 (RED on the old
#                                               guard: the closed --include
#                                               list never scanned .toml)
#  13. Unreadable scanned subdir (chmod 000) -> guard exits 2 AND shows the
#                                               grep stderr (RED on the old
#                                               guard: it discarded grep's
#                                               rc/stderr and printed OK on a
#                                               partial scan). Skipped with a
#                                               clear message when running as
#                                               root (chmod 000 cannot stop
#                                               root).
#  14. Unreadable subdir under tests/e2e   -> guard exits 2 AND shows the
#                                               grep stderr — same as 13 but
#                                               on the FILTERED scan (the
#                                               e2e-model.json exclusion).
#                                               RED on the c943cc1d9 guard:
#                                               its filtered branch ran
#                                               `grep ... | grep -vE` under
#                                               pipefail, whose rc is the
#                                               RIGHTMOST non-zero — `grep -v`
#                                               exiting 1 on empty input
#                                               superseded the scan grep's
#                                               rc=2 and the guard printed OK
#                                               on a partial scan (finding
#                                               G2b, 2026-09-27). Skipped as
#                                               root like case 13.
#  15. MiniMax-M3.1-Flash in .github/workflows/x.yml -> exit 1, file named
#                                               (design.md §4.5 case 15).
#  16. minimax-coding-plan in deploy/foo.sh    -> exit 1, file named
#                                               (design.md §4.5 case 16).
#  17. Both new ids only in e2e-model.json     -> exit 0 (allowance; the
#                                               new shape, design.md §4.5
#                                               case 17).
#  18. DeepSeek id in a tests/e2e comment      -> exit 1, file named. The
#                                               fallback id stays banned
#                                               (design.md §4.5 case 18).
#  19. e2e-model.json in the old {"model":...}
#      shape                                  -> exit 2 (design.md §4.5
#                                               case 19).
#  20. e2e-model.json missing .fallback        -> exit 2 (design.md §4.5
#                                               case 20).
#  21. MiniMax-M3.1-Flash-Preview under evals/ -> exit 1, file named. The
#                                               banned pattern is the
#                                               MiniMax-M3.1-Flash prefix
#                                               on purpose (design.md §4.5
#                                               case 21).
#  22. MiniMax id inside evals/x_test.go        -> exit 0. A Go unit test is
#                                               test data, not the CI model
#                                               choice (chief decision: the
#                                               guard skips *_test.go).
#  23. DeepSeek id inside tests/e2e/fixtures/
#      x.test.ts                                 -> exit 0. Same exemption
#                                               for fixture unit tests.
#  24. Those ids in tests/e2e/foo.spec.ts and
#      .github/workflows/x.yml                   -> exit 1, both files named.
#                                               Playwright specs and workflows
#                                               stay banned.
#  25. MiniMax id in deploy/foo_test.sh          -> exit 1, file named. A
#                                               test-like name outside evals/
#                                               and tests/e2e/ is not exempt.
#
# Exit: 0 all cases pass; 1 a case failed (prints which).

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# GUARD — the guard under test. E2E_GUARD lets a caller point the companion
# at a different guard build (used to prove new cases red on the pre-fix
# guard before they go green on the new one).
GUARD="${E2E_GUARD:-$SCRIPT_DIR/check-no-hardcoded-e2e-model.sh}"
REAL_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

tmp="$(mktemp -d /tmp/e2e-model-guard-test.XXXXXX)"
trap 'rm -rf "$tmp"' EXIT

# make_tree <root>: a minimal synthetic repo with every directory AND file the
# guard existence-checks (.github/SECRETS.md included), so case 5 can delete
# any one of them and observe exit 2.
# legal_model_json is the one document design.md §4.1 allows. make_tree plants
# it so the shape check (cases 19 and 20) does not turn every older case into
# exit 2 once the guard requires primary and fallback. The DeepSeek id is still
# only in this file, which is the allowance case 8 proves.
legal_model_json() {
  cat <<'EOF'
{
  "primary": { "provider": "minimax-coding-plan", "model": "MiniMax-M3.1-Flash", "api_key_ref": "MINIMAX_API_KEY" },
  "fallback": { "provider": "openrouter", "model": "deepseek/deepseek-v4.1-flash", "api_key_ref": "OPENROUTER_API_KEY" }
}
EOF
}

make_tree() {
  local root="$1"
  mkdir -p "$root/.github/workflows" "$root/deploy" "$root/scripts" \
           "$root/evals" "$root/pkg" "$root/cmd" "$root/src" "$root/tests/e2e"
  : > "$root/.github/SECRETS.md"
  legal_model_json > "$root/tests/e2e/e2e-model.json"
}

fail=0
skipped=0

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

# Case 8: the literals ONLY in tests/e2e/e2e-model.json — the ONE allowed
# location. The planted document is the new primary+fallback shape (design.md
# §4.1). The old {"model":...} shape is no longer allowed — case 19 requires
# exit 2 for it — so this allowance now plants the legal shape, DeepSeek id
# included, and still asserts exit 0. Case 6 proves tests/e2e is scanned.
t8="$tmp/allowed-file"
make_tree "$t8"
legal_model_json > "$t8/tests/e2e/e2e-model.json"
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

# Case 10: a literal in deploy/fake.sh — deploy/ is scanned. Regression case:
# the old guard's closed --include list already caught *.sh; the all-text-file
# scan must keep catching it (guards against a widening that silently narrows).
t10="$tmp/deploy-sh"
make_tree "$t10"
printf '%s\n' 'AGENT_MODEL="z-ai/glm-5.2"' > "$t10/deploy/fake.sh"
if REPO_ROOT="$t10" bash "$GUARD" >"$tmp/c10.out" 2>&1; then
  echo "case10 deploy-sh-literal-fails: FAIL — guard went green on a planted violation" >&2
  sed 's/^/  /' "$tmp/c10.out" >&2
  fail=1
else
  if grep -q "deploy/fake.sh" "$tmp/c10.out"; then
    echo "case10 deploy-sh-literal-fails: PASS (guard named deploy/fake.sh)"
  else
    echo "case10 deploy-sh-literal-fails: FAIL — guard failed but did not name deploy/fake.sh" >&2
    sed 's/^/  /' "$tmp/c10.out" >&2
    fail=1
  fi
fi

# Case 11: a literal in evals/x/main.go — same regression as case 10 for *.go
# inside evals/ (old closed --include list caught *.go; must stay caught).
t11="$tmp/evals-go"
make_tree "$t11"
mkdir -p "$t11/evals/x"
printf '%s\n' 'package main' '' 'const fallbackModel = "openai/gpt-4o"' > "$t11/evals/x/main.go"
if REPO_ROOT="$t11" bash "$GUARD" >"$tmp/c11.out" 2>&1; then
  echo "case11 evals-go-literal-fails: FAIL — guard went green on a planted violation" >&2
  sed 's/^/  /' "$tmp/c11.out" >&2
  fail=1
else
  if grep -q "evals/x/main.go" "$tmp/c11.out"; then
    echo "case11 evals-go-literal-fails: PASS (guard named evals/x/main.go)"
  else
    echo "case11 evals-go-literal-fails: FAIL — guard failed but did not name evals/x/main.go" >&2
    sed 's/^/  /' "$tmp/c11.out" >&2
    fail=1
  fi
fi

# Case 12: a literal in deploy/foo.toml — RED on the old guard: its closed
# --include list had no *.toml term, so an other-extension file in a scanned
# dir went unscanned while the guard still printed OK (finding G1).
t12="$tmp/toml"
make_tree "$t12"
printf '%s\n' 'model = "deepseek/deepseek-v4.1-flash"' > "$t12/deploy/foo.toml"
if REPO_ROOT="$t12" bash "$GUARD" >"$tmp/c12.out" 2>&1; then
  echo "case12 toml-literal-fails: FAIL — guard went green on a planted violation" >&2
  sed 's/^/  /' "$tmp/c12.out" >&2
  fail=1
else
  if grep -q "deploy/foo.toml" "$tmp/c12.out"; then
    echo "case12 toml-literal-fails: PASS (guard named deploy/foo.toml)"
  else
    echo "case12 toml-literal-fails: FAIL — guard failed but did not name deploy/foo.toml" >&2
    sed 's/^/  /' "$tmp/c12.out" >&2
    fail=1
  fi
fi

# Case 13: an unreadable scanned subdir — the guard must refuse a green on a
# PARTIAL scan: grep exits 2, the guard exits 2 and shows the grep stderr
# (finding G2). Skipped when running as root: chmod 000 cannot make a
# directory unreadable to root, so the case would prove nothing there.
t13="$tmp/unreadable"
make_tree "$t13"
mkdir "$t13/deploy/sealed"
chmod 000 "$t13/deploy/sealed"
if [ "$(id -u)" -eq 0 ]; then
  echo "case13 unreadable-subdir-exit2: SKIP (running as root — chmod 000 cannot make a dir unreadable to root)"
  skipped=$((skipped + 1))
else
  rc=0
  REPO_ROOT="$t13" bash "$GUARD" >"$tmp/c13.out" 2>&1 || rc=$?
  if [ "$rc" -eq 2 ] && grep -q "grep exited" "$tmp/c13.out"; then
    echo "case13 unreadable-subdir-exit2: PASS (guard exited 2 and showed the grep stderr)"
  else
    echo "case13 unreadable-subdir-exit2: FAIL — want exit 2 with the grep stderr shown, got $rc" >&2
    sed 's/^/  /' "$tmp/c13.out" >&2
    fail=1
  fi
  chmod 0755 "$t13/deploy/sealed"
fi

# Case 14: an unreadable subdir under tests/e2e/ — the FILTERED scan (the one
# whose exclusion filter pipes through `grep -vE`). RED on the c943cc1d9
# guard: pipefail makes the pipeline's rc the RIGHTMOST non-zero, so
# `grep -vE` exiting 1 on empty input superseded the scan grep's rc=2 and the
# guard reported a clean scan over a tree it had only PARTIALLY read (finding
# G2b, 2026-09-27 — squad-lead review of c943cc1d9). Case 13 cannot see this:
# the deploy//evals//workflows scan is unfiltered, so its rc is read directly.
# Skipped when running as root, like case 13.
t14="$tmp/unreadable-e2e"
make_tree "$t14"
mkdir "$t14/tests/e2e/sealed"
chmod 000 "$t14/tests/e2e/sealed"
if [ "$(id -u)" -eq 0 ]; then
  echo "case14 unreadable-e2e-subdir-exit2: SKIP (running as root — chmod 000 cannot make a dir unreadable to root)"
  skipped=$((skipped + 1))
else
  rc=0
  REPO_ROOT="$t14" bash "$GUARD" >"$tmp/c14.out" 2>&1 || rc=$?
  if [ "$rc" -eq 2 ] && grep -q "grep exited" "$tmp/c14.out"; then
    echo "case14 unreadable-e2e-subdir-exit2: PASS (guard exited 2 and showed the grep stderr)"
  else
    echo "case14 unreadable-e2e-subdir-exit2: FAIL — want exit 2 with the grep stderr shown, got $rc" >&2
    sed 's/^/  /' "$tmp/c14.out" >&2
    fail=1
  fi
  chmod 0755 "$t14/tests/e2e/sealed"
fi

# Case 15: MiniMax-M3.1-Flash planted in a workflow. The current guard's
# PATTERN does not name it, so this case is red until the pattern grows.
# design.md §4.5 (15).
t15="$tmp/minimax-workflow"
make_tree "$t15"
printf '%s\n' 'model: MiniMax-M3.1-Flash' > "$t15/.github/workflows/x.yml"
if REPO_ROOT="$t15" bash "$GUARD" >"$tmp/c15.out" 2>&1; then
  echo "case15 minimax-model-in-workflow-fails: FAIL — guard went green on MiniMax-M3.1-Flash" >&2
  sed 's/^/  /' "$tmp/c15.out" >&2
  fail=1
else
  if grep -q "x.yml" "$tmp/c15.out"; then
    echo "case15 minimax-model-in-workflow-fails: PASS (guard named x.yml)"
  else
    echo "case15 minimax-model-in-workflow-fails: FAIL — guard failed but did not name x.yml" >&2
    sed 's/^/  /' "$tmp/c15.out" >&2
    fail=1
  fi
fi

# Case 16: the provider id minimax-coding-plan planted in deploy/. design.md §4.5 (16).
t16="$tmp/minimax-provider"
make_tree "$t16"
printf '%s\n' 'provider=minimax-coding-plan' > "$t16/deploy/foo.sh"
if REPO_ROOT="$t16" bash "$GUARD" >"$tmp/c16.out" 2>&1; then
  echo "case16 minimax-provider-in-deploy-fails: FAIL — guard went green on minimax-coding-plan" >&2
  sed 's/^/  /' "$tmp/c16.out" >&2
  fail=1
else
  if grep -q "deploy/foo.sh" "$tmp/c16.out"; then
    echo "case16 minimax-provider-in-deploy-fails: PASS (guard named deploy/foo.sh)"
  else
    echo "case16 minimax-provider-in-deploy-fails: FAIL — guard failed but did not name deploy/foo.sh" >&2
    sed 's/^/  /' "$tmp/c16.out" >&2
    fail=1
  fi
fi

# Case 17: both new ids appear only inside the legal json. Allowance: exit 0.
# design.md §4.5 (17). Green on a guard that does not yet ban the ids is
# expected; the teeth are cases 15, 16 and 21.
t17="$tmp/ids-only-in-json"
make_tree "$t17"
legal_model_json > "$t17/tests/e2e/e2e-model.json"
rc=0
REPO_ROOT="$t17" bash "$GUARD" >"$tmp/c17.out" 2>&1 || rc=$?
if [ "$rc" -eq 0 ]; then
  echo "case17 new-ids-only-in-json-passes: PASS (guard exited 0)"
else
  echo "case17 new-ids-only-in-json-passes: FAIL — want exit 0, got $rc" >&2
  sed 's/^/  /' "$tmp/c17.out" >&2
  fail=1
fi

# Case 18: the DeepSeek fallback id in a tests/e2e comment is still banned.
# design.md §4.5 (18).
t18="$tmp/deepseek-comment"
make_tree "$t18"
printf '%s\n' '// judge uses deepseek/deepseek-v4.1-flash' > "$t18/tests/e2e/fallback.spec.ts"
if REPO_ROOT="$t18" bash "$GUARD" >"$tmp/c18.out" 2>&1; then
  echo "case18 deepseek-comment-fails: FAIL — guard went green on the fallback id in a comment" >&2
  sed 's/^/  /' "$tmp/c18.out" >&2
  fail=1
else
  if grep -q "tests/e2e/fallback.spec.ts" "$tmp/c18.out"; then
    echo "case18 deepseek-comment-fails: PASS (guard named tests/e2e/fallback.spec.ts)"
  else
    echo "case18 deepseek-comment-fails: FAIL — guard failed but did not name the file" >&2
    sed 's/^/  /' "$tmp/c18.out" >&2
    fail=1
  fi
fi

# Case 19: the old {"model":...} shape is not a legal source file. Exit 2.
# design.md §4.5 (19). The current guard has no shape check, so it exits 0.
t19="$tmp/old-shape"
make_tree "$t19"
printf '%s\n' '{ "model": "deepseek/deepseek-v4.1-flash" }' > "$t19/tests/e2e/e2e-model.json"
rc=0
REPO_ROOT="$t19" bash "$GUARD" >"$tmp/c19.out" 2>&1 || rc=$?
if [ "$rc" -eq 2 ]; then
  echo "case19 old-shape-exit2: PASS (guard exited 2)"
else
  echo "case19 old-shape-exit2: FAIL — want exit 2, got $rc" >&2
  sed 's/^/  /' "$tmp/c19.out" >&2
  fail=1
fi

# Case 20: a document with primary but no fallback is not legal. Exit 2.
# design.md §4.5 (20).
t20="$tmp/missing-fallback"
make_tree "$t20"
printf '%s\n' '{"primary":{"provider":"minimax-coding-plan","model":"MiniMax-M3.1-Flash","api_key_ref":"MINIMAX_API_KEY"}}' > "$t20/tests/e2e/e2e-model.json"
rc=0
REPO_ROOT="$t20" bash "$GUARD" >"$tmp/c20.out" 2>&1 || rc=$?
if [ "$rc" -eq 2 ]; then
  echo "case20 missing-fallback-exit2: PASS (guard exited 2)"
else
  echo "case20 missing-fallback-exit2: FAIL — want exit 2, got $rc" >&2
  sed 's/^/  /' "$tmp/c20.out" >&2
  fail=1
fi

# Case 21: MiniMax-M3.1-Flash-Preview under evals/ is caught by the
# MiniMax-M3.1-Flash prefix on purpose. design.md §4.5 (21).
t21="$tmp/preview-superset"
make_tree "$t21"
printf '%s\n' 'catalog id MiniMax-M3.1-Flash-Preview' > "$t21/evals/notes.md"
if REPO_ROOT="$t21" bash "$GUARD" >"$tmp/c21.out" 2>&1; then
  echo "case21 preview-superset-fails: FAIL — guard went green on MiniMax-M3.1-Flash-Preview" >&2
  sed 's/^/  /' "$tmp/c21.out" >&2
  fail=1
else
  if grep -q "evals/notes.md" "$tmp/c21.out"; then
    echo "case21 preview-superset-fails: PASS (guard named evals/notes.md)"
  else
    echo "case21 preview-superset-fails: FAIL — guard failed but did not name evals/notes.md" >&2
    sed 's/^/  /' "$tmp/c21.out" >&2
    fail=1
  fi
fi

# Case 22: a Go unit test under evals/ may name the MiniMax id. The current
# guard scans every text file under evals/, so this is red until *_test.go
# is skipped.
t22="$tmp/go-unit"
make_tree "$t22"
printf '%s\n' 'package evals' 'const model = "MiniMax-M3.1-Flash"' > "$t22/evals/x_test.go"
rc=0
REPO_ROOT="$t22" bash "$GUARD" >"$tmp/c22.out" 2>&1 || rc=$?
if [ "$rc" -eq 0 ]; then
  echo "case22 go-unit-test-allowed: PASS (guard exited 0)"
else
  echo "case22 go-unit-test-allowed: FAIL — want exit 0 for evals/x_test.go, got $rc" >&2
  sed 's/^/  /' "$tmp/c22.out" >&2
  fail=1
fi

# Case 23: a vitest file under tests/e2e/fixtures/ may name the DeepSeek id.
t23="$tmp/ts-unit"
make_tree "$t23"
mkdir -p "$t23/tests/e2e/fixtures"
printf '%s\n' 'export const id = "deepseek/deepseek-v4.1-flash"' > "$t23/tests/e2e/fixtures/x.test.ts"
rc=0
REPO_ROOT="$t23" bash "$GUARD" >"$tmp/c23.out" 2>&1 || rc=$?
if [ "$rc" -eq 0 ]; then
  echo "case23 ts-fixture-test-allowed: PASS (guard exited 0)"
else
  echo "case23 ts-fixture-test-allowed: FAIL — want exit 0 for tests/e2e/fixtures/x.test.ts, got $rc" >&2
  sed 's/^/  /' "$tmp/c23.out" >&2
  fail=1
fi

# Case 24: a Playwright spec and a workflow still name the file. The
# exemption is unit-test files only.
t24="$tmp/still-banned"
make_tree "$t24"
printf '%s\n' 'const model = "MiniMax-M3.1-Flash"' > "$t24/tests/e2e/foo.spec.ts"
printf '%s\n' 'model: deepseek/deepseek-v4.1-flash' > "$t24/.github/workflows/x.yml"
if REPO_ROOT="$t24" bash "$GUARD" >"$tmp/c24.out" 2>&1; then
  echo "case24 spec-and-workflow-still-banned: FAIL — guard went green" >&2
  sed 's/^/  /' "$tmp/c24.out" >&2
  fail=1
elif grep -q "tests/e2e/foo.spec.ts" "$tmp/c24.out" && grep -q ".github/workflows/x.yml" "$tmp/c24.out"; then
  echo "case24 spec-and-workflow-still-banned: PASS (guard named both files)"
else
  echo "case24 spec-and-workflow-still-banned: FAIL — want exit 1 naming both files" >&2
  sed 's/^/  /' "$tmp/c24.out" >&2
  fail=1
fi

# Case 25: deploy/foo_test.sh looks like a test and is still banned. The
# skip is evals/*_test.go and tests/e2e/fixtures/*.test.ts, not every
# filename that contains "test".
t25="$tmp/deploy-test-name"
make_tree "$t25"
printf '%s\n' 'model=MiniMax-M3.1-Flash' > "$t25/deploy/foo_test.sh"
if REPO_ROOT="$t25" bash "$GUARD" >"$tmp/c25.out" 2>&1; then
  echo "case25 deploy-test-name-still-banned: FAIL — guard went green on deploy/foo_test.sh" >&2
  sed 's/^/  /' "$tmp/c25.out" >&2
  fail=1
elif grep -q "deploy/foo_test.sh" "$tmp/c25.out"; then
  echo "case25 deploy-test-name-still-banned: PASS (guard named deploy/foo_test.sh)"
else
  echo "case25 deploy-test-name-still-banned: FAIL — guard failed but did not name deploy/foo_test.sh" >&2
  sed 's/^/  /' "$tmp/c25.out" >&2
  fail=1
fi

if [ "$fail" -ne 0 ]; then
  echo "check-no-hardcoded-e2e-model.test: FAILED" >&2
  exit 1
fi
if [ "${skipped:-0}" -gt 0 ]; then
  echo "check-no-hardcoded-e2e-model.test: all 25 cases PASS ($skipped skipped)"
else
  echo "check-no-hardcoded-e2e-model.test: all 25 cases PASS"
fi
exit 0
