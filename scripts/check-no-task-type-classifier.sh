#!/usr/bin/env bash
# check-no-task-type-classifier.sh — ADR-084 revision 9 §Q / FR-110 mechanical guard.
#
# FR-110: "A criterion MUST NOT be required, preferred, rewarded or defaulted
# into carrying a check, and nothing may classify a goal as coding or
# non-coding." §Q states the principle in the operator's own frame: evidence
# comes in three universal tiers (the working agent's own tool-call record,
# a criterion's own DECLARED check, and the Judge reading files) that apply
# identically to a goal about a deck, a booking or an email as to one about
# code. "There is no task-type classifier anywhere in this design, and
# FR-110 forbids adding one."
#
# Unlike FR-109's guard (a fixed list of five named symbols that were once
# real and got deleted), FR-110 guards against something that has never
# existed in this tree and must never be ADDED — there is no fixed name to
# list. Per the judge spec's own FR-110 traceability row: "oracle: zero
# functions whose name or doc comment claims to decide whether a goal or
# criterion is a coding task. A blunt instrument, deliberately: the
# constraint is 'zero classifiers' and the cheapest way to keep it is to
# make adding one loud."
#
# ─── What this script actually checks ───────────────────────────────────────
# For every Go function declaration (`^func ...`, top-level or method) under
# the scan scope, this script looks at that declaration's NAME (the `func`
# line itself) and its DOC COMMENT (the contiguous run of `//` lines
# immediately above it, per the ordinary Go doc-comment convention — no
# blank line in between; a blank line, like any non-comment line, breaks the
# association). If that combined text contains the word "coding" adjacent
# to "task", "goal" or "criterion" (either order, optionally joined by a
# space or underscore — e.g. "CodingTask", "coding_goal", "criterion is
# coding"), the function is flagged: its name or its own documentation is
# making exactly the claim FR-110 forbids — that this goal/criterion IS or
# ISN'T "a coding task".
#
# This is deliberately narrower than a whole-file grep for "coding" near
# "task": scoping to a function's own name and its own doc comment (not its
# body, and not unrelated comments elsewhere in the file) is what FR-110's
# oracle literally asks for ("functions whose name or doc comment..."), and
# it is what keeps this guard from tripping on legitimate prose that
# discusses the RETIRED concept without implementing it (a top-of-file or
# mid-body explanatory comment not immediately attached to a func
# declaration is not scanned at all).
#
# Two known compound words are excluded before matching so they can never
# false-positive against "coding" as a bare substring: "encoding"/"decoding"
# (a totally unrelated concept — base64, URL encoding, character encoding —
# that legitimately contains the literal substring "coding") and
# "noncoding"/"non-coding"/"non coding" (the SANCTIONED way FR-111 and its
# regression tests refer to the class of goal this design explicitly
# supports without a classifier — e.g. the real
# TestNonCodingGoal_EmailCriterion_DecidedAtTierOne in
# pkg/agent/judge_noncoding_goal_adr084_test.go, which is proof this
# constraint IS met, not a violation of it). Verified empirically against
# the real tree while building this guard: pkg/routing/classifier.go and
# pkg/routing/features.go both say "coding tasks" in prose, but that prose
# is attached to a `type`/struct-field declaration (LLM routing model
# selection — a completely different "classifier" that predates and is
# unrelated to ADR-084), never to a `func` line, so this guard's
# func-scoped design does not and must not flag them.
#
# ─── Scope ───────────────────────────────────────────────────────────────────
# Hand-written `*.go` files under pkg/, cmd/ and tests/ — the Go-doc-comment
# convention FR-110's oracle describes ("name or doc comment") is a Go
# idiom, and ADR-084's Judge/goal engine is Go backend in full. Excluded:
# this script's own two files, generated artifacts (`/generated/`,
# `_generated`), and node_modules (present for parity with the other guards
# even though this scope never recurses into it).
#
# Exit: 0 clean, 1 offenders found, 2 the check itself could not run.
#
# TEST-ONLY OVERRIDE: CHECK_NO_TASK_TYPE_CLASSIFIER_AWK_OVERRIDE, if set,
# replaces the embedded awk program below with the file it names. It exists
# solely so check-no-task-type-classifier.test.sh can inject a deliberately
# broken awk program to prove a real awk failure is reported as exit 2
# rather than a swallowed false "OK". Never set in CI/Makefile/pr.yml.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="${REPO_ROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"
cd "$REPO_ROOT" || { echo "check-no-task-type-classifier: cannot cd to $REPO_ROOT" >&2; exit 2; }

SCAN_DIRS=(pkg cmd tests)
for d in "${SCAN_DIRS[@]}"; do
  if [ ! -d "$d" ]; then
    echo "check-no-task-type-classifier: expected directory '$d' not found under $REPO_ROOT" >&2
    echo "  (wrong cwd, a renamed package, or a partial checkout — refusing to report a green" >&2
    echo "   verdict for a tree this script never actually scanned)" >&2
    exit 2
  fi
done

AWK_PROGRAM_FILE="$(mktemp "${TMPDIR:-/tmp}/check-no-task-type-classifier-awk.XXXXXX")"
trap 'rm -f "$AWK_PROGRAM_FILE"' EXIT

cat > "$AWK_PROGRAM_FILE" << 'AWKEOF'
function reset() { commentBuf = "" }
FNR == 1 { reset() }
{
  line = $0
  trimmed = line
  sub(/^[ \t]+/, "", trimmed)
  is_comment = (trimmed ~ /^\/\//)
  is_func = (line ~ /^func /) || (line ~ /^func\(/)
  if (is_func) {
    combined = commentBuf "\n" line
    lc = tolower(combined)
    clean = lc
    gsub(/encoding/, "", clean)
    gsub(/decoding/, "", clean)
    gsub(/non-coding/, "", clean)
    gsub(/noncoding/, "", clean)
    gsub(/non coding/, "", clean)
    if (clean ~ /coding[ _]*(task|goal|criterion)/ || clean ~ /(task|goal|criterion)[ _]*coding/) {
      print FILENAME ":" FNR ":" line
    }
    reset()
  } else if (is_comment) {
    commentBuf = commentBuf "\n" line
  } else if (trimmed == "") {
    reset()
  } else {
    reset()
  }
}
AWKEOF

# Test-only override — see the note above the Exit line. A SEPARATE variable
# is used for the file actually passed to awk (rather than overwriting
# AWK_PROGRAM_FILE in place) so the EXIT trap only ever deletes the temp
# file this script created, never a fixture file a caller supplied.
ACTIVE_AWK_PROGRAM="$AWK_PROGRAM_FILE"
if [ -n "${CHECK_NO_TASK_TYPE_CLASSIFIER_AWK_OVERRIDE:-}" ]; then
  ACTIVE_AWK_PROGRAM="$CHECK_NO_TASK_TYPE_CLASSIFIER_AWK_OVERRIDE"
fi

FILE_LIST="$(mktemp "${TMPDIR:-/tmp}/check-no-task-type-classifier-files.XXXXXX")"
trap 'rm -f "$AWK_PROGRAM_FILE" "$FILE_LIST"' EXIT

find "${SCAN_DIRS[@]}" -name '*.go' \
  -not -path '*/generated/*' \
  -not -name '*_generated*' \
  -not -path '*/node_modules/*' \
  > "$FILE_LIST"

if [ ! -s "$FILE_LIST" ]; then
  echo "check-no-task-type-classifier: discovered zero .go files under ${SCAN_DIRS[*]}" >&2
  echo "  (a broken find invocation reporting a green verdict over nothing scanned is a" >&2
  echo "   silent false pass, not a clean tree)" >&2
  exit 2
fi

AWK_STDERR_FILE="$(mktemp "${TMPDIR:-/tmp}/check-no-task-type-classifier-awkerr.XXXXXX")"
trap 'rm -f "$AWK_PROGRAM_FILE" "$FILE_LIST" "$AWK_STDERR_FILE"' EXIT

# `xargs -a FILE` is a GNU extension unavailable on BSD/macOS xargs — read
# the file list from stdin instead, which both implementations support.
hits="$(xargs awk -f "$ACTIVE_AWK_PROGRAM" < "$FILE_LIST" 2>"$AWK_STDERR_FILE")"
awk_status=$?
if [ "$awk_status" -ne 0 ]; then
  echo "check-no-task-type-classifier: awk scan failed (exit $awk_status)" >&2
  cat "$AWK_STDERR_FILE" >&2
  exit 2
fi

# The two files this guard ships as are excluded by construction (they are
# shell, not *.go, so the find above never selects them) — nothing further
# to strip.

if [ -n "$hits" ]; then
  echo "ERROR: a function whose name or doc comment claims to decide whether a goal or" >&2
  echo "criterion is a coding task was found — FR-110 forbids this outright:" >&2
  echo "" >&2
  echo "$hits" >&2
  echo "" >&2
  echo "ADR-084 revision 9 §Q: there is no task-type classifier anywhere in this design." >&2
  echo "Evidence comes in three universal tiers (the tool-call record, a criterion's own" >&2
  echo "DECLARED check, and the Judge reading files) that apply identically to a goal" >&2
  echo "about a deck, a booking or an email as to one about code. A criterion must never" >&2
  echo "be required, preferred, rewarded or defaulted into carrying a check, and nothing" >&2
  echo "may classify a goal as coding or non-coding." >&2
  echo "If this came from a merge, resolve by KEEPING THE ABSENCE — see" >&2
  echo "docs/internal/architecture/ADR-084-judge-as-an-active-reviewer.md." >&2
  exit 1
fi

echo "OK: no task-type classifier found (zero functions whose name or doc comment"
echo "claims to decide whether a goal or criterion is a coding task)."
