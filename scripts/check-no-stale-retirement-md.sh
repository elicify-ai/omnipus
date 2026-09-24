#!/usr/bin/env bash
# check-no-stale-retirement-md.sh
#
# Companion to check-no-subagent-special-case.sh (ADR-091 WP-F FR-F-003). That
# guard matches CODE symbols only — a live definition or call in *.go/*.ts/*.tsx/*.yaml,
# with a "// " comment prefix exempted because a retirement NOTE naming the symbol in
# prose is the desired outcome. It is blind to PROSE that is wrong on its own terms —
# three separate reviewer findings this round (ADR-091 fix round, lane 4) were exactly
# that:
#
#   (a) a deleted ADR-091 symbol listed under a "keep the deletion" heading in a
#       CLAUDE.md/AGENTS.md guide — instructing a maintainer to PRESERVE something this
#       delivery removed, the opposite of what that heading means everywhere else it
#       appears;
#   (b) a file header in the ADR-091 steering core (pkg/agent/steer_*.go) still telling
#       a reader its body is a CP-0 stub after the real implementation landed;
#   (c) a documented test-runner command naming the retired FR-047 guard test file
#       (deleted by residual_audit_test.go's "FR-047 guard gone" row) — running that
#       exact command matches zero files, and most JS test runners exit 0 on "0 tests
#       found", so the documented command is a silent false-green trap.
#
# Each sub-check is deliberately SCOPED, not a blanket ban on mentioning these names in
# prose — banning the names outright would fail legitimate documentation of the deletion
# itself (e.g. pkg/agent/CLAUDE.md's own "Delegation" section, which correctly says
# "the ParentDurableKey field ... are gone in the same delivery"). Scoping to "inside the
# keep-the-deletion heading's own section" / "inside the steering-core file header" /
# "immediately after a test-runner invocation token" is what tells a legitimate mention
# apart from a live regression.
#
# Exit: 0 clean, 1 offenders found, 2 the check itself could not run.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="${REPO_ROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"
cd "$REPO_ROOT" || { echo "check-no-stale-retirement-md: cannot cd to $REPO_ROOT" >&2; exit 2; }

for d in pkg src; do
  if [ ! -d "$d" ]; then
    echo "check-no-stale-retirement-md: expected directory '$d' not found under $REPO_ROOT" >&2
    echo "  (wrong cwd, a renamed package, or a partial checkout — refusing to report a green" >&2
    echo "   verdict for a tree this script never actually scanned)" >&2
    exit 2
  fi
done

FOUND=0

is_exempt_dir() {
  # $1: path relative to REPO_ROOT (no leading slash). Mirrors
  # check-agents-md-sync.sh's exemption list.
  case "$1" in
    node_modules/*|vendor/*|dist/*|.gitnexus/*|.git/*|pkg/api/generated/*|src/lib/api/generated/*|pkg/gateway/spa/*)
      return 0 ;;
    *)
      return 1 ;;
  esac
}

# ── (a) a deleted ADR-091 symbol under a "keeping the deletion" heading ───────────
# Vocabulary kept in lockstep with check-no-subagent-special-case.sh's BANNED_PATTERNS
# (ADR-091 D10) plus ProducingSessionID, which that script scopes to two files by name.
BANNED_NAMES=(
  'newEphemeralSession' 'maxEphemeralHistorySize' 'ephemeralSessionStore'
  'executeSync' 'DelegationModeAwait' 'allow_blocking_question'
  'parentTS\.channel' 'parentTS\.chatID' 'ParentDurableKey'
  'notifyParentIfAllSiblingsDone' 'emitNestedToolCalls' 'ProducingSessionID'
)
NAME_PATTERN="$(IFS='|'; echo "${BANNED_NAMES[*]}")"

while IFS= read -r -d '' md; do
  rel="${md#"$REPO_ROOT"/}"
  is_exempt_dir "$rel" && continue

  # The "keeping the deletion" section: from its heading line to the next
  # heading of any level (or EOF). Matched case-sensitively on the phrase
  # itself, not just the section's exact wording — every guide that carries
  # this heading uses "resolve merges by keeping the deletion" verbatim
  # (checked against pkg/agent/CLAUDE.md and the repo root CLAUDE.md at
  # authoring time), so this is intentionally exact, not fuzzy.
  section="$(awk '
    /keeping the deletion/ { grab=1; next }
    grab && /^#/ { exit }
    grab { print }
  ' "$md")"
  [ -n "$section" ] || continue

  hit="$(printf '%s\n' "$section" | grep -nE "$NAME_PATTERN" || true)"
  if [ -n "$hit" ]; then
    while IFS= read -r line; do
      echo "check-no-stale-retirement-md: $rel: a deleted ADR-091 symbol appears under a \"keeping the deletion\" heading — that heading means KEEP, never applies to something ADR-091 itself removed: $line" >&2
    done <<< "$hit"
    FOUND=1
  fi
done < <(find . \( -name .git -o -name node_modules -o -name vendor -o -name dist -o -name .gitnexus \) -prune -o -type f \( -name CLAUDE.md -o -name AGENTS.md \) -print0)

# ── (b) a stale "CP-0 stub bodies only" header in the steering core ──────────────
# Scoped to pkg/agent/steer_*.go (non-test) — the ADR-091 delivery's own implementation
# files, per pkg/agent/CLAUDE.md's "The four implementation files" list plus their
# siblings. A stub note elsewhere in the tree (e.g. an unrelated ADR's own staged
# rollout) is out of scope on purpose; this check only knows the steering core must be
# real by the time this delivery ships.
while IFS= read -r -d '' f; do
  rel="${f#"$REPO_ROOT"/}"
  case "$rel" in *_test.go) continue ;; esac
  hit="$(head -n 15 "$f" | grep -nE 'stub bodies only' || true)"
  if [ -n "$hit" ]; then
    while IFS= read -r line; do
      echo "check-no-stale-retirement-md: $rel: header still claims CP-0 stub bodies — this delivery's steering core ships real implementations, so this line is stale: $line" >&2
    done <<< "$hit"
    FOUND=1
  fi
done < <(find pkg/agent -maxdepth 1 -type f -name 'steer_*.go' -print0 2>/dev/null)

# ── (c) a test-runner command naming the retired FR-047 guard file ───────────────
RETIRED_TEST='__adr057__noSubagentMessageOrStateReferences\.test\.ts'
RUNNER_CMD_PATTERN="(vitest( run)?|npx vitest|jest|npm test).*${RETIRED_TEST}"
while IFS= read -r -d '' md; do
  rel="${md#"$REPO_ROOT"/}"
  is_exempt_dir "$rel" && continue

  hit="$(grep -nE "$RUNNER_CMD_PATTERN" "$md" || true)"
  if [ -n "$hit" ]; then
    while IFS= read -r line; do
      echo "check-no-stale-retirement-md: $rel: documents a test-runner command naming the retired FR-047 guard file — that file is deleted, the command matches zero tests, and most runners exit 0 on 0 matched tests (silent false-green): $line" >&2
    done <<< "$hit"
    FOUND=1
  fi
done < <(find . \( -name .git -o -name node_modules -o -name vendor -o -name dist -o -name .gitnexus \) -prune -o -type f -name '*.md' -print0)

if [ "$FOUND" -eq 1 ]; then
  echo "" >&2
  echo "check-no-stale-retirement-md: stale prose found. check-no-subagent-special-case.sh" >&2
  echo "cannot see markdown or comments; this guard exists to catch the drift that one is" >&2
  echo "structurally blind to." >&2
  exit 1
fi

exit 0
