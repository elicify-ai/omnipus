#!/usr/bin/env bash
# check-no-tool-error-from-status.sh
#
# Regression guard for issue #617: no SPA component may derive a tool call's
# ERROR state from AssistantUI's `status.type === 'incomplete'`.
#
# Why this is wrong, structurally (not just a style nit): a finished tool
# call ALWAYS carries a truthy `result` once it completes. AssistantUI's
# `toMessagePartStatus` (@assistant-ui/core, runtime/api/message-runtime.ts)
# short-circuits any tool-call part with a truthy result to `{type:
# 'complete'}` — it NEVER reports `incomplete` for a call that actually ran
# and returned something, success or failure. `incomplete` is reachable only
# for a RESULTLESS part, and only then because it inherits the ENCLOSING
# MESSAGE's status (turn died / was cancelled) — never the individual tool
# call's own outcome. So `isError = status.type === 'incomplete'` can never
# be true for a real tool failure, and is only ever (accidentally) true for
# an unrelated resultless/aborted case. This exact shape was independently
# introduced in at least two component families before being fixed (issue
# #617) — hence a durable guard, not just a one-time fix.
#
# The correct source of truth is the tool call's own real status: either the
# `isError` field AssistantUI threads through on the tool-call message part
# (set in src/lib/omnipus-runtime.ts from the store's resolved
# ToolCall.status), or — for components that read the store directly (e.g.
# FileWriteConfirm.tsx's FileOpRow) — the store's `ToolCall.status === 'error'`
# itself.
#
# What this script flags: any line under src/ (outside src/lib/api/generated/,
# and outside `//` comments) where an identifier named `isError` is assigned
# or passed as a prop directly from `<something>.type === 'incomplete'`
# (optionally wrapped in parens), with nothing else combined into the
# expression via `||`.
#
# What this script deliberately does NOT flag (both are legitimate,
# pre-existing patterns — see the issue's investigation notes):
#   - `status.type === 'incomplete' && status.reason === 'cancelled'` (the
#     isCancelledStatus helper, src/lib/toolStatusConfig.tsx) — this checks a
#     SPECIFIC reason (cancellation), not "any incomplete", and is not
#     assigned to `isError`.
#   - `const isError = status.type === 'incomplete' || !!error`
#     (GenericToolCall.tsx) — the `|| !!error` disjunct means this component
#     already reads the real signal (a store-sourced `error` string); the
#     `status.type === 'incomplete'` half only widens coverage to the
#     legitimate resultless/aborted case. A bare `isError = status.type ===
#     'incomplete'` with NOTHING else combined is what's forbidden.
#   - Any occurrence inside a `//` comment (many files document this exact
#     anti-pattern in prose as part of explaining the fix).
#
# Deliberately implemented in python3 (not `grep -P`): this repo's default
# `grep` on macOS dev machines is BSD grep, which has no `-P`/PCRE support —
# a `grep -P` version of this check would silently find nothing locally
# (false green) while still working on GNU-grep CI runners, which is exactly
# the false-signal trap this project's CI-worker notes warn about elsewhere.
# python3 is already a hard dependency of this repo's other check scripts
# (see check-no-handwritten-wire-types.sh, check-browser-tests-gated.sh).
#
# Exit code: 0 if no offenders found, 1 if any found.
#
# Usage:
#   bash scripts/check-no-tool-error-from-status.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="${REPO_ROOT:-$(cd "${SCRIPT_DIR}/.." && pwd)}"
SRC_DIR="${REPO_ROOT}/src"

if [[ ! -d "$SRC_DIR" ]]; then
  echo "check-no-tool-error-from-status: ERROR — directory not found: $SRC_DIR" >&2
  exit 2
fi

python3 - "$SRC_DIR" <<'PYEOF'
import os
import re
import sys

src_dir = sys.argv[1]

# See this script's header comment for the exact shape this matches/excludes.
PATTERN = re.compile(
    r"^\s*(?!//)"
    r".*\bisError\s*[:=]{1,2}\s*\(?\s*\{?\s*\(?"
    r"[A-Za-z_][A-Za-z0-9_.?]*\.type\s*===\s*['\"]incomplete['\"]"
    r"\s*\)?\s*(?!\|\|)[},;]?\s*(//.*)?$"
)

findings = []
for root, dirs, files in os.walk(src_dir):
    # Never flag generated code.
    dirs[:] = [d for d in dirs if os.path.join(root, d) != os.path.join(src_dir, "lib", "api", "generated")]
    if os.sep + os.path.join("lib", "api", "generated") in root + os.sep:
        continue
    for name in files:
        if not (name.endswith(".ts") or name.endswith(".tsx")):
            continue
        path = os.path.join(root, name)
        try:
            with open(path, encoding="utf-8") as f:
                lines = f.readlines()
        except (UnicodeDecodeError, OSError):
            continue
        for i, line in enumerate(lines, start=1):
            if PATTERN.search(line):
                findings.append(f"{os.path.relpath(path, src_dir)}:{i}: {line.strip()}")

if findings:
    print("check-no-tool-error-from-status: FOUND forbidden isError-from-status.type==='incomplete' pattern(s):", file=sys.stderr)
    print("", file=sys.stderr)
    for f in findings:
        print(f"  src/{f}", file=sys.stderr)
    print("", file=sys.stderr)
    print("A finished tool call always carries a truthy result, so AssistantUI's", file=sys.stderr)
    print("part status is 'complete' — never 'incomplete' — for a real failure.", file=sys.stderr)
    print("Read the real outcome instead:", file=sys.stderr)
    print("  - the tool-call part's own 'isError' field (threaded through by", file=sys.stderr)
    print("    src/lib/omnipus-runtime.ts from the store's resolved ToolCall.status), or", file=sys.stderr)
    print("  - the chat store's ToolCall.status === 'error' directly (see", file=sys.stderr)
    print("    FileWriteConfirm.tsx's FileOpRow for the store-hook pattern).", file=sys.stderr)
    print("", file=sys.stderr)
    print("See issue #617 and this script's own header comment for the full rationale.", file=sys.stderr)
    sys.exit(1)

print("check-no-tool-error-from-status: OK — no forbidden isError-from-status patterns found.")
sys.exit(0)
PYEOF
