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
#   GO: Any package-level OR function-scoped struct in pkg/gateway/**/*.go
#       (non-generated, non-test) that has >= 2 fields with `json:` tags is
#       flagged as a hand-written wire-format type.
#
#       Also flagged: `var <name> struct { … }` anonymous structs used as
#       request body decoders, when:
#         - the variable has >= 2 json: tags, OR
#         - the variable name is body/req/request/response and has >= 1 json: tag
#           (these are almost always hand-written wire request/response types)
#
#       Exclusions:
#         - Files under pkg/api/generated/ (generated; never flagged)
#         - Files whose name ends in _test.go
#         - Structs whose `type Foo struct {` or `var foo struct {` line bears
#           `// not-wire-format` (case-insensitive) — opt-out for internal
#           helpers that carry json: tags for non-wire purposes (e.g. logging,
#           config cache). The annotation MUST be followed by a justification
#           of at least 40 characters, e.g.:
#             type MyHelper struct { // not-wire-format: decode-only test assertion target, never emitted over WS
#           Annotations with fewer than 40 characters are flagged as
#           under-documented opt-outs ([go-wire-type-justification]).
#
#   TS:  Any `interface Foo { … }` (exported or not) or `type Foo = { … }`
#        (object-literal form, including intersections/unions of object
#        literals) in src/lib/api.ts or src/lib/ws.ts is flagged as a
#        hand-written wire-format type.
#
#        Also flagged: `const _fooSchema = z.object({ … })` — hand-written
#        Zod schemas that should reference the generated schemas.ts instead.
#
#        Allowed (not flagged):
#          - Re-export type aliases: `export type { X } from '…'`
#          - Anything inside src/lib/api/generated/ (generated)
#          - Any line that bears `// not-wire-format` (case-insensitive)
#            with a justification of >= 40 characters after the annotation.
#
#   GO (Rule 4, issue #618): Any Go string literal — raw (`{"error":"x"}`) or
#       interpreted with escaped quotes ("{\"error\":\"x\"}") — that opens
#       with the shape `{"error":"<value>"` in pkg/gateway/, pkg/agent/, or
#       pkg/tools/ (non-generated, non-test) is a hand-built structured-
#       failure wire payload UNLESS <value> is one of the already-governed
#       discriminators in KNOWN_STRUCTURED_FAILURE_DISCRIMINATORS below
#       (kept in lockstep with pkg/tools/result.go's exported *Code
#       constants and pkg/gateway/tool_result_store.go's
#       structuredFailureDiscriminators allow-list). This is the guard that
#       would have caught issue #618's fourth ungoverned member
#       (tool_assembly_duplicate, pkg/agent/loop.go) BEFORE it shipped: that
#       literal opened with `{"error":"tool_assembly_duplicate"` and carried
#       no contract schema, no allow-list entry, and no length budget.
#
#       A discriminator-shaped value (bare identifier, no spaces/format
#       verbs) that is NOT in the known set is flagged — it is either a new
#       structured-failure member that skipped the contract-first process
#       (add a schema + a marshalWithinBudget-routed producer, then add it
#       here), or a typo. Prose "error" fields (e.g.
#       `{"error":"failed to serialize response: %s"}`) do not match the
#       bare-identifier capture at all and are never flagged — they are not
#       discriminators, just ad hoc human-readable fallback text.
#
#       Exclusions: same as Rule 1 (pkg/api/generated/, *_test.go), plus
#       whole-line `//` comments (so documentation quoting a discriminator
#       literal, e.g. this file's own doc comments, is never flagged) and
#       `// not-wire-format` opt-out lines (same >= 40-char justification
#       rule as Rule 1).
#
#       Match shape (round-2 review, F8): the pattern is `\{\s*[^{}]{0,120}?
#       "error"\s*:\s*"<value>"`, not `\{"error":"<value>"` anchored at the
#       very start of the literal. This deliberately catches two shapes a
#       developer reaches for innocently that an earlier, stricter version of
#       this rule missed entirely (both had zero self-test coverage before):
#         - a space (or other whitespace) between the opening brace and the
#           first key, e.g. `{ "error":"new_code", ...}`
#         - "error" NOT the first key in the literal, e.g.
#           `{"message":%q,"error":"new_code"}`
#       The `[^{}]{0,120}?` gap is non-greedy and bounded, and explicitly
#       excludes `{`/`}` so it cannot skip over an unrelated nested literal
#       or jump past the end of a DIFFERENT brace pair to find a coincidental
#       "error" key elsewhere in the file — it only looks INSIDE the same,
#       still-open literal.
#
#       Known remaining gaps, not closed by this change (still real, still
#       worth a future author's attention, but out of scope for the F8 fix):
#       string-variable concatenation (`` `{"error":"` + code + `"}` `` —
#       the value is not a single quoted literal so the capture group can't
#       match it at all) and a literal split across multiple PHYSICAL lines
#       (pretty-printed JSON) — this rule still scans one line at a time, so
#       `{\n  "error": "code"\n}` is invisible to it. Both would require
#       either AST-level analysis or a whole-file (not per-line) scan with
#       comment-stripping and multi-line line-number bookkeeping; deferred
#       as a larger follow-up rather than folded into this fix.
#
#       Rule 1's struct-based scan is DELIBERATELY left scoped to
#       pkg/gateway/ only (not widened to pkg/agent/pkg/tools alongside
#       Rule 4). A structural drift audit of pkg/agent/ + pkg/tools/ package-
#       level structs against Rule 1's json-tag-count heuristic surfaced 77
#       hits — internal hook-RPC/external-CLI-event-parsing/inbound-parsing
#       structs never crossing the gateway/SPA boundary at all, not the
#       fmt.Sprintf-built discriminator literals issue #618 is about. Rule 4
#       targets the ACTUAL defect mechanism (hand-built JSON via string
#       formatting, not typed structs) precisely, with zero tree-wide
#       triage; widening Rule 1's struct scan to those packages is a
#       separate, much larger audit outside this issue's scope.
#
# Exit code: 0 if no offenders found, 1 if any found.
#
# Usage:
#   bash scripts/check-no-handwritten-wire-types.sh
#   bash scripts/check-no-handwritten-wire-types.sh --baseline   # suppress exit 1 (print findings only)
#   bash scripts/check-no-handwritten-wire-types.sh --self-test  # run synthetic pattern self-tests
#
# Performance note: uses only grep/awk/python3 — runs in < 5 seconds on full repo.

set -euo pipefail

BASELINE_MODE=0
SELF_TEST_MODE=0
for arg in "${@}"; do
  case "$arg" in
    --baseline)   BASELINE_MODE=1 ;;
    --self-test)  SELF_TEST_MODE=1 ;;
  esac
done

# ─── Self-test mode ───────────────────────────────────────────────────────────
# Runs synthetic fixture tests against lint patterns.
# Exits 0 if all pass, 1 if any fail.

if [[ "$SELF_TEST_MODE" -eq 1 ]]; then
  echo "=== check-no-handwritten-wire-types --self-test ==="
  echo ""

  TMP_SELF=$(mktemp -d /tmp/wire-lint-selftest.XXXXXX)
  trap 'rm -rf "$TMP_SELF"' EXIT

  SELF_PASS=0
  SELF_FAIL=0
  SELF_ERRORS=()

  _st_setup_go() {
    local subpath="$1" content="$2"
    mkdir -p "${TMP_SELF}/$(dirname "$subpath")"
    printf '%s\n' "$content" > "${TMP_SELF}/${subpath}"
  }
  _st_setup_ts() {
    local subpath="$1" content="$2"
    mkdir -p "${TMP_SELF}/$(dirname "$subpath")"
    printf '%s\n' "$content" > "${TMP_SELF}/${subpath}"
  }
  _st_clear() {
    # Reset all target files to empty so previous fixtures don't bleed through
    _st_setup_go "pkg/gateway/fixture.go" 'package gateway
// empty'
    _st_setup_go "pkg/agent/fixture.go" 'package agent
// empty'
    _st_setup_go "pkg/tools/fixture.go" 'package tools
// empty'
    _st_setup_ts "src/lib/api.ts"  '// empty'
    _st_setup_ts "src/lib/ws.ts"   '// empty'
  }
  _st_assert_contains() {
    local label="$1" needle="$2" haystack="$3"
    if echo "$haystack" | grep -qF "$needle"; then
      echo "  PASS [$label]: output contains '$needle'"
      SELF_PASS=$((SELF_PASS + 1))
    else
      echo "  FAIL [$label]: output does NOT contain '$needle'"
      SELF_FAIL=$((SELF_FAIL + 1))
      SELF_ERRORS+=("[$label] expected output to contain '$needle'")
    fi
  }
  _st_assert_not_contains() {
    local label="$1" needle="$2" haystack="$3"
    if echo "$haystack" | grep -qF "$needle"; then
      echo "  FAIL [$label]: output unexpectedly contains '$needle'"
      SELF_FAIL=$((SELF_FAIL + 1))
      SELF_ERRORS+=("[$label] expected output to NOT contain '$needle'")
    else
      echo "  PASS [$label]: output does not contain '$needle'"
      SELF_PASS=$((SELF_PASS + 1))
    fi
  }
  _st_run() {
    REPO_ROOT="$TMP_SELF" bash "$0" --baseline 2>&1
  }

  # ── ST-1: function-scoped `type X struct` (drop ^ anchor) ─────────────────
  echo "ST-1: function-scoped type X struct is caught (dropped ^ anchor)"
  _st_clear
  _st_setup_go "pkg/gateway/fixture.go" 'package gateway

func myHandler() {
	type backupEntry struct {
		Filename  string `json:"filename"`
		SizeBytes int64  `json:"size_bytes"`
		CreatedAt string `json:"created_at"`
	}
}
'
  OUT=$(_st_run)
  _st_assert_contains "st1-found"       "backupEntry" "$OUT"
  _st_assert_contains "st1-rule"        "go-wire-type" "$OUT"

  # ── ST-2: function-scoped type with opt-out (valid ≥40 chars) → skipped ───
  echo ""
  echo "ST-2: function-scoped type with valid not-wire-format (>=40 chars) is skipped"
  _st_clear
  _st_setup_go "pkg/gateway/fixture.go" 'package gateway

func myHandler() {
	type internalStats struct { // not-wire-format: decode-only local accumulator; never serialised to any HTTP response
		Count int    `json:"count"`
		Name  string `json:"name"`
	}
}
'
  OUT=$(_st_run)
  _st_assert_not_contains "st2-skipped" "internalStats" "$OUT"

  # ── ST-3: var body struct with ≥2 json tags → caught ──────────────────────
  echo ""
  echo "ST-3: var body struct with >=2 json tags is caught"
  _st_clear
  _st_setup_go "pkg/gateway/fixture.go" 'package gateway

func myHandler() {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
}
'
  OUT=$(_st_run)
  _st_assert_contains "st3-found" "body" "$OUT"
  _st_assert_contains "st3-rule"  "go-wire-type" "$OUT"

  # ── ST-4: var req struct with 1 json tag (req name) → caught ──────────────
  echo ""
  echo "ST-4: var req struct with 1 json tag and name 'req' is caught"
  _st_clear
  _st_setup_go "pkg/gateway/fixture.go" 'package gateway

func myHandler() {
	var req struct {
		Token string `json:"token"`
	}
}
'
  OUT=$(_st_run)
  _st_assert_contains "st4-found" "req" "$OUT"
  _st_assert_contains "st4-rule"  "go-wire-type" "$OUT"

  # ── ST-5: var myHelper struct with 1 json tag (non-body name) → NOT caught ─
  echo ""
  echo "ST-5: var myHelper struct with 1 json tag (non-body name) is NOT caught"
  _st_clear
  _st_setup_go "pkg/gateway/fixture.go" 'package gateway

func myHandler() {
	var myHelper struct {
		ID string `json:"id"`
	}
}
'
  OUT=$(_st_run)
  _st_assert_not_contains "st5-skipped" "myHelper" "$OUT"

  # ── ST-6: var body struct with not-wire-format (≥40 chars) → skipped ──────
  echo ""
  echo "ST-6: var body struct with valid not-wire-format (>=40 chars) is skipped"
  _st_clear
  _st_setup_go "pkg/gateway/fixture.go" 'package gateway

func myHandler() {
	var body struct { // not-wire-format: decode-only local accumulator; never serialised to any HTTP response
		Email    string `json:"email"`
		Password string `json:"password"`
	}
}
'
  OUT=$(_st_run)
  _st_assert_not_contains "st6-skipped" "body" "$OUT"

  # ── ST-7: TS non-exported interface → caught ───────────────────────────────
  echo ""
  echo "ST-7: TS non-exported interface is caught"
  _st_clear
  _st_setup_ts "src/lib/api.ts" 'interface RawSession {
  id: string
  agent_id: string
}
'
  OUT=$(_st_run)
  _st_assert_contains "st7-found" "RawSession" "$OUT"
  _st_assert_contains "st7-rule"  "ts-wire-type" "$OUT"

  # ── ST-8: TS non-exported type = {} → caught ──────────────────────────────
  echo ""
  echo "ST-8: TS non-exported type = {} object literal is caught"
  _st_clear
  _st_setup_ts "src/lib/api.ts" 'type AgentToolsResponse = { config: AgentToolsCfg; tools: AgentToolEntry[] }
'
  OUT=$(_st_run)
  _st_assert_contains "st8-found" "AgentToolsResponse" "$OUT"
  _st_assert_contains "st8-rule"  "ts-wire-type" "$OUT"

  # ── ST-9: TS export type = Foo & { … } intersection → caught ─────────────
  echo ""
  echo "ST-9: TS export type intersection with object literal is caught"
  _st_clear
  _st_setup_ts "src/lib/api.ts" 'export type ExtendedAgent = Agent & {
  extraField: string
}
'
  OUT=$(_st_run)
  _st_assert_contains "st9-found" "ExtendedAgent" "$OUT"
  _st_assert_contains "st9-rule"  "ts-wire-type" "$OUT"

  # ── ST-10: TS export type = {} | {} union → caught ────────────────────────
  echo ""
  echo "ST-10: TS export type union of object literals is caught"
  _st_clear
  _st_setup_ts "src/lib/api.ts" 'export type LoginResult = { success: true; token: string } | { success: false; error: string }
'
  OUT=$(_st_run)
  _st_assert_contains "st10-found" "LoginResult" "$OUT"
  _st_assert_contains "st10-rule"  "ts-wire-type" "$OUT"

  # ── ST-11: TS hand-written z.object schema → caught ───────────────────────
  echo ""
  echo "ST-11: TS hand-written z.object schema is caught"
  _st_clear
  _st_setup_ts "src/lib/api.ts" 'const _testProviderSchema = z.object({ success: z.boolean(), error: z.string().optional() }).passthrough()
'
  OUT=$(_st_run)
  _st_assert_contains "st11-found"   "_testProviderSchema" "$OUT"
  _st_assert_contains "st11-rule"    "ts-hand-zod" "$OUT"

  # ── ST-12: TS z.object schema in non-generated non-api/ws .ts file → IS caught ─
  echo ""
  echo "ST-12: TS z.object schema in any src/lib/*.ts file IS caught (widened scope)"
  _st_clear
  _st_setup_ts "src/lib/transforms.ts" 'const _someSchema = z.object({ foo: z.string() })
'
  OUT=$(_st_run)
  _st_assert_contains "st12-found" "_someSchema" "$OUT"
  _st_assert_contains "st12-rule"  "ts-hand-zod" "$OUT"

  # ── ST-13: TS interface with not-wire-format opt-out → skipped (any justification)
  echo ""
  echo "ST-13: TS non-exported interface with not-wire-format is skipped"
  _st_clear
  _st_setup_ts "src/lib/api.ts" 'interface RawSession { // not-wire-format: adapter alias
  id: string
}
'
  OUT=$(_st_run)
  _st_assert_not_contains "st13-skipped" "RawSession" "$OUT"

  # ── ST-15: Go body := struct{}{} composite literal → caught ──────────────
  echo ""
  echo "ST-15: Go body := struct{}{} composite literal with >=2 json tags is caught"
  _st_clear
  _st_setup_go "pkg/gateway/fixture.go" 'package gateway

func myHandler() {
	body := struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}{}
}
'
  OUT=$(_st_run)
  _st_assert_contains "st15-found" "body" "$OUT"
  _st_assert_contains "st15-rule"  "go-wire-type" "$OUT"

  # ── ST-16: TS z.union schema → caught ─────────────────────────────────────
  echo ""
  echo "ST-16: TS z.union schema is caught"
  _st_clear
  _st_setup_ts "src/lib/api.ts" 'const _loginResultSchema = z.union([z.object({ success: z.literal(true) }), z.object({ success: z.literal(false) })])
'
  OUT=$(_st_run)
  _st_assert_contains "st16-found"   "_loginResultSchema" "$OUT"
  _st_assert_contains "st16-rule"    "ts-hand-zod" "$OUT"

  # ── ST-17: TS interface in non-api/ws src/lib/*.ts file → caught ──────────
  echo ""
  echo "ST-17: TS interface in any src/lib/*.ts file (not just api/ws) is caught"
  _st_clear
  _st_setup_ts "src/lib/helpers.ts" 'interface SomeHelper {
  id: string
  name: string
}
'
  OUT=$(_st_run)
  _st_assert_contains "st17-found" "SomeHelper" "$OUT"
  _st_assert_contains "st17-rule"  "ts-wire-type" "$OUT"

  # ── ST-18: Rule 4 — unknown discriminator literal in pkg/agent is caught ──
  echo ""
  echo "ST-18: Go hand-built {\"error\":\"<unknown>\"} literal in pkg/agent is caught"
  _st_clear
  _st_setup_go "pkg/agent/fixture.go" 'package agent

import "fmt"

func f(err error) string {
	return fmt.Sprintf(`{"error":"a_brand_new_fifth_member","message":%q}`, err.Error())
}
'
  OUT=$(_st_run)
  _st_assert_contains "st18-found" "a_brand_new_fifth_member" "$OUT"
  _st_assert_contains "st18-rule"  "go-wire-discriminator" "$OUT"

  # ── ST-19: Rule 4 — known/governed discriminator literal is NOT caught ────
  echo ""
  echo "ST-19: Go hand-built {\"error\":\"permission_denied\"} literal (governed) is NOT caught"
  _st_clear
  _st_setup_go "pkg/tools/fixture.go" 'package tools

import "fmt"

func f(err error) string {
	return fmt.Sprintf(`{"error":"permission_denied","message":%q}`, err.Error())
}
'
  OUT=$(_st_run)
  _st_assert_not_contains "st19-skipped" "go-wire-discriminator" "$OUT"

  # ── ST-20: Rule 4 — prose "error" value (not a bare discriminator) is NOT
  #    caught, e.g. a fallback message embedding a %s/%v verb ─────────────────
  echo ""
  echo "ST-20: Go {\"error\":\"prose text: %s\"} fallback is NOT caught (not a discriminator shape)"
  _st_clear
  _st_setup_go "pkg/tools/fixture.go" 'package tools

import "fmt"

func f(marshalErr error) string {
	return fmt.Sprintf(`{"error":"failed to serialize response: %s"}`, marshalErr.Error())
}
'
  OUT=$(_st_run)
  _st_assert_not_contains "st20-skipped" "go-wire-discriminator" "$OUT"

  # ── ST-21: Rule 4 — not-wire-format opt-out (>=40 chars) is respected ─────
  echo ""
  echo "ST-21: Go discriminator literal with valid not-wire-format opt-out is skipped"
  _st_clear
  _st_setup_go "pkg/agent/fixture.go" 'package agent

import "fmt"

func f(err error) string {
	return fmt.Sprintf(`{"error":"a_brand_new_fifth_member","message":%q}`, err.Error()) // not-wire-format: local test-only fixture literal, never emitted over any wire boundary
}
'
  OUT=$(_st_run)
  _st_assert_not_contains "st21-skipped" "go-wire-discriminator" "$OUT"

  # ── ST-22: Rule 4 — a whole-line comment quoting a discriminator literal
  #    (e.g. this script's own doc header) is NOT caught ───────────────────
  echo ""
  echo "ST-22: Go whole-line comment quoting a discriminator literal is NOT caught"
  _st_clear
  _st_setup_go "pkg/agent/fixture.go" 'package agent

// Example: an unrelated tool once emitted `{"error":"a_brand_new_fifth_member"}` in prose.
func f() {}
'
  OUT=$(_st_run)
  _st_assert_not_contains "st22-skipped" "go-wire-discriminator" "$OUT"

  # ── ST-23: Rule 4 — space after the opening brace is caught (F8) ──────────
  echo ""
  echo "ST-23: Go {\" \"error\":\"<unknown>\"} with a space after the brace is caught"
  _st_clear
  _st_setup_go "pkg/agent/fixture.go" 'package agent

import "fmt"

func f(err error) string {
	return fmt.Sprintf(`{ "error":"a_brand_new_fifth_member","message":%q}`, err.Error())
}
'
  OUT=$(_st_run)
  _st_assert_contains "st23-found" "a_brand_new_fifth_member" "$OUT"
  _st_assert_contains "st23-rule"  "go-wire-discriminator" "$OUT"

  # ── ST-24: Rule 4 — "error" not the first key is caught (F8) ──────────────
  echo ""
  echo "ST-24: Go {\"message\":%q,\"error\":\"<unknown>\"} (error not first key) is caught"
  _st_clear
  _st_setup_go "pkg/agent/fixture.go" 'package agent

import "fmt"

func f(err error) string {
	return fmt.Sprintf(`{"message":%q,"error":"a_brand_new_fifth_member"}`, err.Error())
}
'
  OUT=$(_st_run)
  _st_assert_contains "st24-found" "a_brand_new_fifth_member" "$OUT"
  _st_assert_contains "st24-rule"  "go-wire-discriminator" "$OUT"

  # ── ST-14: doc header example has sufficient justification (meta-test) ─────
  echo ""
  echo "ST-14: doc header example justification is >=40 chars"
  SCRIPT_CONTENT=$(cat "$0")
  # Extract the example justification from the doc header
  EXAMPLE_JUST=$(echo "$SCRIPT_CONTENT" | grep -o 'not-wire-format: [^"'"'"']*' | head -1 | sed 's/not-wire-format: //')
  EXAMPLE_LEN=${#EXAMPLE_JUST}
  if [[ "$EXAMPLE_LEN" -ge 40 ]]; then
    echo "  PASS [st14-doc-example]: justification is ${EXAMPLE_LEN} chars (>=40)"
    SELF_PASS=$((SELF_PASS + 1))
  else
    echo "  FAIL [st14-doc-example]: justification is ${EXAMPLE_LEN} chars (<40): '${EXAMPLE_JUST}'"
    SELF_FAIL=$((SELF_FAIL + 1))
    SELF_ERRORS+=("[st14-doc-example] doc header example justification is ${EXAMPLE_LEN} chars, need >=40")
  fi

  # ── Summary ───────────────────────────────────────────────────────────────
  echo ""
  echo "─────────────────────────────────────────"
  echo "Self-test results: ${SELF_PASS} passed, ${SELF_FAIL} failed"

  if [[ "$SELF_FAIL" -gt 0 ]]; then
    echo ""
    echo "Failures:"
    for e in "${SELF_ERRORS[@]}"; do
      echo "  - $e"
    done
    exit 1
  fi

  echo "All self-test assertions passed."
  exit 0
fi

# ─── Normal lint mode ─────────────────────────────────────────────────────────

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

# ─── Rule 1: Go — any struct (package-level or function-scoped) in
#     pkg/gateway with >= 2 json tags; or var <name> struct with >= 1 json
#     tag when name is body/req/request/response ──────────────────────────────
#
# Algorithm (single Python pass for speed):
#   - Skip files under pkg/api/generated/ and *_test.go files
#   - For each .go file under pkg/gateway/
#   - Find lines matching `type <Name> struct {` (any leading whitespace)
#   - Find lines matching `var <name> struct {` (any leading whitespace)
#   - That do NOT contain `// not-wire-format` (case-insensitive)
#   - Then scan the following lines until the closing `}` of the struct body
#   - Count fields with `json:"` tag
#   - For `type` form: if count >= 2, emit a finding
#   - For `var` form: if count >= 2 OR (count >= 1 AND name in
#     {body, req, request, response}), emit a finding
#   - Suggest a generated type if schema fields match

set +e   # a $(...) assignment under `set -e` aborts BEFORE $? can be read,
         # which made the handler below dead code: a crashed sub-pass exited
         # non-zero with ZERO output, indistinguishable from a real finding.
GO_OFFENDERS=$(python3 - "$REPO_ROOT" <<'PYEOF'
import re
import os
import sys
import glob

repo_root = sys.argv[1] if len(sys.argv) > 1 else '.'
gateway_dir = os.path.join(repo_root, 'pkg', 'gateway')
generated_dir = os.path.join(repo_root, 'pkg', 'api', 'generated')
schemas_dir = os.path.join(repo_root, 'contracts', 'components', 'schemas')

# Drop ^ anchor — allows leading whitespace (catches function-scoped types)
STRUCT_TYPE_DEF = re.compile(r'\btype\s+(\w+)\s+struct\s*\{')
# var body struct { … } / var req struct { … }
STRUCT_VAR_DEF  = re.compile(r'\bvar\s+(\w+)\s+struct\s*\{')
# body := struct{ … } / body := &struct{ … } composite literals
STRUCT_ASSIGN_DEF = re.compile(r'\b(\w+)\s*:=\s*(?:&\s*)?struct\s*\{')
NOT_WIRE_FORMAT = re.compile(r'//\s*not-wire-format', re.IGNORECASE)
# Matches the annotation and captures everything after it (the justification text).
NOT_WIRE_FORMAT_WITH_CAPTURE = re.compile(r'//\s*not-wire-format\s*:?\s*(.*)', re.IGNORECASE)
JSON_TAG = re.compile(r'`[^`]*json:"[^"`]')

# Variable names that are almost certainly request/response body decoders
BODY_VAR_NAMES = {'body', 'req', 'request', 'response'}

# Minimum justification length (chars) required after '// not-wire-format'.
MIN_JUSTIFICATION_LEN = 40

# ── Load schema "required" fields for type-name suggestions ──────────────────
schema_required_fields = {}   # schema_name -> frozenset of required field names
if os.path.isdir(schemas_dir):
    for yaml_path in glob.glob(os.path.join(schemas_dir, '*.yaml')):
        schema_name = os.path.splitext(os.path.basename(yaml_path))[0]
        try:
            with open(yaml_path, 'r', encoding='utf-8', errors='replace') as f:
                content = f.read()
        except OSError:
            continue
        # Minimal YAML parse: grab the `required:` block lines (field names only)
        in_required = False
        fields = set()
        for line in content.splitlines():
            stripped = line.strip()
            if stripped.startswith('required:'):
                in_required = True
                continue
            if in_required:
                if stripped.startswith('- '):
                    fields.add(stripped[2:].strip())
                elif stripped and not stripped.startswith('#'):
                    in_required = False
        if fields:
            schema_required_fields[schema_name] = frozenset(fields)

def suggest_type(struct_field_names):
    """Return a gen.SuggestedName suggestion if exactly one schema matches all fields."""
    candidate_fields = frozenset(f.lower() for f in struct_field_names)
    matches = []
    for name, required in schema_required_fields.items():
        req_lower = frozenset(f.lower() for f in required)
        if req_lower and req_lower == candidate_fields:
            matches.append(name)
    if len(matches) == 1:
        return f"use gen.{matches[0]}"
    return "add a schema to contracts/components/schemas/ and use the generated type"

def collect_struct_body(lines, start_idx):
    """Return (json_count, field_names) by scanning the struct body starting at start_idx."""
    depth = 0
    json_count = 0
    field_names = []
    j = start_idx
    while j < len(lines):
        l = lines[j]
        depth += l.count('{') - l.count('}')
        if j > start_idx:
            if JSON_TAG.search(l):
                json_count += 1
                # Extract json field name for type suggestion
                m = re.search(r'json:"([^",`]+)', l)
                if m:
                    field_names.append(m.group(1))
        if depth <= 0 and j > start_idx:
            break
        j += 1
    return json_count, field_names

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

            # ── `type X struct {` (package-level or function-scoped) ──────────
            m = STRUCT_TYPE_DEF.search(line)
            if m:
                nwf_m = NOT_WIRE_FORMAT.search(line)
                if nwf_m:
                    cap_m = NOT_WIRE_FORMAT_WITH_CAPTURE.search(line)
                    justification = cap_m.group(1).strip() if cap_m else ''
                    if len(justification) < MIN_JUSTIFICATION_LEN:
                        struct_start_line = i + 1
                        type_name = m.group(1)
                        relpath = os.path.relpath(fpath, repo_root)
                        findings.append(
                            f"{relpath}:{struct_start_line}: [go-wire-type-justification] "
                            f"'// not-wire-format' on '{type_name}' has {len(justification)}-char "
                            f"justification (minimum {MIN_JUSTIFICATION_LEN}) — "
                            f"add a descriptive reason, e.g.: "
                            f"// not-wire-format: internal config cache decoded only at startup, never emitted"
                        )
                    i += 1
                    continue

                struct_start_line = i + 1
                type_name = m.group(1)
                json_count, field_names = collect_struct_body(lines, i)

                if json_count >= 2:
                    relpath = os.path.relpath(fpath, repo_root)
                    suggestion = suggest_type(field_names)
                    findings.append(
                        f"{relpath}:{struct_start_line}: [go-wire-type] "
                        f"hand-written wire-format struct '{type_name}' ({json_count} json fields) — "
                        f"migrate to contracts/components/schemas/ and regenerate; {suggestion}"
                    )
                i += 1
                continue

            # ── `var <name> struct {` anonymous struct ────────────────────────
            mv = STRUCT_VAR_DEF.search(line)
            if mv:
                nwf_m = NOT_WIRE_FORMAT.search(line)
                if nwf_m:
                    cap_m = NOT_WIRE_FORMAT_WITH_CAPTURE.search(line)
                    justification = cap_m.group(1).strip() if cap_m else ''
                    if len(justification) < MIN_JUSTIFICATION_LEN:
                        var_start_line = i + 1
                        var_name = mv.group(1)
                        relpath = os.path.relpath(fpath, repo_root)
                        findings.append(
                            f"{relpath}:{var_start_line}: [go-wire-type-justification] "
                            f"'// not-wire-format' on var '{var_name}' has {len(justification)}-char "
                            f"justification (minimum {MIN_JUSTIFICATION_LEN}) — "
                            f"add a descriptive reason, e.g.: "
                            f"// not-wire-format: internal config cache decoded only at startup, never emitted"
                        )
                    i += 1
                    continue

                var_start_line = i + 1
                var_name = mv.group(1)
                json_count, field_names = collect_struct_body(lines, i)

                flag = False
                if json_count >= 2:
                    flag = True
                elif json_count >= 1 and var_name.lower() in BODY_VAR_NAMES:
                    flag = True

                if flag:
                    relpath = os.path.relpath(fpath, repo_root)
                    suggestion = suggest_type(field_names)
                    findings.append(
                        f"{relpath}:{var_start_line}: [go-wire-type] "
                        f"hand-written wire-format var struct '{var_name}' ({json_count} json field(s)) — "
                        f"migrate to contracts/components/schemas/ and regenerate; {suggestion}"
                    )
                i += 1
                continue

            # ── `name := struct{ … }` / `name := &struct{ … }` composite literals ──
            ma = STRUCT_ASSIGN_DEF.search(line)
            if ma:
                nwf_m = NOT_WIRE_FORMAT.search(line)
                if nwf_m:
                    cap_m = NOT_WIRE_FORMAT_WITH_CAPTURE.search(line)
                    justification = cap_m.group(1).strip() if cap_m else ''
                    if len(justification) < MIN_JUSTIFICATION_LEN:
                        assign_start_line = i + 1
                        assign_name = ma.group(1)
                        relpath = os.path.relpath(fpath, repo_root)
                        findings.append(
                            f"{relpath}:{assign_start_line}: [go-wire-type-justification] "
                            f"'// not-wire-format' on composite struct '{assign_name}' has {len(justification)}-char "
                            f"justification (minimum {MIN_JUSTIFICATION_LEN}) — "
                            f"add a descriptive reason, e.g.: "
                            f"// not-wire-format: internal config cache decoded only at startup, never emitted"
                        )
                    i += 1
                    continue

                assign_start_line = i + 1
                assign_name = ma.group(1)
                json_count, field_names = collect_struct_body(lines, i)

                flag = False
                if json_count >= 2:
                    flag = True
                elif json_count >= 1 and assign_name.lower() in BODY_VAR_NAMES:
                    flag = True

                if flag:
                    relpath = os.path.relpath(fpath, repo_root)
                    suggestion = suggest_type(field_names)
                    findings.append(
                        f"{relpath}:{assign_start_line}: [go-wire-type] "
                        f"hand-written wire-format composite struct '{assign_name}' ({json_count} json field(s)) — "
                        f"migrate to contracts/components/schemas/ and regenerate; {suggestion}"
                    )

            i += 1

for f in findings:
    print(f)
PYEOF
)

# Capture Python exit status explicitly; abort on unexpected failure.
_PY_EXIT=$?
set -e   # re-arm; the assignment above is the only place we tolerate a non-zero exit
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

# ─── Rule 2: TypeScript — interface or type = { } in src/lib ──────────────────
#
# Flags:
#   - `interface Foo { … }` (exported or not)
#   - `export type Foo = { … }`   (object-literal body)
#   - `export type Foo = Bar & { … }` (intersection with anonymous object)
#   - `export type Foo = { … } | { … }` (union of object literals)
#   - (any of the above without `export` prefix)
#
# NOT flagged:
#   - Re-exports: `export type { X } from '...'` (no inline body)
#   - Files under src/lib/api/generated/
#   - Test files (*_test.ts, *.test.ts, *.test.tsx)
#   - Lines bearing `// not-wire-format` (case-insensitive)
#
# ─── Rule 3: TypeScript — hand-written z.object/union/discriminatedUnion/
#             intersection/lazy schemas in src/lib/*.ts ──────────────────────────
#
# Flags `const _fooSchema = z.object/union/discriminatedUnion/intersection/lazy(…`
# in all src/lib/*.ts files (excluding generated dir and test files).

set +e   # a $(...) assignment under `set -e` aborts BEFORE $? can be read,
         # which made the handler below dead code: a crashed sub-pass exited
         # non-zero with ZERO output, indistinguishable from a real finding.
TS_OFFENDERS=$(python3 - "$REPO_ROOT" <<'PYEOF'
import re
import os
import sys
import glob

repo_root = sys.argv[1] if len(sys.argv) > 1 else '.'
lib_dir = os.path.join(repo_root, 'src', 'lib')
generated_dir = os.path.join(repo_root, 'src', 'lib', 'api', 'generated')

# Widened: catches both exported and non-exported interfaces
# export interface Foo {  /  interface Foo {  /  export interface Foo extends
EXPORT_IFACE = re.compile(r'^(?:export\s+)?interface\s+(\w+)[\s{<]')

# Object-literal type forms (with or without export):
#   type Foo = {
#   export type Foo = {
#   export type Foo = Bar & {    (intersection with anonymous object)
#   export type Foo = { … } | { (union of object literals)
EXPORT_TYPE_OBJ = re.compile(
    r'^(?:export\s+)?type\s+(\w+)\s*='
    r'(?:'
        r'\s*\{'                          # basic: type Foo = {
        r'|'
        r'\s*\w[\w.<>, ]*\s*&\s*\{'      # intersection: type Foo = Bar & {
        r'|'
        r'\s*\{[^}]*\}\s*\|'             # union of obj literals: type Foo = { … } |
    r')'
)

# Hand-written Zod schemas: const _fooSchema = z.object/union/discriminatedUnion/intersection/lazy(
HAND_ZOD = re.compile(
    r'\bconst\s+(_\w+Schema)\s*=\s*z\.'
    r'(?:object|union|discriminatedUnion|intersection|lazy)\s*\('
)

NOT_WIRE_FORMAT = re.compile(r'//\s*not-wire-format', re.IGNORECASE)

findings = []

# Collect all .ts files under src/lib/ (excluding generated dir and test files)
all_ts_files = []
if os.path.isdir(lib_dir):
    for dirpath, dirnames, filenames in os.walk(lib_dir):
        # Prune generated directory so we never descend into it
        dirnames[:] = [d for d in dirnames
                       if os.path.join(dirpath, d) != generated_dir
                       and not os.path.join(dirpath, d).startswith(generated_dir + os.sep)]
        for fname in filenames:
            if not fname.endswith('.ts') and not fname.endswith('.tsx'):
                continue
            # Skip test files
            if fname.endswith('_test.ts') or fname.endswith('.test.ts') or fname.endswith('.test.tsx'):
                continue
            fpath = os.path.join(dirpath, fname)
            # Final safety check: skip if inside generated dir
            if os.path.commonpath([fpath, generated_dir]) == generated_dir:
                continue
            all_ts_files.append(fpath)

for fpath in sorted(all_ts_files):
    if not os.path.isfile(fpath):
        continue

    try:
        with open(fpath, 'r', encoding='utf-8', errors='replace') as f:
            lines = f.readlines()
    except OSError:
        continue

    relpath = os.path.relpath(fpath, repo_root)

    for i, line in enumerate(lines):
        # ── hand-written Zod schema check ─────────────────────────────────────
        mz = HAND_ZOD.search(line)
        if mz:
            if NOT_WIRE_FORMAT.search(line):
                pass  # opt-out respected; no further checking
            else:
                findings.append(
                    f"{relpath}:{i+1}: [ts-hand-zod] "
                    f"hand-written Zod schema '{mz.group(1)}' — "
                    f"consider using generated Zod schema from src/lib/api/generated/schemas.ts"
                )
            continue

        # ── interface / type object-literal check ─────────────────────────────
        if NOT_WIRE_FORMAT.search(line):
            continue  # opt-out respected

        m = EXPORT_IFACE.search(line) or EXPORT_TYPE_OBJ.search(line)
        if m:
            type_name = m.group(1)
            findings.append(
                f"{relpath}:{i+1}: [ts-wire-type] "
                f"hand-written wire-format type '{type_name}' — "
                f"migrate to contracts/components/schemas/ and regenerate"
            )

for f in findings:
    print(f)
PYEOF
)

# Capture Python exit status explicitly; abort on unexpected failure.
_PY_EXIT=$?
set -e   # re-arm; the assignment above is the only place we tolerate a non-zero exit
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

# ─── Rule 4: Go — hand-built `{"error":"<discriminator>"` JSON literals in
#     pkg/gateway/, pkg/agent/, pkg/tools/ whose discriminator is not already
#     governed (issue #618) ──────────────────────────────────────────────────
#
# See the doc header's "GO (Rule 4, issue #618)" section for the full
# rationale. Scans for the shape `{"error":"<value>"` in both raw
# (`{"error":"x"}`) and interpreted ("{\"error\":\"x\"}") Go string literals.
# A <value> that does not look like a bare discriminator identifier (i.e.
# contains a space, `%`, or any other non `[A-Za-z0-9_]` character) does not
# match the capture at all and is silently ignored — it is ad hoc prose, not
# a discriminator.

set +e   # a $(...) assignment under `set -e` aborts BEFORE $? can be read,
         # which made the handler below dead code: a crashed sub-pass exited
         # non-zero with ZERO output, indistinguishable from a real finding.
GO_DISCRIMINATOR_OFFENDERS=$(python3 - "$REPO_ROOT" <<'PYEOF'
import re
import os
import sys

repo_root = sys.argv[1] if len(sys.argv) > 1 else '.'
generated_dir = os.path.join(repo_root, 'pkg', 'api', 'generated')

# KNOWN_STRUCTURED_FAILURE_DISCRIMINATORS must stay in lockstep with
# pkg/tools/result.go's exported *Code constants and
# pkg/gateway/tool_result_store.go's structuredFailureDiscriminators map.
# Adding a new discriminator to BOTH of those without adding it here (or vice
# versa) is exactly the drift class issue #618 closes.
KNOWN_STRUCTURED_FAILURE_DISCRIMINATORS = {
    'delegation_denied',
    'file_exists',
    'permission_denied',
    'tool_assembly_duplicate',
    # ADR-066 (T066-01 schemas in contracts/asyncapi.yaml: ToolArgumentRefusal,
    # ToolResultRecallMark; T066-04 producers + *Code constants).
    'tool_arguments_too_large',
    'tool_result_recall_mark',
}

# Matches both raw-string (`{"error":"x"}`) and interpreted-string
# ("{\"error\":\"x\"}") source forms: each quote may optionally be preceded
# by a literal backslash (\\?) as it appears in the RAW SOURCE TEXT (this
# script reads files as plain text, not Go-escape-decoded). The captured
# group requires a bare [A-Za-z0-9_]+ token immediately followed by a
# (possibly backslash-escaped) closing quote — prose containing spaces or
# format verbs (e.g. "failed to serialize response: %s") cannot match this
# shape at all.
#
# F8 (round-2 review): the opening `\{` is no longer required to be
# IMMEDIATELY followed by `"error"`. A bounded, non-greedy gap of up to 120
# characters of anything except `{`/`}` is allowed between the brace and the
# "error" key, so this also catches:
#   - a space/whitespace right after the brace: `{ "error":"code", ...}`
#   - "error" as a non-first key: `{"message":%q,"error":"code"}`
# The `[^{}]` exclusion keeps the gap from crossing into a different,
# unrelated brace pair (nested literal, or the end of an enclosing block),
# so it still only matches an "error" key belonging to the SAME literal that
# opened with the matched `{`.
DISCRIMINATOR_LITERAL = re.compile(
    r'\{[^{}]{0,120}?\\?"error\\?"\s*:\s*\\?"([A-Za-z0-9_]+)\\?"'
)
NOT_WIRE_FORMAT = re.compile(r'//\s*not-wire-format', re.IGNORECASE)
NOT_WIRE_FORMAT_WITH_CAPTURE = re.compile(r'//\s*not-wire-format\s*:?\s*(.*)', re.IGNORECASE)
MIN_JUSTIFICATION_LEN = 40

findings = []

for sub in ('pkg/gateway', 'pkg/agent', 'pkg/tools'):
    scan_dir = os.path.join(repo_root, sub)
    if not os.path.isdir(scan_dir):
        continue
    for dirpath, dirnames, filenames in os.walk(scan_dir):
        for fname in sorted(filenames):
            if not fname.endswith('.go') or fname.endswith('_test.go'):
                continue
            fpath = os.path.join(dirpath, fname)
            if os.path.commonpath([fpath, generated_dir]) == generated_dir:
                continue

            try:
                with open(fpath, 'r', encoding='utf-8', errors='replace') as f:
                    lines = f.readlines()
            except OSError:
                continue

            relpath = os.path.relpath(fpath, repo_root)
            for i, line in enumerate(lines):
                stripped = line.strip()
                # Whole-line comments never carry real code — skip them so a
                # doc comment quoting a discriminator literal (like this
                # script's own header, or tool_result_store.go's allow-list
                # doc comment) is never flagged.
                if stripped.startswith('//'):
                    continue

                m = DISCRIMINATOR_LITERAL.search(line)
                if not m:
                    continue

                discriminator = m.group(1)
                if discriminator in KNOWN_STRUCTURED_FAILURE_DISCRIMINATORS:
                    continue

                if NOT_WIRE_FORMAT.search(line):
                    cap_m = NOT_WIRE_FORMAT_WITH_CAPTURE.search(line)
                    justification = cap_m.group(1).strip() if cap_m else ''
                    if len(justification) < MIN_JUSTIFICATION_LEN:
                        findings.append(
                            f"{relpath}:{i+1}: [go-wire-type-justification] "
                            f"'// not-wire-format' on discriminator literal '{discriminator}' has "
                            f"{len(justification)}-char justification (minimum {MIN_JUSTIFICATION_LEN}) — "
                            f"add a descriptive reason"
                        )
                    continue

                findings.append(
                    f"{relpath}:{i+1}: [go-wire-discriminator] "
                    f"hand-built structured-failure literal with discriminator '{discriminator}' — "
                    f"not in the known allow-list (delegation_denied, file_exists, permission_denied, "
                    f"tool_assembly_duplicate). Add a contracts/asyncapi.yaml schema, a "
                    f"marshalWithinBudget-routed producer in pkg/tools, and register the discriminator "
                    f"in pkg/gateway/tool_result_store.go's structuredFailureDiscriminators AND in this "
                    f"script's KNOWN_STRUCTURED_FAILURE_DISCRIMINATORS — or mark the line "
                    f"'// not-wire-format: <reason>' if it genuinely is not a wire payload"
                )

for f in findings:
    print(f)
PYEOF
)

_PY_EXIT=$?
set -e   # re-arm; the assignment above is the only place we tolerate a non-zero exit
if [[ $_PY_EXIT -ne 0 ]]; then
  echo "check-no-handwritten-wire-types: ERROR — Go discriminator-literal Python sub-pass exited ${_PY_EXIT}" >&2
  exit 2
fi

if [[ -n "$GO_DISCRIMINATOR_OFFENDERS" ]]; then
  while IFS= read -r line; do
    FINDING_LINES+=("$line")
    FINDINGS=$((FINDINGS + 1))
  done <<< "$GO_DISCRIMINATOR_OFFENDERS"
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
echo "To suppress a false positive, add '// not-wire-format: <justification>' on"
echo "the same line as the type/interface declaration.  The justification text"
echo "must be at least 40 characters (explains why the type is NOT a wire-format"
echo "type despite carrying json tags).  Example:"
echo "  type myHelper struct { // not-wire-format: internal config cache decoded only at startup, never emitted"
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
