#!/usr/bin/env bash
# check-no-duplicate-renderer.test.sh
#
# Self-test for check-no-duplicate-renderer.sh (ADR-083 Step 6 register,
# test 119's own naming precedent — a guard script without a self-test is
# not a gate, it is a decoration: a typo'd pattern that never matches exits
# 0 forever, which is the 673/673-tests-pass-with-the-feature-deleted
# failure mode this repository has already been bitten by once).
#
# Plants a duplicate definition in a temporary tree, asserts the guard
# script's exit code is non-zero (CAUGHT); removes it, asserts a clean tree
# exits 0 (does not cry wolf on ordinary code).
#
# Exit code: 0 if both assertions pass, 1 if either fails.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GUARD_SCRIPT="${SCRIPT_DIR}/check-no-duplicate-renderer.sh"

PASS=0
FAIL=0

assert_exit_code() {
  local label="$1"
  local expected="$2"
  local actual="$3"
  if [ "$expected" -eq "$actual" ]; then
    PASS=$((PASS + 1))
    echo "  PASS: $label (exit=$actual)"
  else
    FAIL=$((FAIL + 1))
    echo "  FAIL: $label (expected exit=$expected, got exit=$actual)"
  fi
}

TMP_DIR=$(mktemp -d /tmp/check-no-duplicate-renderer-test.XXXXXX)
trap 'rm -rf "$TMP_DIR"' EXIT

mkdir -p "$TMP_DIR/src/components/library/preview"
mkdir -p "$TMP_DIR/src/components/library"

cat > "$TMP_DIR/src/components/library/preview/LibraryAudioPreview.tsx" <<'EOF'
export function LibraryAudioPreview() {
  return null
}
EOF

echo "── Clean-state case (no duplicate) ──"
CLEAN_OUT=$(REPO_ROOT="$TMP_DIR" "$GUARD_SCRIPT" 2>&1)
CLEAN_EXIT=$?
assert_exit_code "clean tree exits 0" 0 "$CLEAN_EXIT"

echo "── Planted-duplicate case ──"
cat > "$TMP_DIR/src/components/library/LibraryPreviewPane.tsx" <<'EOF'
function LibraryAudioPreview() {
  return null
}
EOF
DUP_OUT=$(REPO_ROOT="$TMP_DIR" "$GUARD_SCRIPT" 2>&1)
DUP_EXIT=$?
assert_exit_code "duplicate is caught (non-zero exit)" 1 "$DUP_EXIT"
if ! echo "$DUP_OUT" | grep -q "LibraryPreviewPane.tsx"; then
  FAIL=$((FAIL + 1))
  echo "  FAIL: offending file not named in guard output"
  echo "$DUP_OUT"
else
  PASS=$((PASS + 1))
  echo "  PASS: offending file named in guard output"
fi

echo ""
echo "check-no-duplicate-renderer.test.sh: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ] && exit 0 || exit 1
