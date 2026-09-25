#!/usr/bin/env bash
# check-agent-files.test.sh
#
# Proof-of-failure companion for check-agent-files.sh (dev-team-setup design
# section 8: the agent-file/dev-team-skill guard, all 13 checks plus the
# 8.2 false-positive handling).
#
# Everything runs against a synthetic fixture tree built under mktemp via
# the guard's REPO_ROOT override; the real repository's .claude/ tree is
# never read for assertions (only for the untouched-check at the end, which
# proves it). Modelled on scripts/check-agents-md-sync.test.sh.
#
# Shape: one shared, fully compliant BASELINE tree exercises the GREEN path
# (case 0). Every RED case clones the baseline and mutates exactly one
# thing, so a single fixture proves both "the guard passes the good version"
# and "the guard fails the bad version" for that check, per design 8.2's
# "one good agent file and one bad per check" companion mechanism.
#
# CASES:
#   0     baseline (unmutated) -> exit 0, "OK"
#   1-13  one targeted mutation per check 1..13 -> exit 1, "check<N>:" present
#   FP-a  allow-marker suppresses a check-5 hit on the marked line
#   FP-b  check 3's user-level skill allowlist (webapp-testing) passes
#         without existing on disk
#   FP-c  check 13 is frontmatter-scoped: a body-text "model:" example is
#         never flagged
#   FP-d  check 7 reads only the teammates: frontmatter list; a prose
#         mention of a phantom role name is never flagged
#   FP-e  check 2 never matches a path-shaped token embedded inside a
#         longer absolute, outside-repo path
#   FP-f  check 9's upstream-drift comparison is WARN-only and never fails
#         the exit code
#   REAL  the real repository is never touched by any of the above
#
# Exit code: 0 if every assertion passes, 1 if any fails.

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GUARD="$SCRIPT_DIR/check-agent-files.sh"
REAL_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

PASS=0
FAIL=0
ERRORS=()

TMP_BASE="$(mktemp -d "${TMPDIR:-/tmp}/check-agent-files-test.XXXXXX")" || exit 2
trap 'rm -rf "$TMP_BASE"' EXIT

# ─── Assert helpers ─────────────────────────────────────────────────────────

assert_exit_code() {
  local label="$1" expected="$2" actual="$3"
  if [ "$actual" -eq "$expected" ]; then
    echo "  PASS [$label]: exit $actual (expected $expected)"
    PASS=$((PASS + 1))
  else
    echo "  FAIL [$label]: exit $actual (expected $expected)"
    FAIL=$((FAIL + 1))
    ERRORS+=("[$label] expected exit $expected, got $actual")
  fi
}

assert_output_contains() {
  local label="$1" needle="$2" file="$3"
  if grep -qF -- "$needle" "$file"; then
    echo "  PASS [$label]: output contains '$needle'"
    PASS=$((PASS + 1))
  else
    echo "  FAIL [$label]: output does NOT contain '$needle'"
    FAIL=$((FAIL + 1))
    ERRORS+=("[$label] expected output to contain '$needle'")
  fi
}

assert_output_not_contains() {
  local label="$1" needle="$2" file="$3"
  if grep -qF -- "$needle" "$file"; then
    echo "  FAIL [$label]: output unexpectedly contains '$needle'"
    FAIL=$((FAIL + 1))
    ERRORS+=("[$label] expected output to NOT contain '$needle'")
  else
    echo "  PASS [$label]: output does not contain '$needle'"
    PASS=$((PASS + 1))
  fi
}

# ─── Fixture helpers ────────────────────────────────────────────────────────

# mutate <file> <old> <new> — exact single-occurrence text replace; fails
# loudly (harness error, not a silent no-op) if <old> is not present, so a
# broken fixture cannot masquerade as a passing test.
mutate() {
  python3 -c '
import sys
path, old, new = sys.argv[1], sys.argv[2], sys.argv[3]
with open(path, "r", encoding="utf-8") as f:
    text = f.read()
if old not in text:
    sys.stderr.write("FIXTURE HARNESS FAILURE: pattern not found in " + path + ": " + repr(old) + "\n")
    sys.exit(2)
text = text.replace(old, new, 1)
with open(path, "w", encoding="utf-8") as f:
    f.write(text)
' "$1" "$2" "$3"
  local rc=$?
  if [ "$rc" -ne 0 ]; then
    echo "FIXTURE HARNESS FAILURE: mutate() failed on $1" >&2
    exit 2
  fi
}

append_line() {
  python3 -c '
import sys
path, line = sys.argv[1], sys.argv[2]
with open(path, "a", encoding="utf-8") as f:
    f.write(line + "\n")
' "$1" "$2"
}

clone_baseline() {
  local name="$1"
  local d="$TMP_BASE/$name"
  cp -R "$BASE_DIR" "$d"
  printf '%s' "$d"
}

# ─── Canonical discipline text (shared by canonical template and every
#     consuming fixture file, so a clean baseline byte-matches by construction)

SHARED_TRAITS_TXT="Always verify your own work with evidence and correct yourself when wrong."
DEVELOPER_RULES_TXT="Do exactly the task and report accidental findings as notes instead of side fixes."
REVIEWER_RULES_TXT="Treat every claim as untrue until you personally verify it against the evidence."

# ─── Baseline tree (case 0 — the GREEN fixture every RED case clones) ──────

BASE_DIR="$TMP_BASE/baseline"
mkdir -p "$BASE_DIR/.claude/agents" "$BASE_DIR/.claude/templates" \
  "$BASE_DIR/.claude/skills/omnipus-shared-rules" \
  "$BASE_DIR/.claude/skills/omnipus-backend-rules" \
  "$BASE_DIR/.claude/skills/omnipus-frontend-rules" \
  "$BASE_DIR/.claude/skills/omnipus-planning-orchestration" \
  "$BASE_DIR/.claude/skills/elicify-test-writing" \
  "$BASE_DIR/.claude/skills/test-integrity-audit"

cat > "$BASE_DIR/CLAUDE.md" <<'EOF'
# CLAUDE.md (fixture)

Skills reference: every dev-team agent preloads omnipus-shared-rules.
EOF

cat > "$BASE_DIR/.claude/templates/agent-discipline.md" <<EOF
# Canonical agent discipline (fixture)

## Shared traits
<!-- agent-discipline:shared-traits:start -->
$SHARED_TRAITS_TXT
<!-- agent-discipline:shared-traits:end -->

## Developer rules
<!-- agent-discipline:developer-rules:start -->
$DEVELOPER_RULES_TXT
<!-- agent-discipline:developer-rules:end -->

## Reviewer rules
<!-- agent-discipline:reviewer-rules:start -->
$REVIEWER_RULES_TXT
<!-- agent-discipline:reviewer-rules:end -->
EOF

cat > "$BASE_DIR/.claude/templates/plugin-reviewer-dispatch.md" <<EOF
# Plugin reviewer dispatch template (fixture)

Load \`omnipus-shared-rules\` with the Skill tool before you begin.

<!-- agent-discipline:reviewer-rules:start -->
$REVIEWER_RULES_TXT
<!-- agent-discipline:reviewer-rules:end -->
EOF

cat > "$BASE_DIR/.claude/skills/omnipus-shared-rules/SKILL.md" <<'EOF'
---
name: omnipus-shared-rules
description: Shared rules fixture.
---
Last reviewed: 2026-09-25

Fixture shared skill body.
EOF

cat > "$BASE_DIR/.claude/skills/omnipus-backend-rules/SKILL.md" <<'EOF'
---
name: omnipus-backend-rules
description: Backend rules fixture.
---
Last reviewed: 2026-09-25

Fixture backend skill body.
EOF

cat > "$BASE_DIR/.claude/skills/omnipus-frontend-rules/SKILL.md" <<'EOF'
---
name: omnipus-frontend-rules
description: Frontend rules fixture.
---
Last reviewed: 2026-09-25

Fixture frontend skill body.
EOF

cat > "$BASE_DIR/.claude/skills/omnipus-planning-orchestration/SKILL.md" <<'EOF'
---
name: omnipus-planning-orchestration
description: Planning fixture.
---
Last reviewed: 2026-09-25

Fixture planning skill body.
EOF

cat > "$BASE_DIR/.claude/skills/elicify-test-writing/SKILL.md" <<'EOF'
---
name: elicify-test-writing
description: Vendored test-writing fixture.
---
Fixture vendored skill body (no Last reviewed line by design; SOURCE.yaml carries the date).
EOF

cat > "$BASE_DIR/.claude/skills/elicify-test-writing/SOURCE.yaml" <<'EOF'
source_repo: /tmp/fixture-elicify-skills-does-not-need-to-exist
source_branch: main
source_commit: abc1234
upstream_subpath: skills/elicify-test-writing
copy_date: 2026-09-25
copy_or_derived: copy
EOF

cat > "$BASE_DIR/.claude/skills/test-integrity-audit/SKILL.md" <<'EOF'
---
name: test-integrity-audit
description: Vendored test-integrity fixture.
---
Fixture vendored skill body.
EOF

cat > "$BASE_DIR/.claude/skills/test-integrity-audit/SOURCE.yaml" <<'EOF'
source_repo: /tmp/fixture-elicify-skills-does-not-need-to-exist
source_branch: feat/test-integrity-audit-skill
source_commit: d87fdcf
upstream_subpath: skills/test-integrity-audit
copy_date: 2026-09-25
copy_or_derived: copy
EOF

cat > "$BASE_DIR/.claude/agents/team-lead.md" <<'EOF'
---
name: team-lead
description: Orchestrates the dev team fixture.
skills:
  - omnipus-shared-rules
  - omnipus-planning-orchestration
---

# team-lead (fixture)

Last reviewed: 2026-09-25

Plugin-reviewer dispatches paste .claude/templates/plugin-reviewer-dispatch.md at the head of every dispatch.
EOF

cat > "$BASE_DIR/.claude/agents/squad-lead.md" <<EOF
---
name: squad-lead
description: Runs one squad end to end (fixture).
skills:
  - omnipus-shared-rules
  - omnipus-planning-orchestration
---

# squad-lead (fixture)

Last reviewed: 2026-09-25

<!-- agent-discipline:shared-traits:start -->
$SHARED_TRAITS_TXT
<!-- agent-discipline:shared-traits:end -->
EOF

cat > "$BASE_DIR/.claude/agents/backend-lead.md" <<EOF
---
name: backend-lead
description: Implements the backend fixture.
skills:
  - omnipus-shared-rules
  - omnipus-backend-rules
---

# backend-lead (fixture)

Last reviewed: 2026-09-25

<!-- agent-discipline:shared-traits:start -->
$SHARED_TRAITS_TXT
<!-- agent-discipline:shared-traits:end -->

<!-- agent-discipline:developer-rules:start -->
$DEVELOPER_RULES_TXT
<!-- agent-discipline:developer-rules:end -->
EOF

cat > "$BASE_DIR/.claude/agents/security-lead.md" <<EOF
---
name: security-lead
description: Reviews security fixture.
skills:
  - omnipus-shared-rules
---

# security-lead (fixture)

Last reviewed: 2026-09-25

<!-- agent-discipline:shared-traits:start -->
$SHARED_TRAITS_TXT
<!-- agent-discipline:shared-traits:end -->

<!-- agent-discipline:reviewer-rules:start -->
$REVIEWER_RULES_TXT
<!-- agent-discipline:reviewer-rules:end -->
EOF

cat > "$BASE_DIR/.claude/agents/qa-lead.md" <<EOF
---
name: qa-lead
description: Tests fixture.
skills:
  - omnipus-shared-rules
  - elicify-test-writing
  - test-integrity-audit
---

# qa-lead (fixture)

Last reviewed: 2026-09-25

<!-- agent-discipline:shared-traits:start -->
$SHARED_TRAITS_TXT
<!-- agent-discipline:shared-traits:end -->

<!-- agent-discipline:developer-rules:start -->
$DEVELOPER_RULES_TXT
<!-- agent-discipline:developer-rules:end -->

<!-- agent-discipline:reviewer-rules:start -->
$REVIEWER_RULES_TXT
<!-- agent-discipline:reviewer-rules:end -->
EOF

BACKEND_MD="$BASE_DIR/.claude/agents/backend-lead.md"

# Snapshot the real repo BEFORE any run (REAL case compares after).
git -C "$REAL_ROOT" status --porcelain > "$TMP_BASE/real-before.txt" 2>/dev/null

echo "=== check-agent-files companion ==="
echo ""

# ─── Case 0: baseline is GREEN ──────────────────────────────────────────────

echo "Case 0: baseline (fully compliant fixture tree) is GREEN"
REPO_ROOT="$BASE_DIR" bash "$GUARD" > "$TMP_BASE/c0.out" 2>&1
ge=$?
assert_exit_code "c0-exit" 0 "$ge"
assert_output_contains "c0-ok" "OK — 0 findings" "$TMP_BASE/c0.out"

# ─── Case 1: check 1 — frontmatter name mismatch ───────────────────────────

echo ""
echo "Case 1: check 1 fires on a name/filename mismatch"
T="$(clone_baseline c1)"
mutate "$T/.claude/agents/backend-lead.md" "name: backend-lead" "name: backend-lead-wrong"
REPO_ROOT="$T" bash "$GUARD" > "$TMP_BASE/c1.out" 2>&1
ge=$?
assert_exit_code "c1-exit" 1 "$ge"
assert_output_contains "c1-check" "check1:" "$TMP_BASE/c1.out"
assert_output_contains "c1-detail" "does not match filename" "$TMP_BASE/c1.out"

# ─── Case 2: check 2 — dead path citation ──────────────────────────────────

echo ""
echo "Case 2: check 2 fires on a citation to a path that does not exist"
T="$(clone_baseline c2)"
append_line "$T/.claude/agents/backend-lead.md" 'See docs/does-not-exist-xyz.md for details.'
REPO_ROOT="$T" bash "$GUARD" > "$TMP_BASE/c2.out" 2>&1
ge=$?
assert_exit_code "c2-exit" 1 "$ge"
assert_output_contains "c2-check" "check2:" "$TMP_BASE/c2.out"
assert_output_contains "c2-detail" "docs/does-not-exist-xyz.md" "$TMP_BASE/c2.out"

# ─── Case 3: check 3 — phantom skill citation ──────────────────────────────

echo ""
echo "Case 3: check 3 fires on a skill name that does not exist on disk"
T="$(clone_baseline c3)"
# Backticks below must stay literal (a fixture skill citation).
# shellcheck disable=SC2016
append_line "$T/.claude/agents/backend-lead.md" 'Load the `omnipus-phantom-skill` skill before editing.'
REPO_ROOT="$T" bash "$GUARD" > "$TMP_BASE/c3.out" 2>&1
ge=$?
assert_exit_code "c3-exit" 1 "$ge"
assert_output_contains "c3-check" "check3:" "$TMP_BASE/c3.out"
assert_output_contains "c3-detail" "omnipus-phantom-skill" "$TMP_BASE/c3.out"

# ─── Case 4: check 4 — banned command pattern ──────────────────────────────

echo ""
echo "Case 4: check 4 fires on an untagged 'go build ./...' instruction"
T="$(clone_baseline c4)"
append_line "$T/.claude/agents/backend-lead.md" 'Run go build ./... before committing.'
REPO_ROOT="$T" bash "$GUARD" > "$TMP_BASE/c4.out" 2>&1
ge=$?
assert_exit_code "c4-exit" 1 "$ge"
assert_output_contains "c4-check" "check4:" "$TMP_BASE/c4.out"
assert_output_contains "c4-detail" "go build ./... (untagged)" "$TMP_BASE/c4.out"

# ─── Case 5: check 5 — retired surface name ────────────────────────────────

echo ""
echo "Case 5: check 5 fires on a retired-surface name in an instruction line"
T="$(clone_baseline c5)"
append_line "$T/.claude/agents/backend-lead.md" 'The exec allowlist controls this.'
REPO_ROOT="$T" bash "$GUARD" > "$TMP_BASE/c5.out" 2>&1
ge=$?
assert_exit_code "c5-exit" 1 "$ge"
assert_output_contains "c5-check" "check5:" "$TMP_BASE/c5.out"
assert_output_contains "c5-detail" "exec allowlist" "$TMP_BASE/c5.out"

# ─── Case 6: check 6 — role-skill isolation breach ─────────────────────────

echo ""
echo "Case 6: check 6 fires when backend-lead names a frontend-only skill"
T="$(clone_baseline c6)"
# Backticks below must stay literal (a fixture skill citation).
# shellcheck disable=SC2016
append_line "$T/.claude/agents/backend-lead.md" 'Load the `omnipus-frontend-rules` skill for CSS details.'
REPO_ROOT="$T" bash "$GUARD" > "$TMP_BASE/c6.out" 2>&1
ge=$?
assert_exit_code "c6-exit" 1 "$ge"
assert_output_contains "c6-check" "check6:" "$TMP_BASE/c6.out"
assert_output_contains "c6-detail" "outside role 'backend-lead' allowed set" "$TMP_BASE/c6.out"

# ─── Case 7: check 7 — phantom teammate ────────────────────────────────────

echo ""
echo "Case 7: check 7 fires on a phantom teammates: entry"
T="$(clone_baseline c7)"
old=$'  - omnipus-backend-rules\n---'
new=$'  - omnipus-backend-rules\nteammates:\n  - phantom-role-xyz\n---'
mutate "$T/.claude/agents/backend-lead.md" "$old" "$new"
REPO_ROOT="$T" bash "$GUARD" > "$TMP_BASE/c7.out" 2>&1
ge=$?
assert_exit_code "c7-exit" 1 "$ge"
assert_output_contains "c7-check" "check7:" "$TMP_BASE/c7.out"
assert_output_contains "c7-detail" "phantom-role-xyz" "$TMP_BASE/c7.out"

# ─── Case 8: check 8 — missing Last reviewed header ────────────────────────

echo ""
echo "Case 8: check 8 fires when the Last reviewed header is removed"
T="$(clone_baseline c8)"
mutate "$T/.claude/agents/backend-lead.md" $'Last reviewed: 2026-09-25\n\n' ''
REPO_ROOT="$T" bash "$GUARD" > "$TMP_BASE/c8.out" 2>&1
ge=$?
assert_exit_code "c8-exit" 1 "$ge"
assert_output_contains "c8-check" "check8:" "$TMP_BASE/c8.out"
assert_output_contains "c8-detail" "missing 'Last reviewed" "$TMP_BASE/c8.out"

# ─── Case 9: check 9 — incomplete vendored SOURCE.yaml ─────────────────────

echo ""
echo "Case 9: check 9 fires when SOURCE.yaml is missing a required field"
T="$(clone_baseline c9)"
mutate "$T/.claude/skills/elicify-test-writing/SOURCE.yaml" $'source_branch: main\n' ''
REPO_ROOT="$T" bash "$GUARD" > "$TMP_BASE/c9.out" 2>&1
ge=$?
assert_exit_code "c9-exit" 1 "$ge"
assert_output_contains "c9-check" "check9:" "$TMP_BASE/c9.out"
assert_output_contains "c9-detail" "SOURCE.yaml incomplete" "$TMP_BASE/c9.out"

# ─── Case 10: check 10 — hard-coded integration branch ─────────────────────

echo ""
echo "Case 10: check 10 fires on a hard-coded release/vN branch literal"
T="$(clone_baseline c10)"
append_line "$T/.claude/agents/backend-lead.md" 'Dispatch against release/v0.1.1.'
REPO_ROOT="$T" bash "$GUARD" > "$TMP_BASE/c10.out" 2>&1
ge=$?
assert_exit_code "c10-exit" 1 "$ge"
assert_output_contains "c10-check" "check10:" "$TMP_BASE/c10.out"
assert_output_contains "c10-detail" "hard-coded integration branch" "$TMP_BASE/c10.out"

# ─── Case 11: check 11 — discipline block drifts from canonical ───────────

echo ""
echo "Case 11: check 11 fires when an embedded discipline block drifts"
T="$(clone_baseline c11)"
mutate "$T/.claude/agents/backend-lead.md" "$DEVELOPER_RULES_TXT" "$DEVELOPER_RULES_TXT (locally edited, out of sync)"
REPO_ROOT="$T" bash "$GUARD" > "$TMP_BASE/c11.out" 2>&1
ge=$?
assert_exit_code "c11-exit" 1 "$ge"
assert_output_contains "c11-check" "check11:" "$TMP_BASE/c11.out"
assert_output_contains "c11-detail" "differs from canonical" "$TMP_BASE/c11.out"

# ─── Case 12: check 12 — dispatch template missing the Skill-tool load ─────

echo ""
echo "Case 12: check 12 fires when the dispatch template drops the Skill-tool instruction"
T="$(clone_baseline c12)"
# Backticks below must stay literal (matching the baseline fixture text).
# shellcheck disable=SC2016
mutate "$T/.claude/templates/plugin-reviewer-dispatch.md" \
  'Load `omnipus-shared-rules` with the Skill tool before you begin.' \
  'Load `omnipus-shared-rules` automatically before you begin.'
REPO_ROOT="$T" bash "$GUARD" > "$TMP_BASE/c12.out" 2>&1
ge=$?
assert_exit_code "c12-exit" 1 "$ge"
assert_output_contains "c12-check" "check12:" "$TMP_BASE/c12.out"
assert_output_contains "c12-detail" "does not instruct loading with the Skill tool" "$TMP_BASE/c12.out"

# ─── Case 13: check 13 — banned model: frontmatter key ─────────────────────

echo ""
echo "Case 13: check 13 fires on a model: key in agent frontmatter"
T="$(clone_baseline c13)"
old=$'  - omnipus-backend-rules\n---'
new=$'  - omnipus-backend-rules\nmodel: sonnet\n---'
mutate "$T/.claude/agents/backend-lead.md" "$old" "$new"
REPO_ROOT="$T" bash "$GUARD" > "$TMP_BASE/c13.out" 2>&1
ge=$?
assert_exit_code "c13-exit" 1 "$ge"
assert_output_contains "c13-check" "check13:" "$TMP_BASE/c13.out"
assert_output_contains "c13-detail" "banned 'model:' key" "$TMP_BASE/c13.out"

# ─── False-positive handling (design 8.2) ──────────────────────────────────

echo ""
echo "FP-a: the '# agent-guard: allow' marker suppresses a check-5 hit on that line"
T="$(clone_baseline fp_a)"
append_line "$T/.claude/agents/backend-lead.md" 'The exec allowlist controls this. # agent-guard: allow'
REPO_ROOT="$T" bash "$GUARD" > "$TMP_BASE/fpa.out" 2>&1
ge=$?
assert_exit_code "fpa-exit" 0 "$ge"
assert_output_not_contains "fpa-no-check5" "check5:" "$TMP_BASE/fpa.out"

echo ""
echo "FP-b: check 3's user-level allowlist passes a skill that is not on disk"
T="$(clone_baseline fp_b)"
# Backticks below must stay literal (a fixture skill citation).
# shellcheck disable=SC2016
append_line "$T/.claude/agents/backend-lead.md" 'Load the `webapp-testing` skill for browser tests.'
REPO_ROOT="$T" bash "$GUARD" > "$TMP_BASE/fpb.out" 2>&1
ge=$?
assert_exit_code "fpb-exit" 0 "$ge"
assert_output_not_contains "fpb-no-phantom" "cites skill 'webapp-testing'" "$TMP_BASE/fpb.out"

echo ""
echo "FP-c: check 13 is frontmatter-scoped — a body-text model: example is not flagged"
T="$(clone_baseline fp_c)"
append_line "$T/.claude/agents/backend-lead.md" 'Example frontmatter skeleton: model: opus (documentation only).'
REPO_ROOT="$T" bash "$GUARD" > "$TMP_BASE/fpc.out" 2>&1
ge=$?
assert_exit_code "fpc-exit" 0 "$ge"
assert_output_not_contains "fpc-no-check13" "check13:" "$TMP_BASE/fpc.out"

echo ""
echo "FP-d: check 7 reads only the teammates: list — a prose mention is not flagged"
T="$(clone_baseline fp_d)"
append_line "$T/.claude/agents/backend-lead.md" 'Coordinate with unicorn-role-zzz when needed.'
REPO_ROOT="$T" bash "$GUARD" > "$TMP_BASE/fpd.out" 2>&1
ge=$?
assert_exit_code "fpd-exit" 0 "$ge"
assert_output_not_contains "fpd-no-phantom" "unicorn-role-zzz" "$TMP_BASE/fpd.out"

echo ""
echo "FP-e: check 2 does not match a path-shaped token inside a longer absolute path"
T="$(clone_baseline fp_e)"
append_line "$T/.claude/agents/backend-lead.md" 'The ledger lives at /Users/example/AI-Agent-Workspace/omnipus-ledger/docs/state.md.'
REPO_ROOT="$T" bash "$GUARD" > "$TMP_BASE/fpe.out" 2>&1
ge=$?
assert_exit_code "fpe-exit" 0 "$ge"
assert_output_not_contains "fpe-no-state-md" "state.md" "$TMP_BASE/fpe.out"

echo ""
echo "FP-f: check 9's upstream-drift comparison is WARN-only and never fails the exit code"
T="$(clone_baseline fp_f)"
mutate "$T/.claude/skills/elicify-test-writing/SOURCE.yaml" \
  "source_repo: /tmp/fixture-elicify-skills-does-not-need-to-exist" \
  "source_repo: $TMP_BASE"
REPO_ROOT="$T" bash "$GUARD" > "$TMP_BASE/fpf.out" 2>&1
ge=$?
assert_exit_code "fpf-exit" 0 "$ge"
assert_output_contains "fpf-warn" "WARN check9:" "$TMP_BASE/fpf.out"

# ─── REAL: the real repository was never touched ───────────────────────────

echo ""
echo "REAL: real repo untouched (git status identical before/after)"
git -C "$REAL_ROOT" status --porcelain > "$TMP_BASE/real-after.txt" 2>/dev/null
if cmp -s "$TMP_BASE/real-before.txt" "$TMP_BASE/real-after.txt"; then
  echo "  PASS [real-untouched]: git status identical before/after"
  PASS=$((PASS + 1))
else
  echo "  FAIL [real-untouched]: git status changed during the run"
  FAIL=$((FAIL + 1))
  ERRORS+=("[real-untouched] real repo git status changed during the companion run")
fi

# Sanity: BACKEND_MD is only used to document the baseline's mutation target
# above; touch it so shellcheck does not flag it as unused if reordered.
: "$BACKEND_MD"

# ─── Summary ────────────────────────────────────────────────────────────────

echo ""
echo "─────────────────────────────────────────"
echo "Results: ${PASS} passed, ${FAIL} failed"

if [ "$FAIL" -gt 0 ]; then
  echo ""
  echo "Failures:"
  for e in "${ERRORS[@]:-}"; do
    [ -n "$e" ] && echo "  - $e"
  done
  exit 1
fi

echo "All assertions passed."
exit 0
