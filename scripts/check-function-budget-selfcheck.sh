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
#
# Exit: 0 all six behave, 1 one or more misbehaved, 2 the harness itself
# could not run (missing gate script).

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHECK="$SCRIPT_DIR/check-function-budget.sh"
[ -f "$CHECK" ] || { echo "selfcheck: missing $CHECK" >&2; exit 2; }

TMP="$(mktemp -d "${TMPDIR:-/tmp}/function-budget-selfcheck.XXXXXX")" || exit 2
trap 'rm -rf "$TMP"' EXIT

fresh_tree() {
  rm -rf "$TMP/tree" "$TMP/budget.txt"
  mkdir -p "$TMP/tree/pkg" "$TMP/tree/src/components"
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

if [ "$FAIL" -ne 0 ]; then
  echo "selfcheck: check-function-budget.sh does not behave — see above" >&2
  exit 1
fi
echo "selfcheck: OK (check-function-budget.sh provably fails on offenders and passes clean/grandfathered cases)"
exit 0
