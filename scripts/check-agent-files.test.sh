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
#   14    (review C9) a tools: allow-list that omits Skill fires
#   2b    (review C8) a dead path under ui/ is caught (design's own F9 example)
#   3b    (review C5) a role's skills: frontmatter omitting its required
#         preload fires, even though the skill name appears elsewhere in
#         the file text
#   4b    (review C7) a TAGGED whole-repo `go test ./...` is banned too —
#         not just the untagged form
#   4c    (review C7) `tsc --noEmit` without `-b` is banned
#   4d    (review C7) a lowercase `Co-authored-by: Claude` trailer is banned
#   6b    (review C4) check 6 fires on a foreign-skill citation even when
#         the line never contains the word "skill"
#   9b    (review C2) check 9's drift WARN fires from a real local_clone
#         git comparison when the upstream branch has moved
#   11b   (review C6) an agent file outside every role-classification list
#         fires "role not classified" instead of silently passing
#   12b   (F1 hand-off, finding A13) the dispatch template's shared-traits
#         block is now diffed byte for byte against canonical too, not
#         just reviewer-rules
#   FP-a  allow-marker suppresses a check-5 hit on the marked line
#   FP-b  check 3's user-level skill allowlist (webapp-testing) passes
#         without existing on disk
#   FP-c  check 13 is frontmatter-scoped: a body-text "model:" example is
#         never flagged
#   FP-d  check 7 reads only the teammates: frontmatter list; a prose
#         mention of a phantom role name is never flagged
#   FP-e  check 2 never matches a path-shaped token embedded inside a
#         longer absolute, outside-repo path
#   FP-f  check 9's local_clone drift comparison is best-effort: an
#         unreachable/non-git clone path never fails the exit code or
#         produces a finding
#   FP-g  (review C3, narrowed) a vendored directory (SOURCE.yaml present)
#         is exempt from check 8 ONLY — content checks 2/4/5/10 now scan
#         every file in the tree, including knowledge/ subfiles
#   FP-h  the allow-marker suppresses a check-6 isolation hit (design 8.2
#         names check 6 as marker-covered)
#   FP-i  (review C7) `tsc -b --noEmit` (the correct wrapped form) is not
#         flagged
#   FP-j  (review C4) an ordinary backtick role mention (e.g. `qa-lead`) is
#         never treated as a skill citation
#   FP-k  (review C9) a tools: allow-list that DOES name Skill is not flagged
#   FP-l  (review C2) no drift WARN when the local_clone tip matches the
#         pinned source_commit
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

<!-- agent-discipline:shared-traits:start -->
$SHARED_TRAITS_TXT
<!-- agent-discipline:shared-traits:end -->

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

# ─── Case 2b: check 2 (review C8) — dead path under ui/ or .github/ ───────

echo ""
echo "Case 2b (C8): check 2 fires on a dead path under ui/ (design's own F9 example)"
T="$(clone_baseline c2b)"
append_line "$T/.claude/agents/backend-lead.md" 'See ui/src/components/Example.tsx for the pattern.'
REPO_ROOT="$T" bash "$GUARD" > "$TMP_BASE/c2b.out" 2>&1
ge=$?
assert_exit_code "c2b-exit" 1 "$ge"
assert_output_contains "c2b-check" "check2:" "$TMP_BASE/c2b.out"
assert_output_contains "c2b-detail" "ui/src/components/Example.tsx" "$TMP_BASE/c2b.out"

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

# ─── Case 3b: check 3 (review C5) — required preload missing ──────────────

echo ""
echo "Case 3b (C5): check 3 fires when a role's skills: frontmatter omits its required preload"
T="$(clone_baseline c3b)"
mutate "$T/.claude/agents/backend-lead.md" $'  - omnipus-backend-rules\n' ''
REPO_ROOT="$T" bash "$GUARD" > "$TMP_BASE/c3b.out" 2>&1
ge=$?
assert_exit_code "c3b-exit" 1 "$ge"
assert_output_contains "c3b-check" "check3:" "$TMP_BASE/c3b.out"
assert_output_contains "c3b-detail" "'omnipus-backend-rules' is not preloaded in the skills:" "$TMP_BASE/c3b.out"

# ─── Case 4: check 4 — banned command pattern ──────────────────────────────

echo ""
echo "Case 4: check 4 fires on a whole-repo 'go build ./...' instruction"
T="$(clone_baseline c4)"
append_line "$T/.claude/agents/backend-lead.md" 'Run go build ./... before committing.'
REPO_ROOT="$T" bash "$GUARD" > "$TMP_BASE/c4.out" 2>&1
ge=$?
assert_exit_code "c4-exit" 1 "$ge"
assert_output_contains "c4-check" "check4:" "$TMP_BASE/c4.out"
assert_output_contains "c4-detail" "go build ./... (whole-repo target)" "$TMP_BASE/c4.out"

# ─── Case 4b: check 4 (review C7) — TAGGED whole-repo run is banned too ───

echo ""
echo "Case 4b (C7): check 4 fires on a TAGGED whole-repo 'go test ./...' too"
T="$(clone_baseline c4b)"
append_line "$T/.claude/agents/backend-lead.md" 'Run go test -tags goolm,stdjson ./... before committing.'
REPO_ROOT="$T" bash "$GUARD" > "$TMP_BASE/c4b.out" 2>&1
ge=$?
assert_exit_code "c4b-exit" 1 "$ge"
assert_output_contains "c4b-check" "check4:" "$TMP_BASE/c4b.out"
assert_output_contains "c4b-detail" "go test ./... (whole-repo target)" "$TMP_BASE/c4b.out"

# ─── Case 4c: check 4 (review C7) — bare 'tsc --noEmit' (no -b) is banned ──

echo ""
echo "Case 4c (C7): check 4 fires on 'tsc --noEmit' without -b"
T="$(clone_baseline c4c)"
append_line "$T/.claude/agents/backend-lead.md" 'Just run tsc --noEmit to check types.'
REPO_ROOT="$T" bash "$GUARD" > "$TMP_BASE/c4c.out" 2>&1
ge=$?
assert_exit_code "c4c-exit" 1 "$ge"
assert_output_contains "c4c-check" "check4:" "$TMP_BASE/c4c.out"
assert_output_contains "c4c-detail" "tsc --noEmit without -b" "$TMP_BASE/c4c.out"

echo ""
echo "FP-i (C7): 'tsc -b --noEmit' (the correct wrapped form) is not flagged"
T="$(clone_baseline fp_i)"
append_line "$T/.claude/agents/backend-lead.md" 'Use npm run typecheck (wired to tsc -b --noEmit).'
REPO_ROOT="$T" bash "$GUARD" > "$TMP_BASE/fpi.out" 2>&1
ge=$?
assert_exit_code "fpi-exit" 0 "$ge"
assert_output_not_contains "fpi-no-check4" "check4:" "$TMP_BASE/fpi.out"

# ─── Case 4d: check 4 (review C7) — lowercase AI trailer is banned too ────

echo ""
echo "Case 4d (C7): check 4 fires on a lowercase 'Co-authored-by: Claude' trailer"
T="$(clone_baseline c4d)"
append_line "$T/.claude/agents/backend-lead.md" 'Co-authored-by: Claude <noreply@anthropic.com>'
REPO_ROOT="$T" bash "$GUARD" > "$TMP_BASE/c4d.out" 2>&1
ge=$?
assert_exit_code "c4d-exit" 1 "$ge"
assert_output_contains "c4d-check" "check4:" "$TMP_BASE/c4d.out"
assert_output_contains "c4d-detail" "AI co-author trailer instruction" "$TMP_BASE/c4d.out"

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

# ─── Case 6b: check 6 (review C4) — isolation without the word "skill" ────

echo ""
echo "Case 6b (C4): check 6 fires on a foreign-skill citation even without the word 'skill' on the line"
T="$(clone_baseline c6b)"
# Backticks below must stay literal (a fixture skill citation).
# shellcheck disable=SC2016
append_line "$T/.claude/agents/backend-lead.md" 'See `omnipus-frontend-rules` for CSS conventions.'
REPO_ROOT="$T" bash "$GUARD" > "$TMP_BASE/c6b.out" 2>&1
ge=$?
assert_exit_code "c6b-exit" 1 "$ge"
assert_output_contains "c6b-check" "check6:" "$TMP_BASE/c6b.out"
assert_output_contains "c6b-detail" "outside role 'backend-lead' allowed set" "$TMP_BASE/c6b.out"

echo ""
echo "FP-j (C4): an ordinary backtick role mention is never treated as a skill citation"
T="$(clone_baseline fp_j)"
# Backticks below must stay literal (an ordinary role mention, not a skill).
# shellcheck disable=SC2016
append_line "$T/.claude/agents/backend-lead.md" 'Coordinate with `qa-lead` on the test plan.'
REPO_ROOT="$T" bash "$GUARD" > "$TMP_BASE/fpj.out" 2>&1
ge=$?
assert_exit_code "fpj-exit" 0 "$ge"
assert_output_not_contains "fpj-no-check3" "cites skill 'qa-lead'" "$TMP_BASE/fpj.out"

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

# ─── Case 9b: check 9 (review C2) — real local_clone drift comparison ─────
# The old drift check tested os.path.isdir(source_repo), which is always
# False since source_repo is an https:// URL — always dead. This proves
# the replacement: a real local git clone whose branch has moved past the
# pinned source_commit produces a WARN.

echo ""
echo "Case 9b (C2): check 9's drift WARN fires from a real local_clone git comparison"
T="$(clone_baseline c9b)"
UPSTREAM_C9B="$TMP_BASE/upstream-c9b"
mkdir -p "$UPSTREAM_C9B"
git -C "$UPSTREAM_C9B" init -q -b main
git -C "$UPSTREAM_C9B" config user.email "fixture@example.com"
git -C "$UPSTREAM_C9B" config user.name "Fixture"
echo "v1" > "$UPSTREAM_C9B/f.txt"
git -C "$UPSTREAM_C9B" add -A
git -C "$UPSTREAM_C9B" commit -q -m "v1"
OLD_SHA_C9B="$(git -C "$UPSTREAM_C9B" rev-parse HEAD)"
echo "v2" > "$UPSTREAM_C9B/f.txt"
git -C "$UPSTREAM_C9B" add -A
git -C "$UPSTREAM_C9B" commit -q -m "v2"
mutate "$T/.claude/skills/elicify-test-writing/SOURCE.yaml" "source_commit: abc1234" "source_commit: $OLD_SHA_C9B"
append_line "$T/.claude/skills/elicify-test-writing/SOURCE.yaml" "local_clone: $UPSTREAM_C9B"
REPO_ROOT="$T" bash "$GUARD" > "$TMP_BASE/c9b.out" 2>&1
ge=$?
assert_exit_code "c9b-exit" 0 "$ge"
assert_output_contains "c9b-warn" "WARN check9:" "$TMP_BASE/c9b.out"
assert_output_contains "c9b-detail" "re-copy and diff" "$TMP_BASE/c9b.out"

echo ""
echo "FP-l (C2): no drift WARN when the local_clone tip matches the pinned source_commit"
T="$(clone_baseline fp_l)"
UPSTREAM_FPL="$TMP_BASE/upstream-fpl"
mkdir -p "$UPSTREAM_FPL"
git -C "$UPSTREAM_FPL" init -q -b main
git -C "$UPSTREAM_FPL" config user.email "fixture@example.com"
git -C "$UPSTREAM_FPL" config user.name "Fixture"
echo "v1" > "$UPSTREAM_FPL/f.txt"
git -C "$UPSTREAM_FPL" add -A
git -C "$UPSTREAM_FPL" commit -q -m "v1"
SHA_FPL="$(git -C "$UPSTREAM_FPL" rev-parse HEAD)"
mutate "$T/.claude/skills/elicify-test-writing/SOURCE.yaml" "source_commit: abc1234" "source_commit: $SHA_FPL"
append_line "$T/.claude/skills/elicify-test-writing/SOURCE.yaml" "local_clone: $UPSTREAM_FPL"
REPO_ROOT="$T" bash "$GUARD" > "$TMP_BASE/fpl.out" 2>&1
ge=$?
assert_exit_code "fpl-exit" 0 "$ge"
assert_output_not_contains "fpl-no-warn" "WARN check9:" "$TMP_BASE/fpl.out"

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

# ─── Case 11b: check 11 (review C6) — unclassified role fails open no more ─

echo ""
echo "Case 11b (C6): check 11 fires when a new agent file is not in any role-classification list"
T="$(clone_baseline c11b)"
cat > "$T/.claude/agents/perf-lead.md" <<'EOF'
---
name: perf-lead
description: Fixture unclassified role.
skills:
  - omnipus-shared-rules
---

# perf-lead (fixture)

Last reviewed: 2026-09-25
EOF
REPO_ROOT="$T" bash "$GUARD" > "$TMP_BASE/c11b.out" 2>&1
ge=$?
assert_exit_code "c11b-exit" 1 "$ge"
assert_output_contains "c11b-check" "check11:" "$TMP_BASE/c11b.out"
assert_output_contains "c11b-detail" "role not classified" "$TMP_BASE/c11b.out"

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

# ─── Case 12b: check 12 (F1 hand-off, finding A13) — shared-traits block ──
# in the dispatch template must also byte-match the canonical source, not
# just reviewer-rules.

echo ""
echo "Case 12b (A13): check 12 fires when the dispatch template's shared-traits block drifts from canonical"
T="$(clone_baseline c12b)"
mutate "$T/.claude/templates/plugin-reviewer-dispatch.md" "$SHARED_TRAITS_TXT" "$SHARED_TRAITS_TXT (locally edited, out of sync)"
REPO_ROOT="$T" bash "$GUARD" > "$TMP_BASE/c12b.out" 2>&1
ge=$?
assert_exit_code "c12b-exit" 1 "$ge"
assert_output_contains "c12b-check" "check12:" "$TMP_BASE/c12b.out"
assert_output_contains "c12b-detail" "'shared-traits' discipline block differs from canonical" "$TMP_BASE/c12b.out"

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

# ─── Case 14: new check (review C9) — tools: allow-list omitting Skill ────

echo ""
echo "Case 14 (C9): check 14 fires when a tools: allow-list omits Skill"
T="$(clone_baseline c14)"
old=$'  - omnipus-backend-rules\n---'
new=$'  - omnipus-backend-rules\ntools: Read, Grep, Glob, Edit\n---'
mutate "$T/.claude/agents/backend-lead.md" "$old" "$new"
REPO_ROOT="$T" bash "$GUARD" > "$TMP_BASE/c14.out" 2>&1
ge=$?
assert_exit_code "c14-exit" 1 "$ge"
assert_output_contains "c14-check" "check14:" "$TMP_BASE/c14.out"
assert_output_contains "c14-detail" "does not name Skill" "$TMP_BASE/c14.out"

echo ""
echo "FP-k (C9): a tools: allow-list that DOES name Skill is not flagged"
T="$(clone_baseline fp_k)"
old=$'  - omnipus-backend-rules\n---'
new=$'  - omnipus-backend-rules\ntools: Read, Grep, Glob, Edit, Skill\n---'
mutate "$T/.claude/agents/backend-lead.md" "$old" "$new"
REPO_ROOT="$T" bash "$GUARD" > "$TMP_BASE/fpk.out" 2>&1
ge=$?
assert_exit_code "fpk-exit" 0 "$ge"
assert_output_not_contains "fpk-no-check14" "check14:" "$TMP_BASE/fpk.out"

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
echo "FP-f (C2): check 9's local_clone drift comparison is best-effort — an unreachable/non-git clone never fails the exit code or produces a finding"
T="$(clone_baseline fp_f)"
append_line "$T/.claude/skills/elicify-test-writing/SOURCE.yaml" "local_clone: $TMP_BASE/does-not-exist-fpf"
REPO_ROOT="$T" bash "$GUARD" > "$TMP_BASE/fpf.out" 2>&1
ge=$?
assert_exit_code "fpf-exit" 0 "$ge"
assert_output_not_contains "fpf-no-check9-finding" "check9:" "$TMP_BASE/fpf.out"

echo ""
echo "FP-g (C3, narrowed): a vendored directory is exempt from check 8 ONLY — content checks 2/4/5/10 now scan every file in the tree, including knowledge/ subfiles"
T="$(clone_baseline fp_g)"
mkdir -p "$T/.claude/skills/elicify-test-writing/knowledge"
cat > "$T/.claude/skills/elicify-test-writing/knowledge/bad-example.md" <<'EOF'
See docs/does-not-exist-vendored.md and run go build ./... and mind the exec allowlist and release/v0.1.1.
EOF
REPO_ROOT="$T" bash "$GUARD" > "$TMP_BASE/fpg.out" 2>&1
ge=$?
assert_exit_code "fpg-exit" 1 "$ge"
assert_output_contains "fpg-check2" "does-not-exist-vendored" "$TMP_BASE/fpg.out"
assert_output_contains "fpg-check4" "go build ./... (whole-repo target)" "$TMP_BASE/fpg.out"
assert_output_contains "fpg-check5" "check5:" "$TMP_BASE/fpg.out"
assert_output_contains "fpg-check10" "hard-coded integration branch" "$TMP_BASE/fpg.out"
# still exempt from check 8 — the vendored SKILL.md itself still carries no
# Last reviewed header, and that must not fire.
assert_output_not_contains "fpg-still-no-check8" "check8: .claude/skills/elicify-test-writing/SKILL.md" "$TMP_BASE/fpg.out"

echo ""
echo "FP-h: the '# agent-guard: allow' marker suppresses a check-6 isolation hit (design 8.2 lists check 6)"
T="$(clone_baseline fp_h)"
# Backticks below must stay literal (a fixture skill citation).
# shellcheck disable=SC2016
append_line "$T/.claude/agents/backend-lead.md" \
  'qa-lead on CHECK also loads the `omnipus-frontend-rules` skill in this sentence only to describe it, never to load it itself. # agent-guard: allow'
REPO_ROOT="$T" bash "$GUARD" > "$TMP_BASE/fph.out" 2>&1
ge=$?
assert_exit_code "fph-exit" 0 "$ge"
assert_output_not_contains "fph-no-check6" "check6:" "$TMP_BASE/fph.out"

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
