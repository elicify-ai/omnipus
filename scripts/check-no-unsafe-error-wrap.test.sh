#!/usr/bin/env bash
# check-no-unsafe-error-wrap.test.sh
#
# Self-test for check-no-unsafe-error-wrap.sh. A guard that cannot fail is
# no guard (docs/internal/false-green-patterns.md) — this builds a scratch
# fixture tree under a temp directory, points the lint script at it via
# --root, and NEVER touches the real repo tree.
#
# Covers the two defect classes that defined this lane, plus the surrounding
# false-green traps:
#
#   1. Class 2 — boundedBody-style unconditional wrap of a Read error
#      (no `if err != nil`) — CAUGHT.
#   2. Class 1 — armReader-style wrap of a Read error INSIDE `if err != nil`
#      (io.EOF must stay bare even when non-nil) — CAUGHT.
#   3. `return fmt.Errorf("...: %w", f())` wrapping a call that may return
#      nil — CAUGHT.
#   4. Wrap of the sentinel selector io.EOF itself — CAUGHT.
#   5. A CORRECT wrap inside `if err != nil` in a non-io function — NOT
#      caught.
#   6. A CORRECT Read that returns the inner error bare — NOT caught.
#   7. Inverted guard (`if err == nil { return nil }; return fmt.Errorf("%w", err)`)
#      — NOT caught (err is proven non-nil).
#   8. A wrap helper that re-wraps a named error — NOT caught.
#   9. A planted os.IsNotExist call (does not unwrap) — CAUGHT.
#  10. A clean tree — exits 0.
#  11. A tree missing pkg/ — exits 2, never a silent green.
#
# Exit code: 0 if all assertions pass, 1 if any assertion fails.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LINT_SCRIPT="${SCRIPT_DIR}/check-no-unsafe-error-wrap.sh"

PASS=0
FAIL=0
ERRORS=()

TMP_DIR=$(mktemp -d /tmp/unsafe-error-wrap-lint-test.XXXXXX)
trap 'rm -rf "$TMP_DIR"' EXIT

setup_skeleton() {
  rm -rf "${TMP_DIR:?}"
  mkdir -p "$TMP_DIR"/{pkg,cmd}
}

setup_fixture() {
  local subpath="$1"
  local content="$2"
  local fpath="${TMP_DIR}/${subpath}"
  mkdir -p "$(dirname "$fpath")"
  printf '%s\n' "$content" > "$fpath"
}

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

echo "=== check-no-unsafe-error-wrap self-test ==="
echo ""

# --- Test 1: class 2 — unconditional wrap in Read ---

echo "Test 1: boundedBody-style unconditional wrap in Read is caught"
setup_skeleton
setup_fixture "pkg/fixture1.go" '
package fixture

import (
	"fmt"
	"io"
)

type boundedBody struct{ io.ReadCloser }

func (b *boundedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	return n, fmt.Errorf("boundedBody.Read: %w", err)
}
'
OUTPUT=$(bash "$LINT_SCRIPT" --root "$TMP_DIR" 2>&1)
EXIT_CODE=$?
assert_exit_code "class2-unconditional-read-exit" 1 "$EXIT_CODE"
assert_output_contains "class2-unconditional-read-file" "fixture1.go" "$OUTPUT"
assert_output_contains "class2-unconditional-read-reason" "io.EOF must be returned bare" "$OUTPUT"

# --- Test 2: class 1 — wrap in Read even inside if err != nil ---

echo ""
echo "Test 2: armReader-style wrap of Read error inside if err != nil is caught"
setup_skeleton
setup_fixture "pkg/fixture2.go" '
package fixture

import (
	"fmt"
	"io"
)

type armReader struct{ io.ReadCloser }

func (r *armReader) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if err != nil {
		return n, fmt.Errorf("armReader.Read: %w", err)
	}
	return n, nil
}
'
OUTPUT=$(bash "$LINT_SCRIPT" --root "$TMP_DIR" 2>&1)
EXIT_CODE=$?
assert_exit_code "class1-eof-wrap-exit" 1 "$EXIT_CODE"
assert_output_contains "class1-eof-wrap-file" "fixture2.go" "$OUTPUT"
assert_output_contains "class1-eof-wrap-reason" "io.EOF must be returned bare" "$OUTPUT"

# --- Test 3: wrap of a call that may return nil ---

echo ""
echo "Test 3: fmt.Errorf(\"...: %w\", f()) is caught"
setup_skeleton
setup_fixture "pkg/fixture3.go" '
package fixture

import (
	"fmt"
	"os"
)

func openConfig(path string) error {
	return fmt.Errorf("openConfig: %w", os.Open(path))
}
'
OUTPUT=$(bash "$LINT_SCRIPT" --root "$TMP_DIR" 2>&1)
EXIT_CODE=$?
assert_exit_code "wrap-call-exit" 1 "$EXIT_CODE"
assert_output_contains "wrap-call-file" "fixture3.go" "$OUTPUT"
assert_output_contains "wrap-call-reason" "wrap of a call that may return nil" "$OUTPUT"

# --- Test 4: wrap of sentinel selector ---

echo ""
echo "Test 4: wrapping io.EOF itself is caught"
setup_skeleton
setup_fixture "pkg/fixture4.go" '
package fixture

import (
	"fmt"
	"io"
)

func asEOF() error {
	return fmt.Errorf("asEOF: %w", io.EOF)
}
'
OUTPUT=$(bash "$LINT_SCRIPT" --root "$TMP_DIR" 2>&1)
EXIT_CODE=$?
assert_exit_code "sentinel-eof-exit" 1 "$EXIT_CODE"
assert_output_contains "sentinel-eof-file" "fixture4.go" "$OUTPUT"
assert_output_contains "sentinel-eof-reason" "io.EOF" "$OUTPUT"

# --- Test 5: correct wrap in a non-io function — NOT caught ---

echo ""
echo "Test 5: a nil-guarded wrap in a non-io function is allowed"
setup_skeleton
setup_fixture "pkg/fixture5.go" '
package fixture

import (
	"fmt"
	"os"
)

func load(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("load: %w", err)
	}
	return f.Close()
}
'
OUTPUT=$(bash "$LINT_SCRIPT" --root "$TMP_DIR" 2>&1)
EXIT_CODE=$?
assert_exit_code "correct-wrap-exit" 0 "$EXIT_CODE"
assert_output_contains "correct-wrap-ok" "OK: no unsafe error wraps" "$OUTPUT"

# --- Test 6: correct Read returning err bare — NOT caught ---

echo ""
echo "Test 6: a Read that returns the inner error bare is allowed"
setup_skeleton
setup_fixture "pkg/fixture6.go" '
package fixture

import "io"

type armReader struct{ io.ReadCloser }

func (r *armReader) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	return n, err
}
'
OUTPUT=$(bash "$LINT_SCRIPT" --root "$TMP_DIR" 2>&1)
EXIT_CODE=$?
assert_exit_code "bare-read-exit" 0 "$EXIT_CODE"
assert_output_contains "bare-read-ok" "OK: no unsafe error wraps" "$OUTPUT"

# --- Test 7: inverted nil guard — NOT caught ---

echo ""
echo "Test 7: inverted if err == nil { return nil } then wrap is allowed"
setup_skeleton
setup_fixture "pkg/fixture7.go" '
package fixture

import (
	"fmt"
	"os"
)

func load(path string) error {
	_, err := os.Open(path)
	if err == nil {
		return nil
	}
	return fmt.Errorf("load: %w", err)
}
'
OUTPUT=$(bash "$LINT_SCRIPT" --root "$TMP_DIR" 2>&1)
EXIT_CODE=$?
assert_exit_code "inverted-guard-exit" 0 "$EXIT_CODE"
assert_output_contains "inverted-guard-ok" "OK: no unsafe error wraps" "$OUTPUT"

# --- Test 8: wrap helper (unguarded ident, not an io method) is allowed ---

echo ""
echo "Test 8: a wrap helper that re-wraps a named error is allowed"
setup_skeleton
setup_fixture "pkg/fixture8.go" '
package fixture

import "fmt"

func wrapFSErr(err error) error {
	return fmt.Errorf("failed to read file: %w", err)
}
'
OUTPUT=$(bash "$LINT_SCRIPT" --root "$TMP_DIR" 2>&1)
EXIT_CODE=$?
assert_exit_code "wrap-helper-exit" 0 "$EXIT_CODE"
assert_output_contains "wrap-helper-ok" "OK: no unsafe error wraps" "$OUTPUT"

# --- Test 9: planted os.IsNotExist — CAUGHT ---

echo ""
echo "Test 9: os.IsNotExist is caught (it does not unwrap)"
setup_skeleton
setup_fixture "pkg/fixture9.go" '
package fixture

import "os"

func missing(path string) bool {
	_, err := os.Stat(path)
	return os.IsNotExist(err)
}
'
OUTPUT=$(bash "$LINT_SCRIPT" --root "$TMP_DIR" 2>&1)
EXIT_CODE=$?
assert_exit_code "legacy-isnotexist-exit" 1 "$EXIT_CODE"
assert_output_contains "legacy-isnotexist-file" "fixture9.go" "$OUTPUT"
assert_output_contains "legacy-isnotexist-reason" "os.IsNotExist" "$OUTPUT"

# --- Test 10: clean tree ---

echo ""
echo "Test 10: a clean tree with no wraps exits 0"
setup_skeleton
setup_fixture "pkg/clean.go" 'package fixture

func nop() {}
'
OUTPUT=$(bash "$LINT_SCRIPT" --root "$TMP_DIR" 2>&1)
EXIT_CODE=$?
assert_exit_code "clean-exit" 0 "$EXIT_CODE"
assert_output_contains "clean-ok" "OK: no unsafe error wraps" "$OUTPUT"

# --- Test 11: missing pkg/ is exit 2, never a silent green ---

echo ""
echo "Test 11: a tree missing pkg/ exits 2"
rm -rf "${TMP_DIR:?}"
mkdir -p "$TMP_DIR/cmd"
OUTPUT=$(bash "$LINT_SCRIPT" --root "$TMP_DIR" 2>&1)
EXIT_CODE=$?
assert_exit_code "missing-pkg-exit" 2 "$EXIT_CODE"
assert_output_contains "missing-pkg-msg" "expected directory 'pkg'" "$OUTPUT"

echo ""
echo "=== results: $PASS passed, $FAIL failed ==="
if [ "$FAIL" -ne 0 ]; then
  for e in "${ERRORS[@]}"; do
    echo "  $e" >&2
  done
  exit 1
fi
exit 0
