#!/usr/bin/env bash
# check-no-stale-retirement-md.test.sh — proof-of-failure companion for
# check-no-stale-retirement-md.sh. Each case builds a minimal throwaway tree, injects
# one fixture, runs the guard against it (via REPO_ROOT) and checks the exit code (and,
# for a violation, that the guard names the offending line).
#
# NOTE on the real tree: unlike check-no-subagent-special-case.test.sh, this file does
# NOT assert "the real tree is clean" as its first case. Sub-check (b) (stale "CP-0 stub
# bodies only" headers in pkg/agent/steer_*.go) is CURRENTLY, LEGITIMATELY red on the
# real tree at authoring time — pkg/agent/steer_audience.go and pkg/agent/steer_cancel.go
# both still open with a CP-0-stub header despite carrying real, ~370-400 line
# implementations. That is lane 1's (steer_audience.go) and lane 2's (steer_cancel.go)
# file to fix, not this guard's to hide — see the ADR-091 fix-round lane 4 report.
#
# Exit: 0 all cases pass, 1 a case failed, 2 the test itself could not run.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GUARD="$SCRIPT_DIR/check-no-stale-retirement-md.sh"
[ -f "$GUARD" ] || { echo "guard not found: $GUARD" >&2; exit 2; }

WORK="$(mktemp -d)" || exit 2
trap 'rm -rf "$WORK"' EXIT

pass=0
fail=0

# case <name> <expected-exit> <relative-file> <content-file-or-literal> [<must-appear>]
# Content is passed as a here-string via the 4th arg (may be multi-line).
case_run() {
  local name="$1" want="$2" rel="$3" content="$4" must="${5:-}"
  local tree="$WORK/$name"
  mkdir -p "$tree/pkg/agent" "$tree/src" "$(dirname "$tree/$rel")"
  printf '%s\n' "$content" > "$tree/$rel"
  local out got
  out="$(REPO_ROOT="$tree" bash "$GUARD" 2>&1)"
  got=$?
  if [ "$got" -ne "$want" ]; then
    echo "FAIL [$name]: exit $got, want $want"; echo "$out"; fail=$((fail + 1)); return
  fi
  if [ -n "$must" ] && ! printf '%s' "$out" | grep -q -- "$must"; then
    echo "FAIL [$name]: output does not name '$must'"; echo "$out"; fail=$((fail + 1)); return
  fi
  echo "PASS [$name]: exit $got"; pass=$((pass + 1))
}

# ── (a) "keeping the deletion" heading ──────────────────────────────────────────

case_run keep-section-violation 1 pkg/agent/CLAUDE.md \
'# pkg/agent

## Retired surfaces — resolve merges by keeping the deletion

- ParentDurableKey stays, per an earlier draft.
' 'ParentDurableKey'

case_run keep-section-ok 0 pkg/agent/CLAUDE.md \
'# pkg/agent

## Retired surfaces — resolve merges by keeping the deletion

- Goal confirm-gate machinery (ADR-088): deleted, do not reintroduce.
'

# The exact real-repo shape (pkg/agent/CLAUDE.md line 54): naming a deleted symbol as
# GONE, outside the keep-the-deletion section, must NOT trip the guard.
case_run narrative-mention-outside-section-ok 0 pkg/agent/CLAUDE.md \
'# pkg/agent

## Delegation

The ParentDurableKey field is gone in this delivery.

## Retired surfaces — resolve merges by keeping the deletion

- Goal confirm-gate machinery (ADR-088): deleted, do not reintroduce.
'

# ── (b) stale CP-0 stub header in the steering core ─────────────────────────────

case_run stub-header-violation 1 pkg/agent/steer_lane4fixture.go \
'// Owner: WP-D. WP-A (this lane) writes this file'"'"'s CP-0 stub bodies only; WP-D
// replaces them with the real implementation at CP-3.
package agent
' 'CP-0 stub'

case_run stub-header-ok-nonsteer 0 pkg/agent/notsteer_lane4fixture.go \
'// This file'"'"'s CP-0 stub bodies only, replaced later. (Deliberately out of the
// steer_*.go scope this check applies to.)
package agent
'

case_run stub-header-ok-realimpl 0 pkg/agent/steer_lane4fixture2.go \
'// SteerLane4Fixture implements the ADR-091 fixture used only by this test.
package agent

func SteerLane4Fixture() {}
'

# ── (c) documented command naming the retired FR-047 guard file ────────────────

case_run stale-command-violation 1 docs/internal/howto-test.md \
'Run the SPA guard directly:

    npx vitest run src/lib/__adr057__noSubagentMessageOrStateReferences.test.ts
' '__adr057__noSubagentMessageOrStateReferences'

# The exact real-repo shape (several docs/internal/specs/*.md files): narrating that the
# file was retired, with no runner-invocation token in front of it, must NOT trip the
# guard.
case_run stale-command-ok-narrative 0 docs/internal/specs/some-spec.md \
'The guard `src/lib/__adr057__noSubagentMessageOrStateReferences.test.ts` is retired;
ADR-091 D7 supersedes FR-047.
'

echo "Results: $pass passed, $fail failed"
[ "$fail" -eq 0 ] || exit 1
exit 0
