#!/usr/bin/env bash
# check-no-browser-turn-park.sh — ADR-085 Explicit Non-Behaviors mechanical
# guard (wave B9, docs/internal/specs/adr-084-086-joint-delivery-plan.md §3,
# row B9; C-77; R-19d).
#
# ADR-085's whole point is D4: an operator taking the browser wheel DEFERS
# the agent's browser calls and tells it to wait — it never cancels,
# interrupts, suspends or parks the turn, and the resuming turn is an
# ordinary turn with no dispatcher, no injected state, and nothing
# persisted. Four things are explicitly, permanently out of scope (the
# spec's own words, "Explicit Non-Behaviors & Safeguards" section):
#
#   (1) a control TOGGLE affordance ("Take control" / "Release control" /
#       "Hand to agent" buttons) — retired once already (ADR-040 D1/D2,
#       formerly ADR-038), and this delivery must not reinstate it.
#   (2) a RESUME DISPATCHER for the browser wheel.
#   (3) a FRAME-PERSISTENCE PATH (a persisted handover record) — "there is
#       nothing to recover: the resume is an ordinary message."
#   (4) a TURN-PARK OR CANCEL SIGNAL fired by a browser take-over.
#
# None of (2), (3) or (4) was ever built — they were rejected at design time
# (see the spec's A13 and its "Explicit Non-Behaviors" section) — so unlike
# check-no-orphan-turn-watchdog.sh (which guards the exact names of a REAL,
# deleted implementation), there is no historical symbol name to pin for
# (2)-(4). This script therefore guards the SHAPE most likely to reappear:
#
#   Clause (a) — the control-toggle STRINGS, which DID exist once
#     (ADR-038/040) and are therefore exactly the shape a bad merge from a
#     pre-ADR-040 branch resurrects as an ordinary, conflict-free addition.
#     Scoped to src/components/browser/ production (non-test) files, per
#     C-77's own wording.
#   Clause (b) — a RESUME-DISPATCHER-shaped Go identifier in pkg/agent/: one
#     token combining "resume" with "browser"/"wheel"/"handover" (case-
#     insensitive), e.g. ResumeBrowserWheel, browserWheelResumeDispatcher,
#     HandoverResumeAction. Deliberately identifier-scoped, not line-scoped:
#     pkg/agent/browser_deferral.go's own ForLLM strings legitimately say
#     "the operator resumes your browser driving" in PROSE (two separate
#     words, space-separated) — this must never be a match, only a single
#     camelCase/PascalCase TOKEN combining the two concepts is.
#   Clause (c) — the four turn-park/cancel entry points this codebase
#     actually has (pkg/agent/cancel.go::RequestCancel,
#     RequestCancelForSession, RequestCancelByChannelChat,
#     pkg/agent/steering.go::InterruptSessionHard — the ADR-057 cancel state
#     machine CLAUDE.md names as "the only way a turn ends early, and only
#     on an explicit Stop/cancel surface") must not be CALLED from
#     pkg/tools/browser/ or pkg/agent/browser_deferral.go, per R-19d's exact
#     scoping. REACHABILITY IS OUT OF SCOPE: this is a literal call-site
#     scan of two locations, not a whole-program call-graph proof that no
#     path from a browser take reaches these functions transitively through
#     other files — R-19d records that limitation explicitly rather than
#     silently overclaiming it.
#
# No B123 handoff comment naming a symbol list for clause (c) was found in
# the tree at the time this guard was written (searched pkg/agent/
# browser_deferral.go and every pkg/tools/browser/*.go file for "B9",
# "symbol list", "hand", "lane 3" — zero hits). The four names above were
# derived independently from pkg/agent/cancel.go and pkg/agent/steering.go
# (CLAUDE.md's own description of "the ADR-057 cancel state machine") and
# verified today to have ZERO call sites in either scanned location — the
# expected clean state. Reported per standing rule 17.
#
# Scope: clause (a) is src/*.tsx,*.ts under src/components/browser/,
# excluding *.test.tsx/*.test.ts. Clauses (b) and (c) are hand-written Go
# under pkg/agent/ (b) and pkg/tools/browser/ + pkg/agent/browser_deferral.go
# (c), excluding *_test.go. Comment-only lines never count (a retirement
# comment naming these things in prose is the sanctioned way to reference
# them, matching check-no-orphan-turn-watchdog.sh's convention).
#
# Exit: 0 clean, 1 offender(s) found, 2 the check itself could not run.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="${REPO_ROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"
cd "$REPO_ROOT" || { echo "check-no-browser-turn-park: cannot cd to $REPO_ROOT" >&2; exit 2; }

REQUIRED_DIRS=(src/components/browser pkg/agent pkg/tools/browser)
for d in "${REQUIRED_DIRS[@]}"; do
  if [ ! -d "$d" ]; then
    echo "check-no-browser-turn-park: expected directory '$d' not found under $REPO_ROOT" >&2
    echo "  (wrong cwd or a partial checkout — refusing to report a green verdict" >&2
    echo "   for a tree this script never actually scanned)" >&2
    exit 2
  fi
done

STDERR_FILE="$(mktemp "${TMPDIR:-/tmp}/check-no-browser-turn-park-stderr.XXXXXX")"
trap 'rm -f "$STDERR_FILE"' EXIT

run_grep() {
  # $1 = pattern, $2 = --include glob(s) (space-separated, may repeat),
  # remaining = dirs/files. Never swallows a real grep failure (exit >1).
  local pattern="$1" includes="$2"; shift 2
  local -a include_args=()
  local inc
  for inc in $includes; do
    include_args+=(--include="$inc")
  done
  local out status
  out=$(grep -rnE "$pattern" "${include_args[@]}" "$@" 2>"$STDERR_FILE")
  status=$?
  if [ "$status" -gt 1 ]; then
    echo "check-no-browser-turn-park: grep failed (exit $status) scanning: $*" >&2
    cat "$STDERR_FILE" >&2
    exit 2
  fi
  printf '%s' "$out"
}

# Drop full-line //, /*, *, # comments — matches check-no-orphan-turn-
# watchdog.sh's established convention across this repo's guards.
strip_comments() {
  awk -F: '
    NF >= 3 {
      line = ""
      for (i = 3; i <= NF; i++) line = line (i > 3 ? ":" : "") $i
      sub(/^[[:space:]]+/, "", line)
      if (line ~ /^\/\//) next
      if (line ~ /^\*/) next
      if (line ~ /^\/\*/) next
      if (line ~ /^#/) next
      print $0
    }
  '
}

fail=0
findings=""

# --- Clause (a): the control-toggle affordance strings ---------------------

TOGGLE_PATTERN='Take control|Release control|Hand to agent'
toggle_hits="$(run_grep "$TOGGLE_PATTERN" '*.tsx *.ts' src/components/browser \
  | grep -v '\.test\.tsx:' | grep -v '\.test\.ts:' \
  | strip_comments || true)"

if [ -n "$toggle_hits" ]; then
  fail=1
  findings="${findings}
Clause (a) — control-toggle affordance string(s) under src/components/browser/ (ADR-038/040 retired this; do not reinstate):
$(printf '%s\n' "$toggle_hits" | sed 's/^/  /')"
fi

# --- Clause (b): a resume-dispatcher-shaped identifier in pkg/agent/ -------

RESUME_PATTERN='[A-Za-z]*[Rr]esume[A-Za-z]*(Browser|Wheel|Handover)[A-Za-z]*|[A-Za-z]*(Browser|Wheel|Handover)[A-Za-z]*[Rr]esume[A-Za-z]*'
resume_hits="$(run_grep "$RESUME_PATTERN" '*.go' pkg/agent \
  | grep -v '_test\.go:' \
  | strip_comments || true)"

if [ -n "$resume_hits" ]; then
  fail=1
  findings="${findings}
Clause (b) — a resume-dispatcher-shaped identifier in pkg/agent/ (the browser resume path must be an ORDINARY turn, no dispatcher):
$(printf '%s\n' "$resume_hits" | sed 's/^/  /')"
fi

# --- Clause (c): a turn-park/cancel entry point called from the browser ----
# path. REACHABILITY IS OUT OF SCOPE (R-19d) — this is a literal call-site
# scan of exactly these two locations, not a transitive call-graph proof.

PARK_PATTERN='\b(RequestCancel|RequestCancelForSession|RequestCancelByChannelChat|InterruptSessionHard)[[:space:]]*\('

browser_pkg_hits="$(run_grep "$PARK_PATTERN" '*.go' pkg/tools/browser \
  | grep -v '_test\.go:' \
  | strip_comments || true)"

deferral_file_hits=""
if [ -f pkg/agent/browser_deferral.go ]; then
  deferral_file_hits="$(run_grep "$PARK_PATTERN" '*.go' pkg/agent/browser_deferral.go \
    | strip_comments || true)"
fi

park_hits=""
if [ -n "$browser_pkg_hits" ] && [ -n "$deferral_file_hits" ]; then
  park_hits="${browser_pkg_hits}
${deferral_file_hits}"
elif [ -n "$browser_pkg_hits" ]; then
  park_hits="$browser_pkg_hits"
elif [ -n "$deferral_file_hits" ]; then
  park_hits="$deferral_file_hits"
fi

if [ -n "$park_hits" ]; then
  fail=1
  findings="${findings}
Clause (c) — a turn-park/cancel entry point called from the browser take path (a browser take-over must NEVER cancel, interrupt, suspend or park a turn):
$(printf '%s\n' "$park_hits" | sed 's/^/  /')"
fi

echo "=== check-no-browser-turn-park ==="
echo ""

if [ "$fail" -ne 0 ]; then
  echo "ERROR: ADR-085 Explicit Non-Behavior(s) reintroduced:" >&2
  printf '%s\n' "$findings" >&2
  echo "" >&2
  echo "These are explicitly, permanently out of scope — see" >&2
  echo "docs/internal/specs/browser-control-handover-spec.md 'Explicit Non-Behaviors" >&2
  echo "& Safeguards' and CLAUDE.md 'Retired surfaces'. If this came from a merge," >&2
  echo "resolve by KEEPING THE DELETION / not reintroducing the pattern." >&2
  exit 1
fi

echo "OK: no control-toggle strings, resume-dispatcher identifiers, or browser-path"
echo "    calls to a turn-park/cancel entry point found."
exit 0
