#!/usr/bin/env bash
# check-spec-status.sh
#
# Guard: every NEW or CHANGED docs/internal/specs/*-spec.md file must carry
# a `Status:` line naming one of the five values the spec-process rewrite
# fixed (founder decision, spec-process/DECISIONS.md #7): Draft, In review,
# Approved, Implemented, Superseded.
#
# SCOPE — NEW/CHANGED ONLY, NEVER THE WHOLE TREE
#
#   This repo already carries ~83 *-spec.md files (of ~211 docs under
#   docs/internal/specs/) from before this field existed; as of this guard's
#   introduction only one of them (library-spec.md) has any "Status:" line
#   at all, and its line is free text, not one of the five values. A guard
#   that scanned every existing spec would go red on all of them on day
#   one — that is explicitly NOT this guard's job (spec-process/LANES.md
#   S5 task 4: "do NOT fail on the 214 existing specs"). It only checks
#   files that are NEW or CHANGED relative to the merge base with the
#   integration branch — i.e., what this PR/branch actually touches.
#
# DETERMINING THE INTEGRATION BRANCH
#
#   The integration branch is never a literal baked into enforcement here
#   (team-lead.md / squad-lead.md: "it is never hard-coded, it changes over
#   time" — and guard check 10 in check-agent-files.sh separately bans a
#   hard-coded release/vN... branch literal under .claude/). Resolution
#   order, first match wins:
#     1. CHECK_SPEC_STATUS_BASE_REF   — explicit override (this script's
#                                        own self-test, or a caller that
#                                        already knows the ref)
#     2. GITHUB_BASE_REF              — auto-populated by GitHub Actions
#                                        for pull_request-triggered runs;
#                                        no workflow-file wiring needed
#     3. OMNIPUS_INTEGRATION_BRANCH   — the coordination-ledger convention
#                                        scripts/hooks/pre-push-ledger-check
#                                        already reads the same way
#     4. main                         — last-resort fallback; this repo's
#                                        actual default branch today (see
#                                        git status), not a policy literal
#
#   The resolved ref is then turned into a commit git can diff against:
#   the ref itself if it already resolves locally, else origin/<ref> if
#   already fetched, else a shallow `git fetch --depth=1 origin <ref>` —
#   CI's checkout (.github/workflows/pr.yml) uses actions/checkout@v7's
#   default fetch-depth (1, the PR merge commit only), so the base
#   branch's history is not present without this fetch.
#
#   FAIL-OPEN, NOT FAIL-CLOSED, WHEN THE BASE CANNOT BE RESOLVED: no local
#   ref, no origin/<ref>, and the fetch fails (no network, ref renamed,
#   running outside a clone with an `origin` remote at all). Scope cannot
#   be determined, so nothing is checked — a WARNING is printed and the
#   guard exits 0. Widening to "scan everything" instead would immediately
#   fail on the pre-existing specs this guard is explicitly not supposed to
#   touch, which is a worse failure mode than staying silent this one run
#   (mirrors scripts/hooks/pre-push-ledger-check's own unset-env behaviour:
#   WARNING + allow, never a block on missing configuration).
#
# WHAT COUNTS AS "CHANGED"
#
#   `git diff --name-only --diff-filter=ACM <base> -- docs/internal/specs/
#   *-spec.md` against the working tree (a single ref given to `git diff`
#   compares base to the on-disk working tree, so this also catches
#   uncommitted local edits, not only committed ones). Deletions (D) are
#   excluded — a removed spec has nothing to check. Renames are not
#   requested explicitly (`-M`): without it, git reports a rename as a
#   plain delete + add, and the add side already matches the pathspec and
#   gets checked — simpler than rename-tracking and the same outcome for
#   this guard's purpose.
#
# WHAT "CARRIES A STATUS LINE" MEANS
#
#   A line, once `*` characters are stripped (so `**Status:** Draft` and
#   `Status: **Draft**` both match), of the shape `Status: <value>` where
#   <value> is exactly one of Draft | In review | Approved | Implemented |
#   Superseded — nothing else on the line. This is deliberately narrower
#   than "the word Status appears somewhere": docs/internal/specs/
#   library-spec.md's existing free-text "Status: driving implementation
#   on `feat/library`..." line is exactly the shape this guard does NOT
#   accept, because DECISIONS.md #7 fixes the field to one of five values,
#   not prose.
#
# OUTPUT CONTRACT (mirrors scripts/check-agent-files.sh)
#
#   One line per finding: "check-spec-status: <path>: <problem>". Exit 0
#   clean (including "scope undetermined" and "nothing changed"), 1 on any
#   finding, 2 on internal error (bad REPO_ROOT, not a git repo).
#
# DISCOVERY (scripts/guards.sh conventions)
#
#   This file is discovered by scripts/guards.sh's scripts/check-*.sh glob.
#   Its proof-of-failure companion is scripts/check-spec-status.test.sh (a
#   <name>.test.sh companion, subtracted from the guard list and run before
#   this guard, per scripts/guards.sh's own rules). No other wiring change
#   — adding this guard is these two files, nothing more.
#
# Usage:
#   bash scripts/check-spec-status.sh
#
# Overrides (for the companion test — never set these in CI):
#   REPO_ROOT                    — repo root to scan
#   CHECK_SPEC_STATUS_BASE_REF   — explicit base ref, skips GITHUB_BASE_REF/
#                                  OMNIPUS_INTEGRATION_BRANCH/main resolution

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" && pwd)"
REPO_ROOT="${REPO_ROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"
SPECS_PATHSPEC="docs/internal/specs/*-spec.md"
VALID_STATUSES="Draft|In review|Approved|Implemented|Superseded"

if [ ! -d "$REPO_ROOT" ]; then
  echo "check-spec-status: ERROR — REPO_ROOT is not a directory: $REPO_ROOT" >&2
  exit 2
fi

if [ ! -e "$REPO_ROOT/.git" ]; then
  echo "check-spec-status: ERROR — REPO_ROOT is not a git repository (no .git): $REPO_ROOT" >&2
  exit 2
fi

cd "$REPO_ROOT" || exit 2

# ─── Resolve the base ref name (never the commit yet) ──────────────────────

BASE_REF="${CHECK_SPEC_STATUS_BASE_REF:-}"
if [ -z "$BASE_REF" ] && [ -n "${GITHUB_BASE_REF:-}" ]; then
  BASE_REF="$GITHUB_BASE_REF"
fi
if [ -z "$BASE_REF" ] && [ -n "${OMNIPUS_INTEGRATION_BRANCH:-}" ]; then
  BASE_REF="$OMNIPUS_INTEGRATION_BRANCH"
fi
if [ -z "$BASE_REF" ]; then
  BASE_REF="main"
fi

# ─── Resolve the base ref to a commit, fetching shallowly if needed ────────

resolve_base_commit() {
  local ref="$1"
  if git rev-parse --verify --quiet "$ref" >/dev/null 2>&1; then
    git rev-parse --verify --quiet "$ref"
    return 0
  fi
  if git rev-parse --verify --quiet "origin/$ref" >/dev/null 2>&1; then
    git rev-parse --verify --quiet "origin/$ref"
    return 0
  fi
  if git remote get-url origin >/dev/null 2>&1; then
    if git fetch --quiet --depth=1 origin "$ref" >/dev/null 2>&1; then
      if git rev-parse --verify --quiet FETCH_HEAD >/dev/null 2>&1; then
        git rev-parse --verify --quiet FETCH_HEAD
        return 0
      fi
    fi
  fi
  return 1
}

BASE_COMMIT="$(resolve_base_commit "$BASE_REF" || true)"

if [ -z "$BASE_COMMIT" ]; then
  echo "check-spec-status: WARNING — cannot resolve integration branch '$BASE_REF' to a commit (no local ref, no origin/$BASE_REF, and the shallow fetch failed or there is no 'origin' remote) — the set of NEW/changed specs cannot be determined, so nothing is checked this run. This never widens to scanning every existing spec." >&2
  echo "check-spec-status: OK — 0 findings (scope undetermined, fail-open by design)"
  exit 0
fi

# ─── Discover NEW/CHANGED *-spec.md files against the base commit ─────────

CHANGED_FILES="$(git diff --name-only --diff-filter=ACM "$BASE_COMMIT" -- "$SPECS_PATHSPEC" 2>/dev/null || true)"

if [ -z "$CHANGED_FILES" ]; then
  echo "check-spec-status: OK — 0 new/changed spec files against $BASE_REF ($BASE_COMMIT)"
  exit 0
fi

STATUS_RE='^[[:space:]]*Status:[[:space:]]*('"$VALID_STATUSES"')[[:space:]]*$'
findings=0
checked=0

while IFS= read -r rel; do
  [ -z "$rel" ] && continue
  path="$REPO_ROOT/$rel"
  if [ ! -f "$path" ]; then
    continue
  fi
  checked=$((checked + 1))
  if ! sed 's/\*//g' "$path" | grep -Eq "$STATUS_RE"; then
    echo "check-spec-status: $rel: missing a 'Status: <value>' line with one of Draft | In review | Approved | Implemented | Superseded"
    findings=$((findings + 1))
  fi
done <<EOF
$CHANGED_FILES
EOF

if [ "$findings" -gt 0 ]; then
  echo "check-spec-status: $findings finding(s) across $checked new/changed spec(s) (base $BASE_REF, $BASE_COMMIT)"
  exit 1
fi

echo "check-spec-status: OK — 0 findings ($checked new/changed spec(s) checked against $BASE_REF, $BASE_COMMIT)"
exit 0
