#!/usr/bin/env bash
# check-browser-tests-gated.sh
#
# Regression guard for #615: every test in pkg/tools/browser that can trigger
# a real Chrome launch (or the ~100MB Chrome-for-Testing download that
# precedes it) MUST call the package's own skipIfNoBrowser(t) convention
# (pkg/tools/browser/browser_e2e_test.go) somewhere in its body — CI
# environments set CI=1, so skipIfNoBrowser skips before any download/launch
# happens there, and a local dev machine with no working Chromium in PATH
# skips too.
#
# #615 found THREE test functions violating this: 15 that used the weaker/
# wrong `testing.Short()` gate (never set true by either CI surface, so it
# never actually skipped anything, and it doesn't prevent the network
# download either), and TWO with NO gate at all — including one whose own
# doc comment incorrectly claimed it was "fully hermetic" while it actually
# called coord.Register, which unconditionally launches Chrome
# (coordinator.go's ensureLaunched). All were fixed by converting them to
# skipIfNoBrowser(t); this script makes sure a future test doesn't
# reintroduce the same gap.
#
# What it checks: any pkg/tools/browser/*_test.go (top-level package only —
# these helpers are unexported and package-scoped, never used by the
# subpackages) function whose body calls one of the real-Chrome-triggering
# helpers (newCoordinatorTestConfig, resolveTestBinary,
# resolveTestBinaryHeadlessShell) must also call skipIfNoBrowser(t)
# somewhere in that same function body.
#
# This is deliberately a narrow, grep/python-based structural check (matches
# this repo's established pattern — see check-no-handwritten-wire-types.sh),
# not a full Go parse: it tracks brace depth per function body starting at
# `func TestXxx(t *testing.T) {`, which is sufficient for this package's
# actual code shape.
#
# Exit code: 0 if every real-Chrome test is gated, 1 if any is not.
#
# Usage:
#   bash scripts/check-browser-tests-gated.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="${REPO_ROOT:-$(cd "${SCRIPT_DIR}/.." && pwd)}"

BROWSER_DIR="${REPO_ROOT}/pkg/tools/browser"

if [[ ! -d "$BROWSER_DIR" ]]; then
  echo "check-browser-tests-gated: ERROR — directory not found: $BROWSER_DIR" >&2
  exit 2
fi

FINDINGS=$(python3 - "$BROWSER_DIR" <<'PYEOF'
import glob
import os
import re
import sys

browser_dir = sys.argv[1]

FUNC_START = re.compile(r'^func (Test\w+)\(t \*testing\.T\)\s*\{')
REAL_CHROME_MARKERS = (
    'newCoordinatorTestConfig(',
    'resolveTestBinary(',
    'resolveTestBinaryHeadlessShell(',
)
GATE_MARKER = 'skipIfNoBrowser('

findings = []

# Top-level package only (non-recursive) — these test helpers are
# unexported and never used by pkg/tools/browser's subpackages.
for fpath in sorted(glob.glob(os.path.join(browser_dir, '*_test.go'))):
    fname = os.path.basename(fpath)
    with open(fpath, 'r', encoding='utf-8', errors='replace') as f:
        lines = f.readlines()

    i = 0
    while i < len(lines):
        m = FUNC_START.match(lines[i])
        if not m:
            i += 1
            continue

        test_name = m.group(1)
        start_line = i + 1
        depth = lines[i].count('{') - lines[i].count('}')
        body_lines = [lines[i]]
        j = i + 1
        while j < len(lines) and depth > 0:
            body_lines.append(lines[j])
            depth += lines[j].count('{') - lines[j].count('}')
            j += 1
        body = ''.join(body_lines)

        has_marker = any(marker in body for marker in REAL_CHROME_MARKERS)
        has_gate = GATE_MARKER in body

        if has_marker and not has_gate:
            matched = ', '.join(marker.rstrip('(') for marker in REAL_CHROME_MARKERS if marker in body)
            findings.append(
                f"{fname}:{start_line}: {test_name} calls a real-Chrome-triggering helper "
                f"({matched}) but never calls skipIfNoBrowser(t)"
            )

        i = j

for f in findings:
    print(f)
PYEOF
)

if [[ -z "$FINDINGS" ]]; then
  echo "check-browser-tests-gated: OK (every real-Chrome test in pkg/tools/browser is gated by skipIfNoBrowser)"
  exit 0
fi

echo "check-browser-tests-gated: FAIL — real-Chrome test(s) not gated by skipIfNoBrowser:"
echo ""
echo "$FINDINGS" | sed 's/^/  /'
echo ""
echo "Fix: add skipIfNoBrowser(t) as (or near) the first line of the test function body."
echo "See pkg/tools/browser/browser_e2e_test.go's skipIfNoBrowser for the convention."
exit 1
