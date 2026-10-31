#!/usr/bin/env bash
# check-e2e-model-reader.test.sh — proof that check-e2e-model-reader.sh can
# go red (the guards.sh companion rule). Builds synthetic trees with a copy of
# the reader, its two test scripts, the golden seed and the provider catalog
# they read, then:
#   1. intact copy                     -> exit 0 (the synthetic tree is valid)
#   2. reader whose env drops a line   -> exit 1 (a contract case fails)
#   3. reader whose --strict is ignored -> exit 1 (a hardening case fails)
#   4. a test script missing           -> exit 2 (refuses a green verdict)
# Exit 0 when every case behaves; 1 otherwise.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
GUARD="$SCRIPT_DIR/check-e2e-model-reader.sh"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/check-e2e-model-reader-test.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT
fail=0

make_tree() {
  local t="$1"
  mkdir -p "$t/scripts" "$t/pkg/config/testdata" "$t/pkg/providers/catalog/data" "$t/tests/e2e"
  cp "$ROOT/scripts/e2e-model.sh" "$ROOT/scripts/e2e-model.test.sh" "$ROOT/scripts/e2e-model-extra.test.sh" "$t/scripts/"
  cp "$ROOT/pkg/config/testdata/ci_two_row_seed.golden.json" "$t/pkg/config/testdata/"
  cp "$ROOT/pkg/providers/catalog/data/providers_catalog.json" "$t/pkg/providers/catalog/data/"
  cp "$ROOT/tests/e2e/e2e-model.json" "$t/tests/e2e/"
}

expect_rc() { # expect_rc <name> <want> <tree>
  local rc=0
  REPO_ROOT="$3" bash "$GUARD" >"$tmp/$1.out" 2>&1 || rc=$?
  if [ "$rc" -eq "$2" ]; then
    echo "$1: PASS (exit $rc)"
  else
    echo "$1: FAIL — want exit $2, got $rc" >&2
    sed 's/^/  /' "$tmp/$1.out" >&2
    fail=1
  fi
}

t1="$tmp/intact"; make_tree "$t1"
expect_rc case1-intact 0 "$t1"

t2="$tmp/env-mutant"; make_tree "$t2"
sed -i.bak "s/^  printf 'E2E_FALLBACK_KEYREF=%s\\\\n' \"\$F_KEYREF\"$/  : # mutant: line dropped/" "$t2/scripts/e2e-model.sh"
rm -f "$t2/scripts/e2e-model.sh.bak" # -i.bak: the one in-place form GNU and BSD (macOS) sed both accept
if cmp -s "$t1/scripts/e2e-model.sh" "$t2/scripts/e2e-model.sh"; then
  echo "case2-env-mutant: FAIL — the mutation did not apply (the reader changed shape)" >&2
  fail=1
else
  expect_rc case2-env-mutant 1 "$t2"
fi

t3="$tmp/strict-mutant"; make_tree "$t3"
sed -i.bak 's/--strict) STRICT=1; shift ;;/--strict) shift ;;/' "$t3/scripts/e2e-model.sh"
rm -f "$t3/scripts/e2e-model.sh.bak"
if cmp -s "$t1/scripts/e2e-model.sh" "$t3/scripts/e2e-model.sh"; then
  echo "case3-strict-mutant: FAIL — the mutation did not apply (the reader changed shape)" >&2
  fail=1
else
  expect_rc case3-strict-mutant 1 "$t3"
fi

t4="$tmp/missing-test"; make_tree "$t4"
rm -f "$t4/scripts/e2e-model-extra.test.sh"
expect_rc case4-missing-test 2 "$t4"

if [ "$fail" -ne 0 ]; then
  echo "check-e2e-model-reader.test: FAILED" >&2
  exit 1
fi
echo "check-e2e-model-reader.test: all 4 cases PASS"
exit 0
