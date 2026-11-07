#!/usr/bin/env bash
# check-function-budget-selfcheck.sh
#
# Proves that scripts/check-function-budget.sh can actually fail (the same
# reasoning as scripts/check-no-removed-providers-selfcheck.sh — a guard
# that has never been seen to go red is not a guard;
# docs/internal/false-green-patterns.md records a guard test that passed
# 673/673 with the feature it guarded deleted).
#
# Builds a throwaway fixture tree per case and drives the real gate
# (scripts/check-function-budget.sh --root <fixture> --budget <fixture>)
# against it:
#
#   a. a 241-line unlisted Go function                        -> must FAIL
#   b. a listed Go function one line under its listed number  -> must pass
#   c. the same, one line over its listed number               -> must FAIL
#   d. a 121-line unlisted function                             -> exactly
#      one WARN line, exit 0
#   e. a 241-line PascalCase .tsx function returning JSX        -> a
#      "WARN ... component" line, exit 0 (components never fail)
#   f. the identical 241-line body renamed to a hook (useThing) -> must FAIL
#      (proves the component classifier is load-bearing, not just present)
#   g. a listed function moved to a sibling file in the same package
#      (what a file split does) keeps its entry -> must pass
#   h. the same function moved to another package is not grandfathered
#      -> must FAIL
#   i. a 241-line unlisted .mjs function under scripts/          -> must
#      FAIL, proving the scripts/+tests/ MJS/JS/CJS scope is wired, not
#      just documented
#   j. a 241-line unlisted .cjs function under tests/            -> must
#      FAIL, same proof for the tests/ root and the .cjs extension
#   k. a 241-line .mjs function OUTSIDE scripts/ and tests/ (docs/) -> out
#      of scope entirely: exit 0, no WARN/FAIL, proving the scope decision
#      (not docs/, not spikes/) is enforced, not accidental
#   l. a 317-line anonymous `test.describe` callback in a .spec.ts under
#      tests/e2e -> must FAIL (a real e2e describe of this size once slipped
#      past a tests/-is-JS-only scanner)
#   m. a 241-line unlisted .ts function under tests/                -> must
#      FAIL, the same proof for the plain TypeScript extension
#   n. a 200-code-line Go function padded with 100 comment-only lines
#      (300 physical)                                               -> must
#      pass: comments do not count, and the WARN cites 200
#   o. 241 code lines padded with comment-only lines               -> must
#      FAIL citing 241, not the physical length
#   p. a listed Go function at its code-line pin, with blank and
#      comment padding that puts the physical span over the pin    -> must
#      pass (the pin is code lines)
#   q. a Go function over 240 code lines with //budget:exception
#      and a reason                                                -> must
#      pass, and print EXCEPTION with that reason
#   r. a short Go function with //budget:exception and no reason   -> must
#      FAIL (empty reason), even though it is under 240
#   s. a 200-code-line .ts function padded with 100 comment lines  -> must
#      pass, proving the TS scanner skips comment-only lines
#   t. a .ts function over 240 with `// budget:exception <reason>` -> must
#      pass and be listed (the TS spelling of the directive)
#   u. a React component of 200 code lines plus 100 comment lines,
#      with a directive above it                                   -> exit 0,
#      WARN > 120, no "> 240 component", no EXCEPTION (exceptions
#      do not apply to components; code lines, not physical)
#   v. the gate invoked with a root that does not exist            -> exit 2
#      (the gate itself could not run)
#   Fix-round rows (CHECK findings F1, F3, F4 and the option-value exit):
#   w. a 400-code-line .ts function whose template literal has an
#      interpolation and a tail starting with /*                  -> must FAIL
#      citing 400 (a context-free lexer counted 2 and passed it)
#   x. 399 code lines with the regular expression /[/*]/           -> must FAIL
#      citing 399
#   y. 241 code lines in a .tsx hook whose JSX text lines start with // and
#      /* (text, not comments)                                     -> must FAIL
#      citing 241
#   z. Go: an exception-like line inside a raw string that ends on the line
#      where a 250-line function starts                            -> must FAIL,
#      no EXCEPTION line
#   aa. the same in TS with a template literal                     -> must FAIL
#   ab/ac. a real marker with another // comment line between it and the
#      function (Go, TS)                                           -> must pass
#      and be listed
#   ad/ae. a marker, a blank line, then a 260-line function (Go, TS) -> must FAIL
#   af/ag. a marker, a code line, then the function (Go, TS)        -> must FAIL
#   ah/ai. a marker above an unrelated short function, then a separate
#      260-line function with none (Go, TS): only the short one is
#      listed, the long one must FAIL
#   aj/ak. a real marker above a line where another statement precedes the
#      function (Go, TS)                                           -> must FAIL
#   al. an exception reason that is the single character !         -> must pass,
#      listed with reason ! (Go, TS)
#   am/an. --root or --budget as the last argument, no value        -> exit 2
#   Round-2 rows (CHECK 2):
#   ao. TS: a marker above an outer function, an unmarked helper starting on
#      the same line                                             -> helper FAILs,
#      only the outer is listed (exceptions: 1)
#   ap. TSX: the same above a component                          -> helper FAILs,
#      no EXCEPTION at all (exceptions: 0)
#   aq. exactly 240 code lines (Go, TS)                           -> must pass
#   ar. marked functions of exactly 120 code lines (Go, TS)       -> no WARN
#   as. empty or whitespace-only reason in TS, .mjs and Go        -> must FAIL
#   aw. TS: two callbacks start on the marked line                 -> only the
#      first is listed; the 260-line second one FAILs
#
# Exit: 0 all cases behave, 1 one or more misbehaved, 2 the harness itself
# could not run (missing gate script).

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHECK="$SCRIPT_DIR/check-function-budget.sh"
[ -f "$CHECK" ] || { echo "selfcheck: missing $CHECK" >&2; exit 2; }

TMP="$(mktemp -d "${TMPDIR:-/tmp}/function-budget-selfcheck.XXXXXX")" || exit 2
trap 'rm -rf "$TMP"' EXIT

fresh_tree() {
  rm -rf "$TMP/tree" "$TMP/budget.txt"
  mkdir -p "$TMP/tree/pkg" "$TMP/tree/src/components" "$TMP/tree/scripts" "$TMP/tree/tests" "$TMP/tree/docs"
  printf '# fixture budget\n# file\tname\tlines\n' > "$TMP/budget.txt"
}

# gen_go_func <out> <func-name> <total-lines>: a Go file whose one function
# spans exactly <total-lines> (func keyword line through closing brace).
gen_go_func() {
  local out="$1" name="$2" body=$(( $3 - 2 )) i=0
  { echo "package fixture"; echo; printf 'func %s() {\n' "$name"; } > "$out"
  while [ "$i" -lt "$body" ]; do printf '\t_ = %d\n' "$i" >> "$out"; i=$((i + 1)); done
  echo "}" >> "$out"
}

# gen_tsx_func <out> <func-name> <total-lines>: a .tsx file whose one
# function spans exactly <total-lines> and returns JSX.
gen_tsx_func() {
  local out="$1" name="$2" filler=$(( $3 - 3 )) i=0
  printf 'function %s() {\n' "$name" > "$out"
  while [ "$i" -lt "$filler" ]; do printf '  const v%d = %d;\n' "$i" "$i" >> "$out"; i=$((i + 1)); done
  { echo "  return (<div>ok</div>);"; echo "}"; } >> "$out"
}

# gen_mjs_func <out> <func-name> <total-lines>: a plain .mjs/.js/.cjs file
# (no JSX, never a "component") whose one function spans exactly
# <total-lines>, same shape as gen_go_func.
gen_mjs_func() {
  local out="$1" name="$2" body=$(( $3 - 2 )) i=0
  printf 'function %s() {\n' "$name" > "$out"
  while [ "$i" -lt "$body" ]; do printf '  const v%d = %d;\n' "$i" "$i" >> "$out"; i=$((i + 1)); done
  echo "}" >> "$out"
}

FAIL=0
expect() { # expect <label> <want-exit>
  local label="$1" want="$2" got
  bash "$CHECK" --root "$TMP/tree" --budget "$TMP/budget.txt" >"$TMP/out" 2>&1
  got=$?
  if [ "$got" -ne "$want" ]; then
    echo "selfcheck FAIL: $label — wanted exit $want, got $got" >&2
    sed 's/^/    | /' "$TMP/out" >&2
    FAIL=1
    return 1
  fi
  echo "selfcheck ok:   $label (exit $got)"
  return 0
}

# (a) 241-line unlisted Go function must FAIL, unlisted-and-not-grandfathered form.
fresh_tree
gen_go_func "$TMP/tree/pkg/fixture_a.go" "OverLimitUnlisted" 241
if expect "(a) 241-line unlisted Go function" 1; then
  grep -q "not grandfathered" "$TMP/out" || { echo "selfcheck FAIL: (a) missing 'not grandfathered' FAIL line" >&2; FAIL=1; }
fi

# (b) listed Go function one line under its listed number must pass.
fresh_tree
printf 'pkg/fixture_b.go\tListedFunc\t300\n' >> "$TMP/budget.txt"
gen_go_func "$TMP/tree/pkg/fixture_b.go" "ListedFunc" 299
if expect "(b) listed Go function one line under its number" 0; then
  grep -q '^FAIL' "$TMP/out" && { echo "selfcheck FAIL: (b) unexpected FAIL line" >&2; FAIL=1; }
fi

# (c) the same, one line over its listed number, must FAIL.
fresh_tree
printf 'pkg/fixture_c.go\tListedFunc\t300\n' >> "$TMP/budget.txt"
gen_go_func "$TMP/tree/pkg/fixture_c.go" "ListedFunc" 301
if expect "(c) listed Go function one line over its number" 1; then
  grep -q "> listed 300" "$TMP/out" || { echo "selfcheck FAIL: (c) missing '> listed 300' FAIL line" >&2; FAIL=1; }
fi

# (d) a 121-line unlisted function must yield exactly one WARN and exit 0.
fresh_tree
gen_go_func "$TMP/tree/pkg/fixture_d.go" "JustOverWarn" 121
if expect "(d) 121-line unlisted function, warn only" 0; then
  n_warn="$(grep -c '^WARN' "$TMP/out")"
  [ "$n_warn" -eq 1 ] || { echo "selfcheck FAIL: (d) expected exactly one WARN line, got $n_warn" >&2; FAIL=1; }
  grep -q '^FAIL' "$TMP/out" && { echo "selfcheck FAIL: (d) unexpected FAIL line" >&2; FAIL=1; }
fi

# (e) a 241-line PascalCase .tsx function returning JSX must WARN as a
# component and never fail.
fresh_tree
gen_tsx_func "$TMP/tree/src/components/Fixture.tsx" "FixtureComponent" 241
if expect "(e) 241-line PascalCase component" 0; then
  grep -q '^WARN .* > 240 component$' "$TMP/out" || { echo "selfcheck FAIL: (e) missing 'WARN ... > 240 component' line" >&2; FAIL=1; }
  grep -q '^FAIL' "$TMP/out" && { echo "selfcheck FAIL: (e) unexpected FAIL line" >&2; FAIL=1; }
fi

# (f) the identical 241-line body renamed to a hook must FAIL: lowercase
# "use" fails the uppercase-name test, so the classifier reports "function",
# not "component" — proving the classifier is wired into the gate, not just
# present in the scanner.
fresh_tree
gen_tsx_func "$TMP/tree/src/components/Fixture.tsx" "useThing" 241
if expect "(f) same body renamed to a hook (useThing)" 1; then
  grep -q "not grandfathered" "$TMP/out" || { echo "selfcheck FAIL: (f) missing 'not grandfathered' FAIL line" >&2; FAIL=1; }
  # A row ending in the literal word "component" (the gate's kind marker,
  # `WARN ... > 240 component`) would mean the hook was misclassified. This
  # must be end-anchored: the fixture's own path (src/components/...)
  # contains the substring "component" too.
  grep -Eq ' component$' "$TMP/out" && { echo "selfcheck FAIL: (f) hook was misclassified as a component" >&2; FAIL=1; }
fi

# (g) a listed function that moved to a SIBLING file in the same package
# (what a file split does) keeps its entry: listed under fixture_g_old.go,
# now living in fixture_g_new.go, one line under its number, must pass.
fresh_tree
printf 'pkg/fixture_g_old.go\tMovedFunc\t300\n' >> "$TMP/budget.txt"
gen_go_func "$TMP/tree/pkg/fixture_g_new.go" "MovedFunc" 299
if expect "(g) listed function moved to a sibling file in the same package" 0; then
  grep -q '^FAIL' "$TMP/out" && { echo "selfcheck FAIL: (g) unexpected FAIL line" >&2; FAIL=1; }
fi

# (h) the same function moved to ANOTHER package is not grandfathered.
fresh_tree
mkdir -p "$TMP/tree/pkg/other"
printf 'pkg/fixture_h.go\tMovedFunc\t300\n' >> "$TMP/budget.txt"
gen_go_func "$TMP/tree/pkg/other/fixture_h.go" "MovedFunc" 299
if expect "(h) listed function moved to another package" 1; then
  grep -q "not grandfathered" "$TMP/out" || { echo "selfcheck FAIL: (h) missing 'not grandfathered' FAIL line" >&2; FAIL=1; }
fi

# (i) a 241-line unlisted .mjs function under scripts/ must FAIL.
fresh_tree
gen_mjs_func "$TMP/tree/scripts/fixture_i.mjs" "overLimitMjs" 241
if expect "(i) 241-line unlisted scripts/*.mjs function" 1; then
  grep -q "not grandfathered" "$TMP/out" || { echo "selfcheck FAIL: (i) missing 'not grandfathered' FAIL line" >&2; FAIL=1; }
fi

# (j) a 241-line unlisted .cjs function under tests/ must FAIL.
fresh_tree
gen_mjs_func "$TMP/tree/tests/fixture_j.cjs" "overLimitCjs" 241
if expect "(j) 241-line unlisted tests/*.cjs function" 1; then
  grep -q "not grandfathered" "$TMP/out" || { echo "selfcheck FAIL: (j) missing 'not grandfathered' FAIL line" >&2; FAIL=1; }
fi

# (k) a 241-line .mjs function outside scripts/ and tests/ (docs/) is out of
# scope entirely — ignored, not scanned, not warned, not failed.
fresh_tree
gen_mjs_func "$TMP/tree/docs/fixture_k.mjs" "outOfScopeMjs" 241
if expect "(k) scripts/tests-outside .mjs function is out of scope" 0; then
  grep -qE '^(WARN|FAIL)' "$TMP/out" && { echo "selfcheck FAIL: (k) unexpected WARN/FAIL line for out-of-scope docs/*.mjs" >&2; FAIL=1; }
fi

# gen_describe <out> <total-lines>: a Playwright-style .spec.ts whose one
# anonymous describe callback spans exactly <total-lines> lines.
gen_describe() {
  local out="$1" body=$(( $2 - 2 )) i=0
  printf "import { test } from '@playwright/test';\ntest.describe('big suite', () => {\n" > "$out"
  while [ "$i" -lt "$((body - 1))" ]; do printf '  const v%d = %d;\n' "$i" "$i" >> "$out"; i=$((i + 1)); done
  echo "});" >> "$out"
}

# (l) a 317-line anonymous test.describe callback under tests/e2e must FAIL.
fresh_tree
mkdir -p "$TMP/tree/tests/e2e"
gen_describe "$TMP/tree/tests/e2e/fixture_l.spec.ts" 317
if expect "(l) 317-line unlisted tests/e2e test.describe callback" 1; then
  grep -q "not grandfathered" "$TMP/out" || { echo "selfcheck FAIL: (l) missing 'not grandfathered' FAIL line" >&2; FAIL=1; }
fi

# (m) a 241-line unlisted .ts function under tests/ must FAIL.
fresh_tree
gen_mjs_func "$TMP/tree/tests/fixture_m.ts" "overLimitTs" 241
if expect "(m) 241-line unlisted tests/*.ts function" 1; then
  grep -q "not grandfathered" "$TMP/out" || { echo "selfcheck FAIL: (m) missing 'not grandfathered' FAIL line" >&2; FAIL=1; }
fi

# gen_go_padded <out> <name> <code-lines> <comment-lines> <blank-lines> [directive]
# code-lines includes the func line and the closing brace. Comment and blank
# lines are inside the span. An optional directive is placed on the line
# immediately above the func (outside the span).
gen_go_padded() {
  local out="$1" name="$2" code="$3" comments="$4" blanks="$5" directive="${6:-}"
  local body=$((code - 2)) i=0
  { echo "package fixture"; echo; } > "$out"
  if [ -n "$directive" ]; then printf '%s\n' "$directive" >> "$out"; fi
  printf 'func %s() {\n' "$name" >> "$out"
  while [ "$i" -lt "$body" ]; do printf '\t_ = %d\n' "$i" >> "$out"; i=$((i + 1)); done
  i=0
  while [ "$i" -lt "$comments" ]; do printf '\t// pad %d\n' "$i" >> "$out"; i=$((i + 1)); done
  i=0
  while [ "$i" -lt "$blanks" ]; do printf '\n' >> "$out"; i=$((i + 1)); done
  echo "}" >> "$out"
}

# gen_ts_padded <out> <name> <code-lines> <comment-lines> [directive]
# code-lines includes the function line and the closing brace.
gen_ts_padded() {
  local out="$1" name="$2" code="$3" comments="$4" directive="${5:-}"
  local body=$((code - 2)) i=0
  : > "$out"
  if [ -n "$directive" ]; then printf '%s\n' "$directive" >> "$out"; fi
  printf 'function %s() {\n' "$name" >> "$out"
  while [ "$i" -lt "$body" ]; do printf '  const v%d = %d;\n' "$i" "$i" >> "$out"; i=$((i + 1)); done
  i=0
  while [ "$i" -lt "$comments" ]; do printf '  // pad %d\n' "$i" >> "$out"; i=$((i + 1)); done
  echo "}" >> "$out"
}

# (n) 200 code lines + 100 comment-only lines (300 physical) must not fail.
fresh_tree
gen_go_padded "$TMP/tree/pkg/fixture_n.go" "CommentPadded" 200 100 0
if expect "(n) 200 code lines padded to 300 physical with comments" 0; then
  grep -q '^FAIL' "$TMP/out" && { echo "selfcheck FAIL: (n) unexpected FAIL line" >&2; FAIL=1; }
  grep -q 'CommentPadded 200 > 120' "$TMP/out" || { echo "selfcheck FAIL: (n) WARN did not cite 200 code lines" >&2; FAIL=1; }
fi

# (o) 241 code lines, with comment padding, must FAIL citing 241.
fresh_tree
gen_go_padded "$TMP/tree/pkg/fixture_o.go" "CodeOverLimit" 241 59 0
if expect "(o) 241 code lines fails" 1; then
  grep -q 'CodeOverLimit 241 > 240 (not grandfathered)' "$TMP/out" || { echo "selfcheck FAIL: (o) missing FAIL citing 241 code lines" >&2; FAIL=1; }
fi

# (p) listed at 250 code lines; body is 250 code + 50 comments + 50 blanks
# (350 physical). The physical span is over the pin; the code count is not.
fresh_tree
printf 'pkg/fixture_p.go\tPinnedCode\t250\n' >> "$TMP/budget.txt"
gen_go_padded "$TMP/tree/pkg/fixture_p.go" "PinnedCode" 250 50 50
if expect "(p) pinned function measured in code lines" 0; then
  grep -q '^FAIL' "$TMP/out" && { echo "selfcheck FAIL: (p) unexpected FAIL line" >&2; FAIL=1; }
  grep -q 'PinnedCode 250 > 120' "$TMP/out" || { echo "selfcheck FAIL: (p) WARN did not cite 250 code lines" >&2; FAIL=1; }
fi

# (q) exception with a reason: over 240 code lines, must pass and be listed.
fresh_tree
gen_go_padded "$TMP/tree/pkg/fixture_q.go" "ExceptedGo" 260 0 0 "//budget:exception kept for the replay dispatcher"
if expect "(q) Go exception with a reason" 0; then
  grep -q '^FAIL' "$TMP/out" && { echo "selfcheck FAIL: (q) unexpected FAIL line" >&2; FAIL=1; }
  grep -q '^EXCEPTION .*ExceptedGo 260 kept for the replay dispatcher$' "$TMP/out" || { echo "selfcheck FAIL: (q) missing EXCEPTION line" >&2; FAIL=1; }
  grep -q 'exceptions: 1$' "$TMP/out" || { echo "selfcheck FAIL: (q) summary did not count the exception" >&2; FAIL=1; }
fi

# (r) empty reason on a short function must FAIL even though it is under 240.
fresh_tree
gen_go_padded "$TMP/tree/pkg/fixture_r.go" "EmptyReason" 2 0 0 "//budget:exception"
if expect "(r) empty exception reason fails" 1; then
  grep -q 'EmptyReason 2 empty exception reason' "$TMP/out" || { echo "selfcheck FAIL: (r) missing empty-reason FAIL line" >&2; FAIL=1; }
fi

# (s) TS comment padding: 200 code lines, 100 comment lines, must not fail.
fresh_tree
gen_ts_padded "$TMP/tree/tests/fixture_s.ts" "commentPaddedTs" 200 100
if expect "(s) 200 code lines padded in tests/*.ts" 0; then
  grep -q '^FAIL' "$TMP/out" && { echo "selfcheck FAIL: (s) unexpected FAIL line" >&2; FAIL=1; }
  grep -q 'commentPaddedTs 200 > 120' "$TMP/out" || { echo "selfcheck FAIL: (s) WARN did not cite 200 code lines" >&2; FAIL=1; }
fi

# (t) TS spelling of the directive, over 240, must pass and be listed.
fresh_tree
gen_ts_padded "$TMP/tree/tests/fixture_t.ts" "exceptedTs" 250 0 "// budget:exception kept for the describe callback"
if expect "(t) TS exception with a reason" 0; then
  grep -q '^FAIL' "$TMP/out" && { echo "selfcheck FAIL: (t) unexpected FAIL line" >&2; FAIL=1; }
  grep -q '^EXCEPTION .*exceptedTs 250 kept for the describe callback$' "$TMP/out" || { echo "selfcheck FAIL: (t) missing EXCEPTION line" >&2; FAIL=1; }
fi

# gen_tsx_padded <out> <name> <code-lines> <comment-lines> [directive]
# code-lines includes the function line, the JSX return line and the brace.
gen_tsx_padded() {
  local out="$1" name="$2" code="$3" comments="$4" directive="${5:-}"
  local filler=$((code - 3)) i=0
  : > "$out"
  if [ -n "$directive" ]; then printf '%s\n' "$directive" >> "$out"; fi
  printf 'function %s() {\n' "$name" >> "$out"
  while [ "$i" -lt "$filler" ]; do printf '  const v%d = %d;\n' "$i" "$i" >> "$out"; i=$((i + 1)); done
  i=0
  while [ "$i" -lt "$comments" ]; do printf '  // pad %d\n' "$i" >> "$out"; i=$((i + 1)); done
  { echo "  return (<div>ok</div>);"; echo "}"; } >> "$out"
}

# (u) a component measured in code lines, with a directive, stays warn-only.
# 200 code + 100 comments would be a physical-span component warning; it must
# not be one, and the directive must not be listed as an exception.
fresh_tree
gen_tsx_padded "$TMP/tree/src/components/FixturePad.tsx" "FixturePad" 200 100 "// budget:exception should be ignored on a component"
if expect "(u) component comment padding, directive ignored" 0; then
  grep -q '^FAIL' "$TMP/out" && { echo "selfcheck FAIL: (u) unexpected FAIL line" >&2; FAIL=1; }
  grep -q '^EXCEPTION' "$TMP/out" && { echo "selfcheck FAIL: (u) component was listed as an exception" >&2; FAIL=1; }
  grep -q '> 240 component' "$TMP/out" && { echo "selfcheck FAIL: (u) component warned > 240 on comment padding" >&2; FAIL=1; }
  grep -q 'FixturePad 200 > 120' "$TMP/out" || { echo "selfcheck FAIL: (u) missing WARN citing 200 code lines" >&2; FAIL=1; }
fi

# (v) a root that does not exist is exit 2: the gate could not run.
if bash "$CHECK" --root "$TMP/no-such-root" --budget "$TMP/budget.txt" >"$TMP/out" 2>&1; then
  echo "selfcheck FAIL: (v) missing root exited 0" >&2
  FAIL=1
else
  got=$?
  if [ "$got" -ne 2 ]; then
    echo "selfcheck FAIL: (v) missing root wanted exit 2, got $got" >&2
    sed 's/^/    | /' "$TMP/out" >&2
    FAIL=1
  else
    echo "selfcheck ok:   (v) missing root (exit 2)"
  fi
fi


# body_go <n>: n code lines inside a Go function body. body_ts <n>: the same for TS.
body_go() { local i=0; while [ "$i" -lt "$1" ]; do printf '\t_ = %d\n' "$i"; i=$((i + 1)); done; }
body_ts() { local i=0; while [ "$i" -lt "$1" ]; do printf '  const v%d = %d;\n' "$i" "$i"; i=$((i + 1)); done; }

no_exception_line() { # no_exception_line <label>
  grep -q '^EXCEPTION' "$TMP/out" && { echo "selfcheck FAIL: $1 a lookalike was accepted as an exception" >&2; FAIL=1; }
}

# (w) a 400-code-line function with an interpolated template whose tail
# starts a comment-looking /*: the parser's own tokens, not a context-free
# lexer, must decide what is a comment.
fresh_tree
{ printf 'function templateTail() {\n  const text = `prefix ${1}\n/*`;\n'; body_ts 396; echo '}'; } > "$TMP/tree/tests/fixture_w.ts"
if expect "(w) 400 code lines with an interpolated template tail" 1; then
  grep -q 'templateTail 400 > 240 (not grandfathered)' "$TMP/out" || { echo "selfcheck FAIL: (w) FAIL line did not cite 400 code lines" >&2; FAIL=1; }
fi

# (x) 399 code lines with a regular expression containing /*.
fresh_tree
{ printf 'function regexBody() {\n  const r = /[/*]/;\n'; body_ts 396; echo '}'; } > "$TMP/tree/tests/fixture_x.ts"
if expect "(x) 399 code lines with a /[/*]/ regular expression" 1; then
  grep -q 'regexBody 399 > 240 (not grandfathered)' "$TMP/out" || { echo "selfcheck FAIL: (x) FAIL line did not cite 399 code lines" >&2; FAIL=1; }
fi

# (y) 241 code lines in a hook; two JSX text lines look like comments but are
# text. Counted as comments the function would be 239 and pass.
fresh_tree
{ printf 'function useJsxText() {\n  const el = (<div>\n// jsx text is code\n/* so is this */\n</div>);\n'; body_ts 235; echo '}'; } > "$TMP/tree/src/components/FixtureY.tsx"
if expect "(y) 241 code lines counting JSX text that begins with // and /*" 1; then
  grep -q 'useJsxText 241 > 240 (not grandfathered)' "$TMP/out" || { echo "selfcheck FAIL: (y) FAIL line did not cite 241 code lines" >&2; FAIL=1; }
fi

# (z) Go: a raw string holds an exception-like line and ends on the line where
# the 250-line function starts. There is no comment, so no exception.
fresh_tree
{ printf 'package fixture\n\nvar lookalike = `\n//budget:exception fake string reason\n`; func RawLookalike() {\n'; body_go 248; echo '}'; } > "$TMP/tree/pkg/fixture_z.go"
if expect "(z) Go marker inside a raw string" 1; then
  grep -q 'RawLookalike 250 > 240 (not grandfathered)' "$TMP/out" || { echo "selfcheck FAIL: (z) missing the not-grandfathered FAIL" >&2; FAIL=1; }
  no_exception_line "(z)"
fi

# (aa) TS: the same through a template literal.
fresh_tree
{ printf 'const lookalike = `\n// budget:exception fake string reason\n`; function rawLookalike() {\n'; body_ts 248; echo '}'; } > "$TMP/tree/tests/fixture_aa.ts"
if expect "(aa) TS marker inside a template literal" 1; then
  grep -q 'rawLookalike 250 > 240 (not grandfathered)' "$TMP/out" || { echo "selfcheck FAIL: (aa) missing the not-grandfathered FAIL" >&2; FAIL=1; }
  no_exception_line "(aa)"
fi

# (ab) Go: a real marker, one more comment line, then the function.
fresh_tree
{ printf 'package fixture\n\n//budget:exception doc follows the marker\n// Doc says what the function does.\nfunc BlockGo() {\n'; body_go 258; echo '}'; } > "$TMP/tree/pkg/fixture_ab.go"
if expect "(ab) Go marker with a further comment line before the function" 0; then
  grep -q '^EXCEPTION .*BlockGo 260 doc follows the marker$' "$TMP/out" || { echo "selfcheck FAIL: (ab) missing EXCEPTION line" >&2; FAIL=1; }
fi

# (ac) TS: the same.
fresh_tree
{ printf '// budget:exception doc follows the marker\n// Doc says what the function does.\nfunction blockTs() {\n'; body_ts 258; echo '}'; } > "$TMP/tree/tests/fixture_ac.ts"
if expect "(ac) TS marker with a further comment line before the function" 0; then
  grep -q '^EXCEPTION .*blockTs 260 doc follows the marker$' "$TMP/out" || { echo "selfcheck FAIL: (ac) missing EXCEPTION line" >&2; FAIL=1; }
fi

# (ad) Go: a blank line between the marker and the function ends the link.
fresh_tree
{ printf 'package fixture\n\n//budget:exception detached by a blank line\n\nfunc DetachedGo() {\n'; body_go 258; echo '}'; } > "$TMP/tree/pkg/fixture_ad.go"
if expect "(ad) Go marker separated by a blank line" 1; then
  grep -q 'DetachedGo 260 > 240 (not grandfathered)' "$TMP/out" || { echo "selfcheck FAIL: (ad) missing the not-grandfathered FAIL" >&2; FAIL=1; }
  no_exception_line "(ad)"
fi

# (ae) TS: the same.
fresh_tree
{ printf '// budget:exception detached by a blank line\n\nfunction detachedTs() {\n'; body_ts 258; echo '}'; } > "$TMP/tree/tests/fixture_ae.ts"
if expect "(ae) TS marker separated by a blank line" 1; then
  grep -q 'detachedTs 260 > 240 (not grandfathered)' "$TMP/out" || { echo "selfcheck FAIL: (ae) missing the not-grandfathered FAIL" >&2; FAIL=1; }
  no_exception_line "(ae)"
fi

# (af) Go: a code line sits between the marker and the function.
fresh_tree
{ printf 'package fixture\n\n//budget:exception code intervenes\nvar between = 1\nfunc InterveneGo() {\n'; body_go 258; echo '}'; } > "$TMP/tree/pkg/fixture_af.go"
if expect "(af) Go marker with a code line before the function" 1; then
  grep -q 'InterveneGo 260 > 240 (not grandfathered)' "$TMP/out" || { echo "selfcheck FAIL: (af) missing the not-grandfathered FAIL" >&2; FAIL=1; }
  no_exception_line "(af)"
fi

# (ag) TS: the same.
fresh_tree
{ printf '// budget:exception code intervenes\nconst between = 1;\nfunction interveneTs() {\n'; body_ts 258; echo '}'; } > "$TMP/tree/tests/fixture_ag.ts"
if expect "(ag) TS marker with a code line before the function" 1; then
  grep -q 'interveneTs 260 > 240 (not grandfathered)' "$TMP/out" || { echo "selfcheck FAIL: (ag) missing the not-grandfathered FAIL" >&2; FAIL=1; }
  no_exception_line "(ag)"
fi

# (ah) Go: the marker belongs to a short function; a later long function with
# no marker in the same file must still fail.
fresh_tree
{ printf 'package fixture\n\n//budget:exception only for the short one\nfunc ShortGo() {\n\t_ = 1\n}\n\nfunc LongGo() {\n'; body_go 258; echo '}'; } > "$TMP/tree/pkg/fixture_ah.go"
if expect "(ah) Go marker above another function, long function unmarked" 1; then
  grep -q 'LongGo 260 > 240 (not grandfathered)' "$TMP/out" || { echo "selfcheck FAIL: (ah) the unmarked long function was not failed" >&2; FAIL=1; }
  grep -q '^EXCEPTION .*ShortGo' "$TMP/out" || { echo "selfcheck FAIL: (ah) the marked short function was not listed" >&2; FAIL=1; }
  grep -q 'EXCEPTION .*LongGo' "$TMP/out" && { echo "selfcheck FAIL: (ah) LongGo was listed as an exception" >&2; FAIL=1; }
fi

# (ai) TS: the same.
fresh_tree
{ printf '// budget:exception only for the short one\nfunction shortTs() {\n  return 1;\n}\n\nfunction longTs() {\n'; body_ts 258; echo '}'; } > "$TMP/tree/tests/fixture_ai.ts"
if expect "(ai) TS marker above another function, long function unmarked" 1; then
  grep -q 'longTs 260 > 240 (not grandfathered)' "$TMP/out" || { echo "selfcheck FAIL: (ai) the unmarked long function was not failed" >&2; FAIL=1; }
  grep -q '^EXCEPTION .*shortTs' "$TMP/out" || { echo "selfcheck FAIL: (ai) the marked short function was not listed" >&2; FAIL=1; }
  grep -q 'EXCEPTION .*longTs' "$TMP/out" && { echo "selfcheck FAIL: (ai) longTs was listed as an exception" >&2; FAIL=1; }
fi

# (aj) Go: the marker is directly above a line where another declaration
# comes before the func keyword, so it is not pinned to the function.
fresh_tree
{ printf 'package fixture\n\n//budget:exception belongs to the var\nvar before = 1; func PrefixGo() {\n'; body_go 258; echo '}'; } > "$TMP/tree/pkg/fixture_aj.go"
if expect "(aj) Go marker above a line that starts with another declaration" 1; then
  grep -q 'PrefixGo 260 > 240 (not grandfathered)' "$TMP/out" || { echo "selfcheck FAIL: (aj) missing the not-grandfathered FAIL" >&2; FAIL=1; }
  no_exception_line "(aj)"
fi

# (ak) TS: the same.
fresh_tree
{ printf '// budget:exception belongs to the const\nconst before = 1; function prefixTs() {\n'; body_ts 258; echo '}'; } > "$TMP/tree/tests/fixture_ak.ts"
if expect "(ak) TS marker above a line that starts with another statement" 1; then
  grep -q 'prefixTs 260 > 240 (not grandfathered)' "$TMP/out" || { echo "selfcheck FAIL: (ak) missing the not-grandfathered FAIL" >&2; FAIL=1; }
  no_exception_line "(ak)"
fi

# (al) a reason that is just ! is an ordinary reason, not the empty form.
fresh_tree
{ printf 'package fixture\n\n//budget:exception !\nfunc BangGo() {\n'; body_go 258; echo '}'; } > "$TMP/tree/pkg/fixture_al.go"
if expect "(al) Go exception whose reason is !" 0; then
  grep -q '^EXCEPTION .*BangGo 260 !$' "$TMP/out" || { echo "selfcheck FAIL: (al) missing EXCEPTION line with reason !" >&2; FAIL=1; }
fi
fresh_tree
{ printf '// budget:exception !\nfunction bangTs() {\n'; body_ts 258; echo '}'; } > "$TMP/tree/tests/fixture_al.ts"
if expect "(al) TS exception whose reason is !" 0; then
  grep -q '^EXCEPTION .*bangTs 260 !$' "$TMP/out" || { echo "selfcheck FAIL: (al) missing TS EXCEPTION line with reason !" >&2; FAIL=1; }
fi

# (ao) TS: a marker above an outer function belongs to the outer function only.
# A helper that starts on the same line is a different function and gets nothing.
fresh_tree
{ printf '// budget:exception outer only\nfunction outerTs() { function helperTs() {\n'; body_ts 258; printf '}\n}\n'; } > "$TMP/tree/tests/fixture_ao.ts"
if expect "(ao) TS marker above an outer function, unmarked helper on the same line" 1; then
  grep -q 'helperTs 260 > 240 (not grandfathered)' "$TMP/out" || { echo "selfcheck FAIL: (ao) the nested helper was not failed" >&2; FAIL=1; }
  grep -q '^EXCEPTION .*outerTs ' "$TMP/out" || { echo "selfcheck FAIL: (ao) the marked outer function was not listed" >&2; FAIL=1; }
  grep -q 'EXCEPTION .*helperTs' "$TMP/out" && { echo "selfcheck FAIL: (ao) the helper inherited the marker" >&2; FAIL=1; }
  grep -q 'exceptions: 1$' "$TMP/out" || { echo "selfcheck FAIL: (ao) expected exactly one exception" >&2; FAIL=1; }
fi

# (ap) TSX: a marker above a component does nothing at all, so it cannot reach a
# helper that starts on the same line either.
fresh_tree
{ printf '// budget:exception ignored on the component\nfunction ComponentAp() { function helperTsx() {\n'; body_ts 258; printf '}\n  return (<div>ok</div>);\n}\n'; } > "$TMP/tree/src/components/FixtureAp.tsx"
if expect "(ap) TSX marker above a component, unmarked helper on the same line" 1; then
  grep -q 'helperTsx 260 > 240 (not grandfathered)' "$TMP/out" || { echo "selfcheck FAIL: (ap) the nested helper was not failed" >&2; FAIL=1; }
  grep -q '^EXCEPTION' "$TMP/out" && { echo "selfcheck FAIL: (ap) a component marker produced an exception" >&2; FAIL=1; }
  grep -q 'exceptions: 0$' "$TMP/out" || { echo "selfcheck FAIL: (ap) expected zero exceptions" >&2; FAIL=1; }
fi

# (aq) the fail boundary is exclusive: exactly 240 code lines is legal.
fresh_tree
gen_go_func "$TMP/tree/pkg/fixture_aq.go" "ExactlyLimitGo" 240
gen_mjs_func "$TMP/tree/tests/fixture_aq.ts" "exactlyLimitTs" 240
if expect "(aq) exactly 240 code lines (Go and TS) does not fail" 0; then
  grep -q '^FAIL' "$TMP/out" && { echo "selfcheck FAIL: (aq) a 240-line function failed" >&2; FAIL=1; }
  grep -q 'ExactlyLimitGo 240 > 120' "$TMP/out" || { echo "selfcheck FAIL: (aq) Go WARN did not cite 240" >&2; FAIL=1; }
  grep -q 'exactlyLimitTs 240 > 120' "$TMP/out" || { echo "selfcheck FAIL: (aq) TS WARN did not cite 240" >&2; FAIL=1; }
fi

# (ar) the warn boundary is exclusive: a marked function of exactly 120 code
# lines is listed as an exception and draws no WARN (an unmarked one is filtered
# out by the scanners, so only a marked one can show an inclusive warn).
fresh_tree
gen_go_padded "$TMP/tree/pkg/fixture_ar.go" "MarkedAtWarnGo" 120 0 0 "//budget:exception exactly at the warn line"
gen_ts_padded "$TMP/tree/tests/fixture_ar.ts" "markedAtWarnTs" 120 0 "// budget:exception exactly at the warn line"
if expect "(ar) marked functions of exactly 120 code lines draw no WARN" 0; then
  grep -q '^WARN' "$TMP/out" && { echo "selfcheck FAIL: (ar) a 120-line function drew a WARN" >&2; FAIL=1; }
  grep -q '^EXCEPTION .*MarkedAtWarnGo 120 ' "$TMP/out" || { echo "selfcheck FAIL: (ar) Go exception not listed" >&2; FAIL=1; }
  grep -q '^EXCEPTION .*markedAtWarnTs 120 ' "$TMP/out" || { echo "selfcheck FAIL: (ar) TS exception not listed" >&2; FAIL=1; }
fi

# (as) an empty or whitespace-only reason fails in every language: short TS,
# short .mjs, and a Go reason made only of spaces.
fresh_tree
gen_ts_padded "$TMP/tree/tests/fixture_as.ts" "emptyReasonTs" 2 0 "// budget:exception"
if expect "(as) empty exception reason in TS fails" 1; then
  grep -q 'emptyReasonTs 2 empty exception reason' "$TMP/out" || { echo "selfcheck FAIL: (as) missing TS empty-reason FAIL line" >&2; FAIL=1; }
fi
fresh_tree
gen_mjs_func "$TMP/tree/scripts/fixture_as.mjs" "emptyReasonMjs" 3
sed -i '1i //budget:exception' "$TMP/tree/scripts/fixture_as.mjs"
if expect "(as) empty exception reason in .mjs fails" 1; then
  grep -q 'emptyReasonMjs 3 empty exception reason' "$TMP/out" || { echo "selfcheck FAIL: (as) missing .mjs empty-reason FAIL line" >&2; FAIL=1; }
fi
fresh_tree
gen_go_padded "$TMP/tree/pkg/fixture_as.go" "SpaceReason" 2 0 0 "//budget:exception    "
if expect "(as) whitespace-only exception reason in Go fails" 1; then
  grep -q 'SpaceReason 2 empty exception reason' "$TMP/out" || { echo "selfcheck FAIL: (as) missing whitespace-reason FAIL line" >&2; FAIL=1; }
fi


# (aw) TS: two callbacks start on the line the marker sits above. The marker
# belongs to the first only; the second, 260-line one must still fail.
fresh_tree
{ printf '// budget:exception first callback only\nconst pair = wrap(() => 1, () => {\n'; body_ts 258; printf '});\n'; } > "$TMP/tree/tests/fixture_aw.ts"
if expect "(aw) TS marker above a line that starts two callbacks" 1; then
  grep -q 'wrap) 260 > 240 (not grandfathered)' "$TMP/out" || { echo "selfcheck FAIL: (aw) the second callback was not failed" >&2; FAIL=1; }
  grep -q 'exceptions: 1$' "$TMP/out" || { echo "selfcheck FAIL: (aw) expected exactly one exception" >&2; FAIL=1; }
fi

# (am/an) an option given with no value is a gate that could not run: exit 2.
for opt in --root --budget; do
  bash "$CHECK" "$opt" >"$TMP/out" 2>&1
  got=$?
  if [ "$got" -ne 2 ]; then
    echo "selfcheck FAIL: (am/an) $opt without a value wanted exit 2, got $got" >&2
    sed 's/^/    | /' "$TMP/out" >&2
    FAIL=1
  else
    echo "selfcheck ok:   (am/an) $opt without a value (exit 2)"
  fi
done

if [ "$FAIL" -ne 0 ]; then
  echo "selfcheck: check-function-budget.sh does not behave — see above" >&2
  exit 1
fi
echo "selfcheck: OK (check-function-budget.sh provably fails on offenders and passes clean/grandfathered cases)"
exit 0
