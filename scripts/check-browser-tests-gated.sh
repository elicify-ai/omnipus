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
# F15 fix: brace-depth tracking is done on a STRING/COMMENT-STRIPPED copy of
# each file's source, not the raw text. The original version counted `{`/`}`
# characters anywhere on a line, including inside string literals, rune
# literals, and comments — so a line like `closing := "}"` (one unmatched
# `}` inside a Go string) desynced the running depth counter. Depending on
# which way it desynced, that either truncated the current function's body
# early (silently dropping a later still-in-body marker/gate check) or, more
# dangerously, swallowed one or more SUBSEQUENT test functions' entire bodies
# into the current one — merging a later function's own skipIfNoBrowser(t)
# call into an earlier, ungated function's recorded body and making the
# whole file appear correctly gated (exit 0) when it was not. A minimal
# character-level lexer (stripToCode() below) blanks out the contents of
# `"..."`, `` `...` ``, `'...'`, `//...` and `/*...*/` spans — including
# escape sequences inside interpreted strings/runes and multi-line raw
# strings/block comments — before any brace counting happens, so a brace
# inside any of those spans can never move the depth counter. If a file
# can't be fully tokenized (an unterminated string/comment at EOF) or a
# function's braces never balance back to zero before EOF, the script now
# fails LOUDLY (exit 2, distinct from a real finding) instead of silently
# treating a partial/incorrect scan as "OK".
#
# Exit code: 0 if every real-Chrome test is gated, 1 if any is not, 2 if the
# checker itself could not parse the source (never a silent pass).
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

set +e   # $(...) under `set -e` aborts before $? is readable — a crashed
         # sub-pass would exit non-zero with ZERO output, indistinguishable
         # from a real finding.
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


class ParseError(Exception):
    """Raised when a .go file cannot be tokenized cleanly, or a function's
    braces never balance — the two "silently miscount" failure modes F15
    closes. Callers must treat this as a checker failure (exit 2), never as
    an implicit pass."""


def strip_to_code(text):
    """Return a copy of `text`, same length and same newline positions, with
    the contents of string literals ("...", `...`, '...'), line comments
    (//...), and block comments (/*...*/) replaced by spaces. Braces inside
    any of those spans are therefore invisible to the brace-depth counter
    that runs on the returned text, which is the whole point: a Go source
    string containing a literal '{' or '}' (e.g. `closing := "}"`) must never
    perturb function-body boundary detection.

    Raises ParseError if the file ends while still inside a string/comment
    span (a malformed or truncated file) — the caller must not silently
    brace-count a file this function could not fully tokenize.
    """
    out = []
    i = 0
    n = len(text)
    state = 'code'  # code | line_comment | block_comment | string | raw_string | rune

    while i < n:
        c = text[i]

        if state == 'code':
            if c == '/' and i + 1 < n and text[i + 1] == '/':
                state = 'line_comment'
                out.append('  ')
                i += 2
                continue
            if c == '/' and i + 1 < n and text[i + 1] == '*':
                state = 'block_comment'
                out.append('  ')
                i += 2
                continue
            if c == '"':
                state = 'string'
                out.append(' ')
                i += 1
                continue
            if c == '`':
                state = 'raw_string'
                out.append(' ')
                i += 1
                continue
            if c == "'":
                state = 'rune'
                out.append(' ')
                i += 1
                continue
            out.append(c)
            i += 1
            continue

        if state == 'line_comment':
            if c == '\n':
                state = 'code'
                out.append('\n')
            else:
                out.append(' ')
            i += 1
            continue

        if state == 'block_comment':
            if c == '*' and i + 1 < n and text[i + 1] == '/':
                state = 'code'
                out.append('  ')
                i += 2
                continue
            out.append('\n' if c == '\n' else ' ')
            i += 1
            continue

        if state == 'string':
            if c == '\\' and i + 1 < n:
                # Escape sequence (incl. \" and \\) — consume both chars as
                # non-code so an escaped quote can never end the string early.
                out.append('  ')
                i += 2
                continue
            if c == '"':
                state = 'code'
                out.append(' ')
                i += 1
                continue
            out.append('\n' if c == '\n' else ' ')
            i += 1
            continue

        if state == 'raw_string':
            # Raw strings have no escapes; only a backtick closes them, and
            # they may legitimately span multiple lines.
            if c == '`':
                state = 'code'
                out.append(' ')
                i += 1
                continue
            out.append('\n' if c == '\n' else ' ')
            i += 1
            continue

        if state == 'rune':
            if c == '\\' and i + 1 < n:
                out.append('  ')
                i += 2
                continue
            if c == "'":
                state = 'code'
                out.append(' ')
                i += 1
                continue
            out.append('\n' if c == '\n' else ' ')
            i += 1
            continue

    if state != 'code':
        raise ParseError(
            f"file ends while still inside a {state.replace('_', ' ')} "
            "(unterminated string/comment) — cannot reliably brace-count"
        )

    return ''.join(out)


findings = []
parse_errors = []

# Top-level package only (non-recursive) — these test helpers are
# unexported and never used by pkg/tools/browser's subpackages.
for fpath in sorted(glob.glob(os.path.join(browser_dir, '*_test.go'))):
    fname = os.path.basename(fpath)
    with open(fpath, 'r', encoding='utf-8', errors='replace') as f:
        raw_text = f.read()

    try:
        code_only_text = strip_to_code(raw_text)
    except ParseError as e:
        parse_errors.append(f"{fname}: {e}")
        continue

    lines = raw_text.splitlines(keepends=True)
    code_lines = code_only_text.splitlines(keepends=True)
    if len(lines) != len(code_lines):
        # Defensive: strip_to_code is contracted to preserve every newline
        # position exactly. A mismatch here means that contract broke —
        # treat it as a checker bug, not a silent pass.
        parse_errors.append(
            f"{fname}: internal error — stripped text has {len(code_lines)} lines, "
            f"source has {len(lines)} lines"
        )
        continue

    i = 0
    while i < len(lines):
        m = FUNC_START.match(lines[i])
        if not m:
            i += 1
            continue

        test_name = m.group(1)
        start_line = i + 1
        depth = code_lines[i].count('{') - code_lines[i].count('}')
        body_lines = [lines[i]]
        j = i + 1
        while j < len(lines) and depth > 0:
            body_lines.append(lines[j])
            depth += code_lines[j].count('{') - code_lines[j].count('}')
            j += 1

        if depth != 0:
            # Ran off the end of the file with the function body still
            # "open". Either the source is malformed, or (more likely for
            # this narrow a scanner) a brace-counting edge case wasn't
            # accounted for. Either way, silently accepting a truncated body
            # is exactly the failure mode F15 closes — fail loudly instead.
            parse_errors.append(
                f"{fname}:{start_line}: {test_name}'s braces never balanced "
                f"before EOF (ended at depth {depth}) — cannot reliably "
                "determine this function's body"
            )
            break

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

if parse_errors:
    print("PARSE_ERROR", file=sys.stderr)
    for e in parse_errors:
        print(e, file=sys.stderr)
    sys.exit(3)

for f in findings:
    print(f)
PYEOF
)
PY_EXIT=$?
set -e
if [[ $PY_EXIT -eq 3 ]]; then
  echo "check-browser-tests-gated: ERROR — could not reliably parse one or more test files (this is NOT a lint finding; fix the source or the scanner):" >&2
  echo "" >&2
  exit 2
fi
if [[ $PY_EXIT -ne 0 ]]; then
  echo "check-browser-tests-gated: ERROR — Python sub-pass exited ${PY_EXIT} (the checker itself failed; this is NOT a lint finding)" >&2
  exit 2
fi

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
