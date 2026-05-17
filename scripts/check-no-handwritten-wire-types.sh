#!/usr/bin/env bash
# check-no-handwritten-wire-types.sh
#
# Enforces hard-constraint #8 (CLAUDE.md): every wire-format type that crosses
# the gateway/SPA boundary must be defined in the contract specs and generated
# into pkg/api/generated/ (Go) or src/lib/api/generated/ (TypeScript). Hand-
# written wire-format structs and interfaces outside those directories are
# forbidden.
#
# Rules enforced:
#
#   GO: Any package-level struct in pkg/gateway/**/*.go (non-generated,
#       non-test) that has >= 2 fields with `json:` tags is flagged as a
#       hand-written wire-format type.
#
#       Exclusions:
#         - Files under pkg/api/generated/ (generated; never flagged)
#         - Files whose name ends in _test.go
#         - Structs whose `type Foo struct {` line bears `// not-wire-format`
#           (case-insensitive) — opt-out for internal helpers that carry
#           json: tags for non-wire purposes (e.g. logging, config cache).
#
#   TS:  Any `export interface Foo { … }` or `export type Foo = { … }`
#        (object-literal form) in src/lib/api.ts or src/lib/ws.ts is flagged
#        as a hand-written wire-format type.
#
#        Allowed (not flagged):
#          - Re-export type aliases: `export type { X } from '…'`
#          - Anything inside src/lib/api/generated/ (generated)
#          - Any line that bears `// not-wire-format` (case-insensitive)
#
# Exit code: 0 if no offenders found, 1 if any found.
#
# Usage:
#   bash scripts/check-no-handwritten-wire-types.sh
#   bash scripts/check-no-handwritten-wire-types.sh --baseline   # suppress exit 1 (print findings only)
#
# Performance note: uses only grep/awk/python3 — runs in < 5 seconds on full repo.

set -euo pipefail

BASELINE_MODE=0
if [[ "${1:-}" == "--baseline" ]]; then
  BASELINE_MODE=1
fi

# Resolve repo root. The REPO_ROOT env variable overrides the default
# (script parent directory) — used by the self-test to point at a tmp fixture.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="${REPO_ROOT:-$(cd "${SCRIPT_DIR}/.." && pwd)}"

FINDINGS=0
FINDING_LINES=()

# ─── Helper ───────────────────────────────────────────────────────────────────

emit() {
  local file="$1" line="$2" rule="$3" detail="$4"
  FINDING_LINES+=("${file}:${line}: [${rule}] ${detail}")
  FINDINGS=$((FINDINGS + 1))
}

# ─── Rule 1: Go — any package-level struct in pkg/gateway with >= 2 json tags ─
#
# Algorithm (single Python pass for speed):
#   - Skip files under pkg/api/generated/ and *_test.go files
#   - For each .go file under pkg/gateway/
#   - Find lines matching `type <Name> struct {`
#   - That do NOT contain `// not-wire-format` (case-insensitive)
#   - Then scan the following lines until the closing `}` of the struct body
#   - Count fields with `json:"` tag
#   - If count >= 2, emit a finding

GO_OFFENDERS=$(python3 - "$REPO_ROOT" <<'PYEOF'
import re
import os
import sys

repo_root = sys.argv[1] if len(sys.argv) > 1 else '.'
gateway_dir = os.path.join(repo_root, 'pkg', 'gateway')
generated_dir = os.path.join(repo_root, 'pkg', 'api', 'generated')

STRUCT_DEF = re.compile(r'^type\s+(\w+)\s+struct\s*\{')
NOT_WIRE_FORMAT = re.compile(r'//\s*not-wire-format', re.IGNORECASE)
JSON_TAG = re.compile(r'`[^`]*json:"[^"`]')

findings = []

if not os.path.isdir(gateway_dir):
    sys.exit(0)

for dirpath, dirnames, filenames in os.walk(gateway_dir):
    for fname in sorted(filenames):
        if not fname.endswith('.go'):
            continue
        # Skip test files
        if fname.endswith('_test.go'):
            continue
        fpath = os.path.join(dirpath, fname)
        # Skip generated files
        if os.path.commonpath([fpath, generated_dir]) == generated_dir:
            continue

        try:
            with open(fpath, 'r', encoding='utf-8', errors='replace') as f:
                lines = f.readlines()
        except OSError:
            continue

        i = 0
        while i < len(lines):
            line = lines[i]
            m = STRUCT_DEF.search(line)
            if m:
                # Check opt-out marker on same line
                if NOT_WIRE_FORMAT.search(line):
                    i += 1
                    continue

                struct_start_line = i + 1  # 1-indexed
                type_name = m.group(1)

                # Count json-tagged fields in struct body
                depth = 0
                json_count = 0
                j = i
                while j < len(lines):
                    l = lines[j]
                    depth += l.count('{') - l.count('}')
                    if j > i and JSON_TAG.search(l):
                        json_count += 1
                    if depth <= 0 and j > i:
                        break
                    j += 1

                if json_count >= 2:
                    relpath = os.path.relpath(fpath, repo_root)
                    findings.append(f"{relpath}:{struct_start_line}: [go-wire-type] hand-written wire-format struct '{type_name}' ({json_count} json fields) — migrate to contracts/components/schemas/ and regenerate")

            i += 1

for f in findings:
    print(f)
PYEOF
)

# Capture Python exit status explicitly; abort on unexpected failure.
_PY_EXIT=$?
if [[ $_PY_EXIT -ne 0 ]]; then
  echo "check-no-handwritten-wire-types: ERROR — Go Python sub-pass exited ${_PY_EXIT}" >&2
  exit 2
fi

if [[ -n "$GO_OFFENDERS" ]]; then
  while IFS= read -r line; do
    FINDING_LINES+=("$line")
    FINDINGS=$((FINDINGS + 1))
  done <<< "$GO_OFFENDERS"
fi

# ─── Rule 2: TypeScript — export interface or export type = { } in src/lib ───
#
# Flags any `export interface Foo { … }` or `export type Foo = { … }`
# (object-literal body) in src/lib/api.ts and src/lib/ws.ts.
#
# NOT flagged:
#   - Re-exports: `export type { X } from '...'` (no inline body)
#   - Files under src/lib/api/generated/
#   - Lines bearing `// not-wire-format`
#
# Algorithm (single Python pass for accuracy on multi-line type aliases):

TS_OFFENDERS=$(python3 - "$REPO_ROOT" <<'PYEOF'
import re
import os
import sys

repo_root = sys.argv[1] if len(sys.argv) > 1 else '.'
lib_dir = os.path.join(repo_root, 'src', 'lib')
generated_dir = os.path.join(repo_root, 'src', 'lib', 'api', 'generated')

# Matches: export interface FooBar {   or   export interface FooBar extends …
EXPORT_IFACE = re.compile(r'^export\s+interface\s+(\w+)[\s{<]')
# Matches: export type FooBar = {   (object-literal only, not union/primitives)
EXPORT_TYPE_OBJ = re.compile(r'^export\s+type\s+(\w+)\s*=\s*\{')
NOT_WIRE_FORMAT = re.compile(r'//\s*not-wire-format', re.IGNORECASE)

findings = []

# Only check the two designated files
target_files = [
    os.path.join(lib_dir, 'api.ts'),
    os.path.join(lib_dir, 'ws.ts'),
]

for fpath in target_files:
    if not os.path.isfile(fpath):
        continue
    # Skip if somehow inside generated directory
    if os.path.commonpath([fpath, generated_dir]) == generated_dir:
        continue

    try:
        with open(fpath, 'r', encoding='utf-8', errors='replace') as f:
            lines = f.readlines()
    except OSError:
        continue

    for i, line in enumerate(lines):
        # Check opt-out marker
        if NOT_WIRE_FORMAT.search(line):
            continue

        m = EXPORT_IFACE.search(line) or EXPORT_TYPE_OBJ.search(line)
        if m:
            type_name = m.group(1)
            relpath = os.path.relpath(fpath, repo_root)
            findings.append(f"{relpath}:{i+1}: [ts-wire-type] hand-written wire-format type '{type_name}' — migrate to contracts/components/schemas/ and regenerate")

for f in findings:
    print(f)
PYEOF
)

# Capture Python exit status explicitly; abort on unexpected failure.
_PY_EXIT=$?
if [[ $_PY_EXIT -ne 0 ]]; then
  echo "check-no-handwritten-wire-types: ERROR — TS Python sub-pass exited ${_PY_EXIT}" >&2
  exit 2
fi

if [[ -n "$TS_OFFENDERS" ]]; then
  while IFS= read -r line; do
    FINDING_LINES+=("$line")
    FINDINGS=$((FINDINGS + 1))
  done <<< "$TS_OFFENDERS"
fi

# ─── Output ───────────────────────────────────────────────────────────────────

if [[ ${#FINDING_LINES[@]} -eq 0 ]]; then
  echo "check-no-handwritten-wire-types: OK (0 findings)"
  exit 0
fi

echo "check-no-handwritten-wire-types: ${#FINDING_LINES[@]} finding(s)"
echo ""
for line in "${FINDING_LINES[@]}"; do
  echo "  $line"
done
echo ""
echo "To suppress a false positive, add '// not-wire-format' on the same line"
echo "as the type/interface declaration."
echo ""
echo "To fix a real finding:"
echo "  1. Add the type to contracts/components/schemas/<TypeName>.yaml"
echo "  2. Reference it from contracts/openapi.yaml or contracts/asyncapi.yaml"
echo "  3. Run: make gen-contracts"
echo "  4. Commit the regenerated diff alongside the spec change"
echo "  5. Delete the hand-written struct/interface"

if [[ "$BASELINE_MODE" -eq 1 ]]; then
  exit 0
fi
exit 1
